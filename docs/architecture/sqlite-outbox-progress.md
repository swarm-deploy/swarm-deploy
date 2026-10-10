# SQLite integration ledger

Branch: `feat/sqlite-outbox-refactor`. Runtime integration is implemented.

## Implemented

- One `<dataDir>/swarm-deploy.sqlite` database using the pure-Go modernc driver,
  WAL, foreign keys, busy timeout, FULL synchronization, private file permissions,
  a context-aware Thiht transactor and explicit shutdown.
- One consolidated `migrations/sqlite/0001_initial.sql`, applied by Goose, contains
  the domain, projection and two-table Outbox schema.
- SQLite is the only supported persistent application store. The former JSON
  repositories and startup importer were removed. Existing JSON files are ignored
  and left untouched as operator-owned archives.
- The cutover is intentionally breaking: Events, Alerts, Recommendations, Assistant
  chats, resource snapshots and controller state from JSON are not migrated.
  No Deployment or successful desired baseline is fabricated at startup.
- One durable Event Bus publishes business state and Outbox envelopes atomically.
  Subscriber processing happens later in separate transactions; external I/O remains
  at-least-once.
- Outbox deliveries have independent retries, lease fencing, restart recovery,
  replay/discard operations and immediate cleanup after the final acknowledgement.
- Typed codecs persist safe event envelopes rather than arbitrary Compose, raw
  errors, unmasked environment values or assistant prompts.
- Event History remains a separate projection. Deployment failures and node
  disconnections feed Alerts instead of creating new user Events; `deploySuccess`
  remains public.
- Deployment attempts record running/succeeded/failed/interrupted state, safe desired
  snapshots, comparison basis and structured semantic differences. Only a successful
  apply advances the desired baseline.
- The first real Deployment in a fresh database uses `comparison_basis=none`.
  Overview and Deployments label it **Initial snapshot**. Deployment Details shows
  **Initial Deployment Snapshot** with the safe target configuration, without
  presenting every value as newly added or claiming live Swarm convergence.
- Later Deployments retain the normal `added` / `changed` / `removed`
  before-to-after diff.
- The Deployments API reads first-class Deployment records, not Event History.
  List responses stay lightweight; detail data is loaded by ID.
- The `sd` CLI exposes `outbox-list`, `outbox-replay` and `outbox-discard`
  without exposing payloads.

## Verification expectations

CI must run Go tests, generated API verification, frontend tests/build and lint.
A clean data directory must create the complete SQLite schema. Restarting must
preserve SQLite state. Invalid legacy JSON next to the database must remain unread
and unchanged, create no records and never block startup.

Docker image construction and manual desktop/mobile UI review are recorded only when
actually performed. The current verified commit and workflow run belong in the PR
description rather than this long-lived architecture ledger.

## Operational boundaries and remaining risks

- One active controller and a local persistent volume are required; WAL is not a
  shared multi-host database.
- Back up the complete data directory before rollout. There is no automatic or manual
  JSON-to-SQLite conversion in this release.
- Do not delete a user's SQLite database or alter Goose migration history to bypass a
  startup failure.
- Docker effects and SQLite cannot commit atomically. A crash can leave an
  interrupted Deployment or repeat external notification delivery.
- A successful Deployment means the apply pipeline completed, not that service health
  or live convergence was verified.
- A backed-up staging rollout remains an operator release check.

See [architecture](sqlite-outbox-refactor.md),
[Outbox contracts](event-bus-outbox-schema.md) and
[storage cutover instructions](../../example/README-sqlite.md).
