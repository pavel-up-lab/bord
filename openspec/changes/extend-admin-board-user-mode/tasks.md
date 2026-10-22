# Tasks

## 1. Board lifecycle persistence and public isolation

- [x] 1.1 Add repository queries/contracts for paginated all-state board listing, safe aggregate counts, unique board creation and compare-state archive/restore; verify SQLite repository tests cover active, archived, duplicate and conflict cases.
- [x] 1.2 Add efficient parent-board active checks for thread, post and attachment paths; verify repository query tests prove direct IDs cannot bypass an archived board.
- [x] 1.3 Enforce archived-as-not-found across public catalog, posts, attachments and delete reads, and reject new threads/replies; verify board service and public API tests cover every read/write path plus restore.
- [x] 1.4 Verify schema compatibility without a migration by running migration tests from versions 0–3 and documenting the `enabled` lifecycle invariant in storage code comments.

## 2. Management board API

- [x] 2.1 Extend `api/openapi.yaml` with safe board DTOs and list/create/archive/restore operations, regenerate clients, and verify OpenAPI contract tests pass.
- [x] 2.2 Implement administrator-only integration operations with validation, transactional state changes and safe success/failure audit records; verify unit tests cover roles, reasons, duplicate slug and repeated-state conflicts.
- [x] 2.3 Register board handlers only on the management router and map validation/conflict/not-found responses consistently; verify router tests prove public `404` and moderator/reports `403`.
- [x] 2.4 Extend the management E2E workflow through create → public visibility → archive → public read/write denial → management visibility → restore, and verify audit actor/action/target/reason/outcome/request-ID fields.
- [x] 2.5 Add typed `internal/adminclient` board methods and safe error handling; verify client tests cover pagination, state filters, requests without credential leakage in errors and all mutation payloads.

## 3. Shared рамки и theme picker

- [x] 3.1 Capture current ordinary Bord wide/narrow panel, theme picker, keyboard and mouse behavior in regression/golden tests before extraction.
- [x] 3.2 Extract display-row, panel rendering, split geometry and clipping into `internal/terminalui`; verify shared tests cover active/inactive borders, custom MC line sets, tiny terminals, Unicode width and trusted ANSI rows.
- [x] 3.3 Convert `internal/tui` to the shared panel API without visible behavior changes; verify its golden, mouse and terminal-width tests remain unchanged and pass.
- [x] 3.4 Extract the stateful framed theme picker into `internal/terminalui`; verify tests cover current selection, viewport scrolling, arrows/j/k, PgUp/PgDn, Enter, Esc/F2/t, wheel, click selection, outside click and sanitized labels.
- [x] 3.5 Replace `admintui` local panel and theme overlay implementations with shared components; verify admin wide/narrow golden tests match ordinary Bord borders, hints, corners, selection colors and modal geometry.

## 4. Admin board screen

- [x] 4.1 Add the administrator-only «Доски» screen, state filter, pagination, keyboard navigation and mouse hit-testing; verify model/view tests cover active and archived rows in wide and narrow terminals.
- [x] 4.2 Add create-board modal fields for slug, title and reason with server-aligned validation and safe error display; verify keyboard and mouse tests cover submit, retry and cancel.
- [x] 4.3 Add archive/restore reason modals with explicit second confirmation and pending-state input lock; verify tests prove no mutation occurs before confirmation and conflicts preserve selection/state.
- [x] 4.4 Update admin help, command bar and `docs/admin-tui.md` to describe board lifecycle and role limits; verify every documented shortcut is represented by a model test.

## 5. Integrated user mode and transport boundaries

- [x] 5.1 Extract the ordinary client bootstrap for public API, posting, SSE, cache, opener and user state from `cmd/bord` into a reusable package; verify existing `cmd/bord` command/startup tests still pass.
- [x] 5.2 Add `--server`/`BORD_SERVER` to `bord-admin` as a public-only setting while retaining independent `--management`; verify precedence and invalid-URL command tests.
- [x] 5.3 Implement the root Bubble Tea app model that owns persistent admin/user child models, routes tagged async messages, resizes children and closes both contexts; verify switching preserves selection, pagination and drafts.
- [x] 5.4 Add the keyboard/mouse upper mode menu with narrow fallback and focus-safe hit-testing; verify clicks never leak to underlying content and switching remains available at minimum supported dimensions.
- [x] 5.5 Wire the unchanged full `internal/tui.Model` as user mode with posting, replies, own-post deletion, attachments, SSE and image viewer; verify an app-level test exercises browse and publish flows before/after mode switches.
- [x] 5.6 Share theme selection between both child models and persist active mode/theme only in admin state; verify restart tests preserve presentation while user session/delete secrets remain only in the ordinary user store.
- [x] 5.7 Add recording-transport security tests for all public HTTP/SSE paths and serialized-state scans; verify management Authorization/credential never reaches public requests, logs, errors or JSON state.
- [x] 5.8 Update README and `docs/admin-tui.md` with public URL configuration, mode switching, state separation and theme picker controls; verify documented launch examples pass command parsing tests.

## 6. Cross-surface verification

- [x] 6.1 Extend `scripts/terminal-smoke.sh` for admin boards, mode switching, shared theme picker and Linux/macOS/Windows target builds; verify the script completes successfully.
- [x] 6.2 Run `make quality`, OpenAPI drift checks and `openspec validate extend-admin-board-user-mode --strict`; record/fix every failure before marking complete.
- [x] 6.3 Build both binaries reproducibly and run a local two-listener smoke proving management routes are private, public user mode works and archived content remains preserved but inaccessible publicly.

## Workflow follow-up

- Review and commit the completed implementation without touching the user-owned `temp/` directory.
- Deploy backend before the updated `bord-admin`, create a verified pre-upgrade backup and execute the production lifecycle smoke from `design.md`.
- Archive the OpenSpec change only after production verification succeeds.
