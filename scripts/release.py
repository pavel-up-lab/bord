#!/usr/bin/env python3
"""Build deterministic cross-platform release archives and SHA-256 checksums."""

from __future__ import annotations

import argparse
import gzip
import hashlib
import io
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import time
import zipfile


TARGETS = (
    ("bord", "linux", "amd64"),
    ("bord", "darwin", "amd64"),
    ("bord", "windows", "amd64"),
    ("bord-server", "linux", "amd64"),
)


def run(*args: str, env: dict[str, str] | None = None) -> str:
    return subprocess.check_output(args, env=env, text=True).strip()


def archive_tar(source: Path, output: Path, epoch: int) -> None:
    raw = io.BytesIO()
    with tarfile.open(fileobj=raw, mode="w", format=tarfile.PAX_FORMAT) as tar:
        for path in sorted(source.iterdir(), key=lambda item: item.name):
            info = tar.gettarinfo(str(path), arcname=f"{source.name}/{path.name}")
            info.uid = info.gid = 0
            info.uname = info.gname = ""
            info.mtime = epoch
            with path.open("rb") as file:
                tar.addfile(info, file)
    with output.open("wb") as file:
        with gzip.GzipFile(filename="", mode="wb", fileobj=file, mtime=epoch) as zipped:
            zipped.write(raw.getvalue())


def archive_zip(source: Path, output: Path, epoch: int) -> None:
    stamp = time.gmtime(max(epoch, 315532800))[:6]
    with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
        for path in sorted(source.iterdir(), key=lambda item: item.name):
            info = zipfile.ZipInfo(f"{source.name}/{path.name}", stamp)
            info.create_system = 3
            info.external_attr = (0o755 if path.name.endswith(".exe") else 0o644) << 16
            archive.writestr(info, path.read_bytes(), compress_type=zipfile.ZIP_DEFLATED, compresslevel=9)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--version", required=True)
    parser.add_argument("--output", default="dist")
    options = parser.parse_args()
    if not options.version.startswith("v"):
        parser.error("version must start with v")

    root = Path(__file__).resolve().parent.parent
    output = (root / options.output).resolve()
    if output == root or root not in output.parents:
        parser.error("output must be inside the repository")
    shutil.rmtree(output, ignore_errors=True)
    output.mkdir(parents=True)
    commit = run("git", "rev-parse", "HEAD")
    epoch = int(os.environ.get("SOURCE_DATE_EPOCH", run("git", "show", "-s", "--format=%ct", "HEAD")))
    build_date = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(epoch))

    with tempfile.TemporaryDirectory(prefix="bord-release-") as temporary:
        temp = Path(temporary)
        for command, goos, goarch in TARGETS:
            name = f"{command}_{options.version}_{goos}_{goarch}"
            stage = temp / name
            stage.mkdir()
            binary = command + (".exe" if goos == "windows" else "")
            target = stage / binary
            env = os.environ.copy()
            env.update({"CGO_ENABLED": "0", "GOOS": goos, "GOARCH": goarch})
            if command == "bord-server":
                ldflags = f"-s -w -X bord/internal/operations/command.Version={options.version} -X bord/internal/operations/command.Commit={commit} -X bord/internal/operations/command.BuildDate={build_date}"
            else:
                ldflags = f"-s -w -X main.version={options.version} -X main.commit={commit} -X main.buildDate={build_date}"
            subprocess.run(
                ["go", "build", "-trimpath", "-buildvcs=false", "-ldflags", ldflags, "-o", str(target), f"./cmd/{command}"],
                cwd=root,
                env=env,
                check=True,
            )
            shutil.copy2(root / "LICENSE", stage / "LICENSE")
            shutil.copy2(root / "THIRD_PARTY_NOTICES", stage / "THIRD_PARTY_NOTICES")
            archive = output / (name + (".zip" if goos == "windows" else ".tar.gz"))
            (archive_zip if goos == "windows" else archive_tar)(stage, archive, epoch)

    checksums = []
    for path in sorted(output.iterdir(), key=lambda item: item.name):
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        checksums.append(f"{digest}  {path.name}\n")
    (output / "SHA256SUMS").write_text("".join(checksums), encoding="ascii")


if __name__ == "__main__":
    main()
