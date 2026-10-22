#!/usr/bin/env python3
"""Validate release archives without executing foreign-platform binaries."""

from __future__ import annotations

import argparse
import hashlib
from pathlib import Path, PurePosixPath
import shutil
import tarfile
import zipfile


TARGETS = (
    ("bord", "linux", "amd64", ".tar.gz", "bord"),
    ("bord", "linux", "arm64", ".tar.gz", "bord"),
    ("bord", "darwin", "amd64", ".tar.gz", "bord"),
    ("bord", "darwin", "arm64", ".tar.gz", "bord"),
    ("bord", "windows", "amd64", ".zip", "bord.exe"),
    ("bord", "windows", "arm64", ".zip", "bord.exe"),
    ("bord-admin", "linux", "amd64", ".tar.gz", "bord-admin"),
    ("bord-admin", "linux", "arm64", ".tar.gz", "bord-admin"),
    ("bord-admin", "darwin", "amd64", ".tar.gz", "bord-admin"),
    ("bord-admin", "darwin", "arm64", ".tar.gz", "bord-admin"),
    ("bord-admin", "windows", "amd64", ".zip", "bord-admin.exe"),
    ("bord-admin", "windows", "arm64", ".zip", "bord-admin.exe"),
    ("bord-server", "linux", "amd64", ".tar.gz", "bord-server"),
)
COMMON_SUPPORT_FILES = {"LICENSE", "THIRD_PARTY_NOTICES"}


def safe_members(names: list[str], root: str, binary: str) -> None:
    support_files = set(COMMON_SUPPORT_FILES)
    if root.startswith("bord_") or root.startswith("bord-admin_"):
        support_files.add("README.md")
    expected = {f"{root}/{name}" for name in support_files}
    expected.add(f"{root}/{binary}")
    if set(names) != expected:
        raise SystemExit(f"unexpected archive members for {root}: {sorted(names)}")
    for name in names:
        path = PurePosixPath(name)
        if path.is_absolute() or ".." in path.parts:
            raise SystemExit(f"unsafe archive member: {name}")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--directory", required=True, type=Path)
    parser.add_argument("--version", required=True)
    parser.add_argument("--extract", type=Path)
    parser.add_argument("--tui-only", action="store_true")
    parser.add_argument("--admin-only", action="store_true")
    options = parser.parse_args()
    if options.tui_only and options.admin_only:
        parser.error("--tui-only and --admin-only are mutually exclusive")
    targets = tuple(
        target for target in TARGETS
        if (not options.tui_only or target[0] == "bord")
        and (not options.admin_only or target[0] == "bord-admin")
    )
    checksums = {}
    for line in (options.directory / "SHA256SUMS").read_text(encoding="ascii").splitlines():
        digest, name = line.split("  ", 1)
        checksums[name] = digest
    if len(checksums) != len(targets):
        raise SystemExit(f"SHA256SUMS must describe exactly {len(targets)} release archives")
    if options.extract:
        if options.extract.exists():
            shutil.rmtree(options.extract)
        options.extract.mkdir()
    for command, goos, goarch, suffix, binary in targets:
        root = f"{command}_{options.version}_{goos}_{goarch}"
        name = root + suffix
        archive = options.directory / name
        if hashlib.sha256(archive.read_bytes()).hexdigest() != checksums.get(name):
            raise SystemExit(f"checksum mismatch: {name}")
        if suffix == ".zip":
            with zipfile.ZipFile(archive) as handle:
                names = handle.namelist()
                safe_members(names, root, binary)
                executable = handle.read(f"{root}/{binary}")
                if options.extract:
                    handle.extractall(options.extract)
        else:
            with tarfile.open(archive, "r:gz") as handle:
                names = handle.getnames()
                safe_members(names, root, binary)
                member = handle.getmember(f"{root}/{binary}")
                if member.mode & 0o111 == 0:
                    raise SystemExit(f"binary is not executable: {name}")
                stream = handle.extractfile(member)
                assert stream is not None
                executable = stream.read()
                if options.extract:
                    handle.extractall(options.extract, filter="data")
        for metadata in (options.version.encode(),):
            if metadata not in executable:
                raise SystemExit(f"missing embedded version in {name}")
    print("validated archives: " + ", ".join(sorted(checksums)))


if __name__ == "__main__":
    main()
