# Supply-chain evidence for `a00cb00`

Local Linux execution on 2026-10-17 targeted commit
`a00cb00e89fb357be9f6f9454d5c9336e7d35d17`. No credentials, environment dumps,
absolute paths, or full scanner reports are stored in this repository.

## Result

- Four archives were built twice with `SOURCE_DATE_EPOCH=1792267749`; both
  `SHA256SUMS` files were identical, archive validation passed, and both Linux
  binaries reported the expected version and commit.
- `govulncheck` found 0 reachable vulnerabilities. Grype found 7 Critical
  module-level matches for `golang.org/x/crypto v0.41.0` in stripped binaries;
  these lack function symbols and none is reachable according to govulncheck.
- The forbidden/restricted license policy passed. The inventory has 30 rows,
  including the pinned manual review of `modernc.org/mathutil v1.7.1`.
- The source CycloneDX SBOM has 79 components; the CycloneDX SBOM generated from
  extracted release binaries has 90 components. Scanning archives directly was
  rejected because it yielded an empty SBOM.

Tools: Go `1.27.2`, Python `3.14.4`, govulncheck `1.9.0` (database updated
2026-10-08), go-licenses `1.6.0`, cyclonedx-gomod `1.9.0`, Syft `1.54.1`, and
Grype `0.120.1` (database schema `v6.1.10`, built 2026-10-17T06:27:09Z).

## Artifact hashes

```text
15e6da892f4ee8bdb613c6e4be753f9eb9107d1073b4005c6660301e1e584193  bord-server_v0.0.0-supply-chain_linux_amd64.tar.gz
79e5fce4fc49052472f7e827bec4ebd90fe330b07d021959682c74fa638eb357  bord_v0.0.0-supply-chain_darwin_amd64.tar.gz
f095cce0166ccadc0670835a579fb16dbadf8610de7ce341b5cbbc74a7b212a0  bord_v0.0.0-supply-chain_linux_amd64.tar.gz
91c85431303720c706072b96394cddd86c318a181a9bd6157f519ee0ea5b49dd  bord_v0.0.0-supply-chain_windows_amd64.zip
f9156f15794bd3f1cf1f8ea5f51a823fe7792f789bc4e9b4ba609608e489241f  SHA256SUMS
125c80795503799db94ee178a6321e4e1d89eaf8c1528bba25302aaec299b0ad  dependency-licenses.csv
736ca8645c8df0159dd16ef32405041822aba9a5214b425db2d64ca6998c7cf9  govulncheck.json
3fe6bec8809953d2654c5609e0d0d1a558cff505d638f9c16dfcb8f04dd4f7ce  source-bom.cdx.json
895adcb7412e3f79746268a67056f6e2d3b5c92dce829a75e80ffc6ab15eda7e  release-bom.cdx.json
7bdeff11800afddff130fd39a9fb1fba780969551c7c8c1894e95250c898f466  grype.json
```

The evidence files are reproducible by the pinned workflow/tool commands; SBOM
and scanner hashes identify this run and can change with timestamps or database
updates, while release archives are byte-reproducible for the target commit.

## Task 7.7 status

Task 7.7 remains unchecked. This local run verifies the workflow's Linux build,
archive, checksum, SBOM, reachability, and license logic, but it does **not**
verify GitHub's uploaded artifact container or hosted macOS/Windows runner output.
The exact external completion gate is a successful run of the updated workflows
at this commit after they are pushed, followed by download and validation of the
`release-*` and `security-evidence` artifacts. The workflows now retain all
reports and fail on reachable Go vulnerabilities, disallowed licenses, empty
SBOMs, malformed evidence, checksum mismatch, or unsafe archive contents.
