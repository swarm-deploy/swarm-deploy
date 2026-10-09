# SQLite integration ledger

Branch: `feat/sqlite-outbox-refactor`. Runtime integration is implemented.
Local verification is recorded below; this is not a production rollout or remote CI result.

## Integration commits

- `5bdac49`: SQLite repositories and atomic legacy import.
- `1dc9480`: runtime repository wiring and propagated persistence errors.
- `e61a190`: Deployment attempts, safe desired snapshots and consolidated schema.
- `28e6837`: single Outbox wiring, producer transactions and independent projections.
- `31cb336`: Deployments API, Overview and separate Events/Alerts tables on one page.

## Implemented

- One `<dataDir>/swarm-deploy.sqlite`: pure-Go modernc driver, WAL, per-connection
  foreign keys and busy timeout, FULL synchronization, private database file,
  context DB getter and Thiht/transactor savepoints, explicit shutdown.
- One consolidated `internal/storage/migrations/0001_initial.sql` contains all
  domain and Outbox tables, indexes, uniqueness and foreign-key constraints.
- All runtime durable repositories use SQLite: GitOps, Event History, Alerts,
  Recommendations, nodes, service catalog, secret metadata and assistant chats.
  Repository read/write errors reach callers. JSON implementations retained for
  legacy fixtures/compatibility tests are not constructed by application wiring.
- `internal/legacyimport` preflights all legacy sources, then imports them and
  the completion marker in one transaction. IDs, timestamps, history order,
  alert lifecycle and chat usage/turns are preserved. Originals remain untouched.
  Invalid input or an unmarked nonempty database fails startup; no historical
  Deployment or successful snapshot is fabricated.
- One durable Event Bus replaces QueueDispatcher, its queues, Slow API and global
  time-window deduplication. All producers use `Publish(ctx,event) error`.
  State changes and publication share T1; claim is short T2; DB projections,
  child publication and acknowledgement share separate T3. External I/O runs
  outside write transactions.
- Outbox has independent destination retries, lease/token fencing, restart recovery,
  retained terminal failures, replay/discard and immediate deletion after the last
  acknowledgement. No retention, synchronous delivery or commit hooks.
- Typed versioned codecs persist safe envelopes, not arbitrary Event/Details,
  Compose, environment, raw errors/logs or assistant prompts. Deployment events
  contain references to redacted desired state.
- History deliberately excludes new deployFailed/nodeDisconnected signals.
  Alerts handle deployment and node failures/recovery. Source uniqueness and
  per-resource ordering prevent retries from reopening newer resolved state.
- Telegram and custom destinations have independent stable subscriptions.
  Notification errors cannot recursively generate notification failures.
  Existing notifier Notify/Message and template configuration remain.
- Service metadata loads safe desired state by deployment ID, performs external
  inspection, then atomically stores catalog + source receipt + child
  `serviceCatalogUpdated`. RAG consumes that child after the catalog commit.
  Recommendations use safe desired state and reject obsolete source events.
- Node snapshots and lifecycle publications commit together. Persisted node
  identities distinguish genuine nodeJoined from reconnects, including reappearance
  after a missing snapshot. Refresh reconciles connection transitions without
  synthesizing joins.
- `internal/modules/gitops/deployment` owns running/succeeded/failed/interrupted
  attempts, redacted effective snapshots and field-level semantic differences.
  Preparation precedes attempt creation. Running is committed before Docker;
  successful attempt + baseline + GitOps state + source event commit atomically.
  Failure never advances baseline; startup interrupts unfinished attempts.
  Post-apply cleanup cannot change an already successful outcome.
- Compose comparison excludes source formatting, parser env-key ordering and
  checkout path, while tracking referenced resource content. envmasker and
  fingerprints hide sensitive changes without a new hashing key.
- Deployments API and Overview read deployments, not Event History. The deployments
  page shows planned changes and all four statuses. Events & Alerts is one page
  with two separate tables; existing /alerts links redirect to its alerts section.
- The container includes `sd outbox-list`, `outbox-replay` and `outbox-discard`.
  Inspection returns safe operational metadata, not event payloads.
- Branch CI now runs the full pure-Go application suite plus integration race tests;
  the existing transactor spike remains separately covered.

## Verification

Verified locally on the integrated tree:

- `CGO_ENABLED=0 go test ./...`: passed.
- `CGO_ENABLED=1 go test -race ./internal/storage ./internal/legacyimport ./internal/modules/event/... ./internal/modules/gitops/... ./internal/modules/resources/... ./internal/modules/alertmanagement/... ./internal/modules/recommendations/... ./internal/modules/assistant/...`: passed.
- `make lint`: passed, 0 issues.
- `PATH="/Users/avukrainskiy/go/bin:$PATH" GOTOOLCHAIN=go1.26.3 make gen`:
  passed; ogen and go.uber.org/mock output regenerated.
- UI `npm test`: 6 files / 28 tests passed; `npm run build`: passed.
- `docker build -t swarm-deploy:sqlite-outbox-integrated .`: passed, both
  Linux amd64 binaries built with `CGO_ENABLED=0`.
  Image: `sha256:e863ce21434fd6eafac3035257a6de08ba7c92fdb8b0431b6196907e54ee5134`.
- Container smoke checks with `--network none`: controller `-h` and
  `--entrypoint sd ... --help` passed; all three Outbox commands are listed.
- `git diff --check`: passed.
- Earlier executable transactor spike passed both pure-Go and race suites.

These are local checks. Updated GitHub workflows have not been run remotely.

Tests cover publisher rollback and commit visibility, T3 projection/child/ack
rollback, independent destinations, replay/discard, final-ack deletion, no-subscriber
publication, non-deduplicated facts, lease recovery/fencing, importer atomicity and
untouched backup bytes, redaction, deployment crash recovery and projection failure
after successful apply. Reconciler tests assert that running exists before Docker
and Docker does not receive a transaction context.

## Operational boundaries and remaining risks

- One active controller and a local persistent volume are required; WAL is not a
  shared multi-host database. Follow the backup/import procedure before rollout.
- The initial-schema checksum deliberately rejects databases created with an
  earlier experimental schema from this branch. Do not delete a user's database
  or silently bypass the mismatch. Legacy production JSON import is the supported
  upgrade path; experimental DB conversion needs a separately reviewed plan.
- External effects are at-least-once. A crash after sending/applying and before
  acknowledgement can repeat a notification or leave an interrupted deployment.
  Docker and SQLite cannot be committed atomically.
- Successful deployment means apply completed, not verified Swarm convergence.
  Projection lag is expected; failed deliveries need operator attention.
- No live Swarm rollout, remote CI execution, push or merge to master was performed.
  A backed-up staging rollout remains an operator release check, not a claim made
  by local unit/build verification.

See [architecture](sqlite-outbox-refactor.md),
[Outbox contracts](event-bus-outbox-schema.md) and
[migration/operations instructions](../../example/README-sqlite.md).
