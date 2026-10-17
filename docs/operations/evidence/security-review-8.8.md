# Security and privacy review — OpenSpec 8.8

Date: 2026-10-17. Review base: merged `main` at `cd0cae0`; remediation is in
the commit containing this report. This document intentionally contains no
credentials, key material, raw addresses, request bodies, or scanner payloads.

## Result

**Open Critical: 0. Open High: 0.** OpenSpec 8.8 is complete. Tasks 7.7 and
8.9 remain open because hosted CI/release-artifact evidence is outside this
local review; neither task is changed by this report.

## Severity disposition

| ID | Severity | Disposition | Evidence |
| --- | --- | --- | --- |
| Prior H1 | High | Closed | Untrusted board, post, ID, state and error text pass through terminal C0/C1/CSI/OSC neutralization; regressions include OSC52 and multiline content. |
| Prior H2 | High | Closed | Admin authentication carries a copied request actor; endpoint operations enforce reports/moderator/administrator roles, reject request-ID collisions, and commit successful mutations with audit rows in one transaction. Failed attempts are separately audited. |
| Prior H3 | High | Closed | Production composes concrete Public, Media, Abuse, Admin, storage, outbox, trusted-proxy, retention and keyring adapters. Public and management routers remain separated, and integration/operator tests exercise the live composition. |
| Prior H4 | High | Closed | Media accepts only JPEG/PNG with exact end markers, pre-decode dimensions and 16 MP bounds, 5 MiB normalized input/output, cancellation, metadata stripping and a two-upload concurrency budget. |
| H5 | High | Fixed here | Delete verifiers were client supplied and could select the parser's maximum Argon2 cost (256 MiB, 10 iterations, parallelism 16), turning later delete attempts into resource exhaustion. Creation now accepts only the approved versioned Argon2 profile; adversarial parsing tests use a synthetic high-cost verifier without executing it. |
| H6 | High | Fixed here | SSE subscriber state and per-client queues had no global admission cap. The hub now rejects admission at 1,024 live clients and immediately reuses capacity after close. |
| H7 | High | Fixed here | Owner-delete abuse checks left request-scoped subject entries in the in-memory pending map, and concurrent trusted-edge request-ID reuse could overwrite another request's pending subject. The pipeline now atomically rejects active collisions and delete completion/errors discard state; E2E verifies the map returns to zero. |
| M1 | Medium | Fixed here | Backup verification read an unbounded manifest and followed manifested symlinks before the later tree walk rejected them. Verification now caps the manifest at 8 MiB, uses strict single-object JSON, and requires every manifested entry to be a regular file before hashing or SQLite access. |
| M2 | Medium | Fixed here | HTTP header size relied on Go's larger implicit default. Public and management listeners now explicitly cap headers at 64 KiB in addition to read-header/body timeouts. |

No Critical finding was confirmed. No Critical or High risk was accepted or
deferred.

## Reviewed boundaries

- Strict JSON, path/query/header bounds, page/cursor limits, body/media limits,
  upload concurrency, SSE replay/queues/admission, and HTTP read/header limits.
- Terminal rendering, SSE reset/resume behavior, local state/cache atomicity and
  permissions, checksum verification, and shell-free external openers.
- Session/delete/admin secrets, Argon2 parameter bounds, domain-separated HMAC
  uses, distinct lookup/encryption keys, AES-GCM envelope key IDs, rotation
  readiness and resumable offline re-encryption.
- Log/audit redaction, trusted-proxy walking, server-generated request IDs,
  admin RBAC/actor lifetime, mutation/audit atomicity, and public/management
  listener separation.
- Image structure/decode bounds, content-addressed blob confinement, backup
  path/symlink/checksum/SQLite integrity checks, 90-day retention boundaries,
  legal holds, active-ban exceptions, and destructive operator confirmations.

## Supply-chain evidence

`docs/operations/evidence/supply-chain-eb856dd.md` records the upgrade of
`golang.org/x/crypto` from v0.41.0 to v0.56.0 and its compatible `x/*` graph.
The recorded local run found 0 reachable vulnerabilities and 0 High/Critical
Grype findings; the sole inventory finding was severity Unknown for the unused
deprecated OpenPGP package. Source and extracted-release CycloneDX SBOMs,
license inventory, scanner reports and release archives have SHA-256 identifiers
in that evidence file. Task 7.7 correctly remains open until hosted artifacts
are downloaded and checked; this is not a local Critical/High exception.

## Verification commands

```text
go test ./...
go test -race ./...
FUZZTIME=5s REPEAT=5 ./scripts/quality-gate.sh
go vet ./...
make contract
git diff --check
```

Targeted adversarial regressions:

```text
go test ./internal/security -run 'ValidateVerifierParameters|SecretVectors'
go test ./internal/integration -run 'DecodeVerifier|ProductionRuntimeE2E|Key'
go test ./internal/events -run 'ResourceLimit|ReplayReset|SSE'
go test ./internal/operations/backup -run 'BoundsManifest|Tampering|NonPortable|Overlap'
go test ./internal/api ./internal/media ./internal/client ./internal/server
```
