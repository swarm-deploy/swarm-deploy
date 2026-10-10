# SQLite integration ledger

Branch: `feat/sqlite-outbox-refactor`. Runtime integration is implemented.
Local verification is recorded below; this is not a production rollout or remote CI result.

## Integration commits

- `5bdac49`: SQLite repositories and atomic legacy import.
- `1dc9480`: runtime repository wiring and propagated persistence errors.
- `e61a190`: Deployment attempts, safe desired snapshots and consolidated schema.
- `28e6837`: single Outbox wiring, producer transactions and independent projections.
- `31cb336`: Deployments API, Overview and separate Events/Alerts tables on one page.
- `50e1299`: PR 123 deployment safety, recovery, lifecycle signals and history ordering review fixes.

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
- History deliberately excludes deployment-failure lifecycle facts and new
  nodeDisconnected signals. Only `deploySuccess` remains a public deployment event;
  `deployFailed` is a notification compatibility alias. Retention and `ReadRecent` use the
  original Outbox publication sequence, including a reserved legacy-import prefix,
  so delayed retries cannot replace newer history.
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
  Failure never advances baseline. Every attempt retains its desired snapshot and comparison
  basis. Recovery uses the latest uncertain attempt as its diff basis and reports unknown
  actual state instead of an empty successful-baseline diff. Reconciliation retries known completion outcomes
  after transient persistence errors; unknown running outcomes atomically update
  runtime, mark the attempt interrupted and publish a durable interruption fact.
  Failed/interrupted attempts after a successful baseline force a new reconciliation
  attempt even when desired state returns to that baseline.
  Apply, verification and pruning outcomes are recorded independently before success is committed.
- Compose comparison excludes source formatting, parser env-key ordering and
  checkout path, while tracking referenced resource content. Commands, entrypoints,
  healthchecks and init-job scripts persist only masks plus existing unkeyed
  fingerprints. Scalar/list command form remains part of effective identity.
- Preparation failures update runtime and publish an internal lifecycle fact in one
  transaction without creating a Deployment. Alerts correlate repeated preparation,
  apply and interruption failures; success resolves the incident. Existing
  `deployFailed` notification configuration covers the full failure lifecycle.
- Deployments API and Overview read deployments, not Event History. The list endpoint returns
  cursor-paged summaries; detail returns structured secret-safe changes. The deployments
  page shows summary counts, comparison confidence and all four statuses. Events & Alerts is one page
  with two separate tables; existing /alerts links redirect to its alerts section.
- The container includes `sd outbox-list`, `outbox-replay` and `outbox-discard`.
  Inspection returns safe operational metadata, not event payloads.
- Branch CI now runs the full pure-Go application suite plus integration race tests;
  the existing transactor spike remains separately covered.

## Verification

Verified locally for the PR 123 review fixes on 2026-10-10:

- `GOCACHE=/private/tmp/go-build CGO_ENABLED=0 go test ./...`: passed.
- `GOCACHE=/private/tmp/go-build CGO_ENABLED=1 go test -race ./internal/storage
  ./internal/legacyimport ./internal/modules/event/... ./internal/modules/gitops/...
  ./internal/modules/alertmanagement/... ./internal/modules/notifications/...`: passed.
- `GOCACHE=/private/tmp/go-build GOLANGCI_LINT_CACHE=/private/tmp/golangci-lint make lint`:
  passed, 0 issues.
- UI `npm test -- --run`: 6 files / 28 tests passed; `npm run build`: passed.
- `docker build -t swarm-deploy:pr123-review-fixes .`: passed; both Linux amd64
  binaries were built with `CGO_ENABLED=0`. Image:
  `sha256:e28bb87c79b541f15385072a58eef6c0a0877d8d29c93c16cc7505a98e9747fb`.
- `git diff --check`: passed before the code commit and is repeated before push.
- API and mock interfaces were unchanged, so `make gen` was not run for these fixes.

Remote GitHub checks are recorded only after the final commits are pushed.

Tests cover publisher rollback and commit visibility, T3 projection/child/ack
rollback, independent destinations, replay/discard, final-ack deletion, no-subscriber
publication, non-deduplicated facts, lease recovery/fencing, importer atomicity and
untouched backup bytes. Review regressions cover opaque executable-data redaction and
API output, command scalar/list semantics, rollback after failed/interrupted attempts,
same-process completion recovery, atomic preparation/interruption signals and alert
lifecycle, and publication-ordered history retention with delayed retries and legacy
import. Reconciler tests assert that running exists before Docker and Docker does not
receive a transaction context.

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
