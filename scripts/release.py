#!/usr/bin/env python3
"""Build deterministic cross-platform release archives and SHA-256 checksums."""

from __future__ import annotations

import argparse
import gzip
import hashlib
import io
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile
import time
import zipfile


TARGETS = (
    ("bord", "linux", "amd64"),
    ("bord", "linux", "arm64"),
    ("bord", "darwin", "amd64"),
    ("bord", "darwin", "arm64"),
    ("bord", "windows", "amd64"),
    ("bord", "windows", "arm64"),
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
    parser.add_argument("--tui-only", action="store_true", help="package only cross-platform bord clients")
    options = parser.parse_args()
    if re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z][0-9A-Za-z.-]*)?", options.version) is None:
        parser.error("version must be a safe semantic version beginning with v")

    root = Path(__file__).resolve().parent.parent
    output = (root / options.output).resolve()
    safe_output = re.fullmatch(
        r"dist(?:-[A-Za-z0-9][A-Za-z0-9._-]*)?", output.name
    )
    if output.parent != root or safe_output is None:
        parser.error("output must be a top-level dist or dist-NAME directory")
    shutil.rmtree(output, ignore_errors=True)
    output.mkdir(parents=True)
    commit = run("git", "rev-parse", "HEAD")
    epoch = int(os.environ.get("SOURCE_DATE_EPOCH", run("git", "show", "-s", "--format=%ct", "HEAD")))
    build_date = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(epoch))

    with tempfile.TemporaryDirectory(prefix="bord-release-") as temporary:
        temp = Path(temporary)
        targets = tuple(target for target in TARGETS if not options.tui_only or target[0] == "bord")
        for command, goos, goarch in targets:
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
            if command == "bord":
                shutil.copy2(root / "docs" / "tui.md", stage / "README.md")
            archive = output / (name + (".zip" if goos == "windows" else ".tar.gz"))
            (archive_zip if goos == "windows" else archive_tar)(stage, archive, epoch)

    checksums = []
    for path in sorted(output.iterdir(), key=lambda item: item.name):
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        checksums.append(f"{digest}  {path.name}\n")
    (output / "SHA256SUMS").write_text("".join(checksums), encoding="ascii")
    if options.tui_only:
        (output / "README.txt").write_text(
            "Bord TUI " + options.version + "\n\n"
            "Choose the archive matching your operating system and CPU architecture.\n"
            "Extract it and run bord (bord.exe on Windows). The complete Russian\n"
            "installation and usage guide is included as README.md in every archive.\n\n"
            "Verify the downloaded archive against SHA256SUMS before extraction.\n",
            encoding="utf-8",
        )


if __name__ == "__main__":
    main()
