# SQLite storage contracts

`Repository` implements the board use-case contracts from `internal/app/board`.
Every mutating use case calls it through `WithinTransaction`; repository methods
select the `*sql.Tx` stored in the supplied context. Callers must not retain that
context or invoke repository writes in a goroutine after the callback returns.
The board package owns minimal structural `Clock` and `Transactor` interfaces
because the architecture gate prohibits child app packages from importing the
parent `internal/app`; foundation implementations satisfy them without adapters
leaking inward.

Public thread and post IDs are zero-padded decimal, monotonically allocated values
with the OpenAPI-required minimum length. Catalog
pages are ordered by `(bumped_at DESC, thread_id DESC)`, and thread pages by
`post_id ASC`. A cursor is exclusive. Deleted posts remain addressable as
tombstones, but their body and attachment reference are cleared in the same
transaction that appends the versioned outbox event.

SQLite is opened in WAL mode with foreign keys, a 5-second busy timeout,
`synchronous=NORMAL`, and one in-process pooled connection. The deliberately
bounded pool serializes writers; WAL permits other processes to continue reading.
Readiness requires connectivity, those safety pragmas, and exactly the schema
version supported by the binary.

## PostgreSQL migration thresholds

Start a PostgreSQL migration change when any threshold is sustained for 15
minutes at normal production load, rather than increasing SQLite timeouts:

- write transaction p95 exceeds 100 ms or p99 exceeds 250 ms;
- SQLite busy/timeout failures exceed 0.5% of writes;
- write demand exceeds 50 transactions/second or the writer queue remains busy
  above 70% of the time;
- the database exceeds 50 GiB or routine backup/restore exceeds the recovery
  objective;
- more than one active server replica or online schema changes are required.

Before migration, preserve the use-case-shaped interfaces, monotonic ordering,
idempotency uniqueness, foreign keys, and content-plus-outbox atomicity. Do not
expose SQL-specific cursors or transaction handles outside this package.
