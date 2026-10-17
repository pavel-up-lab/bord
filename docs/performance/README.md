# MVP load profile

The bounded profile uses the production SQLite configuration, migrations,
filesystem blob store, board services, anti-abuse checks, HTTP router, outbox
dispatcher and SSE hub. It starts an isolated server and data directory, sends no
external traffic, and writes compact JSON without hostnames, IPs, request bodies
or credentials.

Run the release profile from a clean checkout:

```sh
./scripts/load-profile.sh /tmp/bord-load-profile.json
```

Defaults are three 10-second phases: 100 public reads/s, 10 creates/s, and 50 SSE
clients receiving 2 created-post events/s. To investigate scaling, override
`BORD_LOAD_DURATION`, `BORD_LOAD_READ_RPS`, `BORD_LOAD_WRITE_RPS`,
`BORD_LOAD_SSE_CLIENTS`, or `BORD_LOAD_SSE_RPS`. The report records commit,
toolchain/hardware, p50/p95/p99, throughput, errors, observed SQLite busy errors,
heap/system memory and goroutines.

## Minimum honest MVP capacity

The single-node MVP claim is deliberately limited to the default bounded
three-phase profile on hardware no weaker than the recorded baseline:

- 100 public reads/s with p95 at or below 100 ms;
- 10 creates/s with p95 at or below 500 ms;
- 50 live SSE clients at 2 events/s, with no missing deliveries and p95 fanout at
  or below 1 second;
- zero request errors and zero observed SQLite busy responses;
- at most 64 MiB additional Go runtime system memory and no more than 10
  goroutines remaining above the pre-profile baseline.

This is a bounded acceptance profile, not a maximum-capacity claim. TLS, Caddy,
internet latency, media uploads and multi-host operation are outside it. Repeat
the command on production-class staging hardware before release; failure blocks
release rather than weakening thresholds.

## Alerts and escalation

Alert on any `bord_sqlite_busy_total` increase over 5 minutes; page if it persists
for 15 minutes or accompanies HTTP 5xx. Warn when route p95 exceeds 100 ms for
public reads, 500 ms for creates, or SSE symptoms exceed 1 second for 10 minutes;
page at twice those values for 5 minutes. Warn at 70% and page at 85% of the
profiled memory ceiling or when goroutines rise continuously for 15 minutes.
Sustained traffic above this profile requires a new capacity decision; recurring
busy errors or write p95 above 500 ms trigger the documented PostgreSQL migration
evaluation rather than unmeasured SQLite tuning.
