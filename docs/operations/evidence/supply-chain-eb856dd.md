# Supply-chain patch evidence for `eb856dd`

Local Linux execution on 2026-10-17 targeted commit
`eb856ddb3a1d4a23be0dc76c475d8355cf9a6f3f`. Reports and release artifacts are
kept out of Git; the hashes below identify the exact generated files without
storing machine paths, credentials, or full scanner output in the repository.

## Dependency remediation

The application directly imports `golang.org/x/crypto/argon2`. The selected
module changed from `golang.org/x/crypto v0.41.0` to `v0.56.0`, the first
release that fixes all High/Critical advisories known to the scanners used by
this run. Its compatible module graph required these transitive updates:

| Module | Before | After |
| --- | --- | --- |
| `golang.org/x/crypto` | `v0.41.0` | `v0.56.0` |
| `golang.org/x/mod` | `v0.27.0` | `v0.38.0` |
| `golang.org/x/net` | `v0.43.0` | `v0.57.0` |
| `golang.org/x/sync` | `v0.16.0` | `v0.22.0` |
| `golang.org/x/sys` | `v0.36.0` | `v0.47.0` |
| `golang.org/x/text` | `v0.28.0` | `v0.41.0` |
| `golang.org/x/tools` | `v0.36.0` | `v0.48.0` |

The seven Critical module-level matches previously reported for `x/crypto
v0.41.0` were:

| Grype advisory | CVE | Go advisory | Fixed |
| --- | --- | --- | --- |
| `GHSA-5cgq-3rg8-m6cv` | `CVE-2026-42508` | `GO-2026-5021` | `v0.52.0` |
| `GHSA-89gr-r52h-f8rx` | `CVE-2026-39831` | `GO-2026-5019` | `v0.52.0` |
| `GHSA-f5wc-c3c7-36mc` | `CVE-2026-39832` | `GO-2026-5006` | `v0.52.0` |
| `GHSA-jppx-rxg9-jmrx` | `CVE-2026-39833` | `GO-2026-5005` | `v0.52.0` |
| `GHSA-rm3j-f69w-wqmq` | `CVE-2026-39834` | `GO-2026-5020` | `v0.52.0` |
| `GHSA-vgwf-h737-ff37` | `CVE-2026-39830` | `GO-2026-5017` | `v0.52.0` |
| `GHSA-x527-x647-q7gg` | `CVE-2026-46595` | `GO-2026-5023` | `v0.52.0` |

The same affected release also had two High advisories,
`GHSA-q4h4-gmj2-qvw2` / `CVE-2026-46597` / `GO-2026-5013` and
`GHSA-w879-237q-wc7r` / `CVE-2026-39829` / `GO-2026-5018`, both fixed in
`v0.52.0`. Version `v0.56.0` was required rather than `v0.52.0` because it is
the first release also fixing `GO-2026-6303` (fixed in `v0.55.0`) and
`GO-2026-6354`/`GO-2026-6355` (fixed in `v0.56.0`).

## Patched result

- Grype found **0 Critical and 0 High** matches. Its only match was the
  severity-`Unknown` module-level `GO-2026-5932`, which concerns the deprecated
  `golang.org/x/crypto/openpgp` package; the binaries use `argon2` and contain no
  OpenPGP package. This is not a High/Critical policy exception.
- `govulncheck` reported the same `GO-2026-5932` at module/package inventory
  level and found **0 reachable vulnerabilities**.
- The source CycloneDX SBOM has 79 components and the extracted-release SBOM
  has 90; both record `golang.org/x/crypto v0.56.0`.
- The forbidden/restricted license gate passed with 30 inventory rows,
  including the pinned manual review for `modernc.org/mathutil v1.7.1`.
- `go test ./...`, `go test -race ./...`, `go vet ./...`, `make contract`, the
  validator's pass/fail fixtures, four cross-builds, checksum verification,
  archive validation, reproducibility comparison, and both Linux native smokes
  passed.

Tools: Go `1.27.2`, Python `3.14.4`, govulncheck `1.9.0` (database updated
2026-10-08), go-licenses `1.6.0`, cyclonedx-gomod `1.9.0`, Syft `1.54.1`, and
Grype `0.120.1` (database schema `v6.1.10`, built 2026-10-17T06:27:09Z).

## Artifact hashes

```text
a5d1394a132bd8cf8ada2d36f64c3ff6b974607cf77611b21cb37f555abf82a5  bord-server_v0.0.0-supply-chain-patched_linux_amd64.tar.gz
e81efcb1a2bb815d4445477bf380298cafdbeb949ea2edf573d040b83d978820  bord_v0.0.0-supply-chain-patched_darwin_amd64.tar.gz
8d587553c7046d7b7b278aa0b440dbe89abfaedd14f801d746654cf704691195  bord_v0.0.0-supply-chain-patched_linux_amd64.tar.gz
93092c4d964b80c881de3db29da1636d9bc841b73684bc5dcfae322bfdaa751f  bord_v0.0.0-supply-chain-patched_windows_amd64.zip
9b6ca61e95cf79e495595dd81647ac2c95ba841acec8b67212a330f1ee507fb6  dependency-licenses.csv
952817c576e57b2aafd59b205266b05576fb73f37301c14fe78383bddb8a7a52  govulncheck.json
b5f080e7bdbfa079ce4f428155a25d1285d875e77556c73cddd434bfd2477832  grype.json
c4170d2381dffcc26ca7f063b2192b66da2229f4d87c79572702090c24f0be0b  release-bom.cdx.json
afe2d8fbb3e8d2a23e62cefd5c864ac068a98b6e33dc03f08b19b9efcb73bcc3  source-bom.cdx.json
```

## Task 7.7 status

Task 7.7 remains unchecked. Local artifact validation now has no High/Critical
findings, but it still cannot validate GitHub's uploaded artifact container or
hosted macOS/Windows runner output. The completion gate remains a successful run
of the updated workflows after push, followed by download and validation of the
`release-*` and `security-evidence` artifacts.
