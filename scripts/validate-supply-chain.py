#!/usr/bin/env python3
"""Validate the machine-readable evidence emitted by the security/release jobs."""

from __future__ import annotations

import argparse
import csv
import json
from pathlib import Path


def json_stream(path: Path) -> list[dict]:
    text = path.read_text(encoding="utf-8")
    decoder = json.JSONDecoder()
    values = []
    offset = 0
    while offset < len(text):
        while offset < len(text) and text[offset].isspace():
            offset += 1
        if offset < len(text):
            value, offset = decoder.raw_decode(text, offset)
            values.append(value)
    return values


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--directory", required=True, type=Path)
    parser.add_argument("--release", action="store_true")
    options = parser.parse_args()
    directory = options.directory

    events = json_stream(directory / "govulncheck.json")
    findings = [event["finding"] for event in events if "finding" in event]
    reachable = [
        finding
        for finding in findings
        if any(frame.get("function") for frame in finding.get("trace", []))
    ]
    if reachable:
        raise SystemExit(
            "reachable Go vulnerabilities: "
            + ", ".join(sorted({finding["osv"] for finding in reachable}))
        )

    with (directory / "dependency-licenses.csv").open(
        encoding="utf-8", newline=""
    ) as handle:
        licenses = list(csv.reader(handle))
    if not licenses:
        raise SystemExit("empty dependency license inventory")

    source = json.loads((directory / "source-bom.cdx.json").read_text())
    if source.get("bomFormat") != "CycloneDX" or not source.get("components"):
        raise SystemExit("source SBOM is not a populated CycloneDX document")

    message = [
        f"source components={len(source['components'])}",
        f"license rows={len(licenses)}",
        f"reachable Go vulnerabilities={len(reachable)}",
    ]
    if options.release:
        release = json.loads((directory / "release-bom.cdx.json").read_text())
        if release.get("bomFormat") != "CycloneDX" or not release.get("components"):
            raise SystemExit("release SBOM is not a populated CycloneDX document")
        scan = json.loads((directory / "grype.json").read_text())
        critical = [
            match
            for match in scan.get("matches", [])
            if match.get("vulnerability", {}).get("severity", "").lower()
            == "critical"
        ]
        message.extend(
            [
                f"release components={len(release['components'])}",
                f"critical module-level release findings={len(critical)}",
            ]
        )
    print("validated supply-chain evidence: " + ", ".join(message))


if __name__ == "__main__":
    main()
