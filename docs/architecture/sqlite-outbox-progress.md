# SQLite integration ledger

Branch: `feat/sqlite-outbox-refactor`. **Incomplete integration; not ready for release.**

## Implemented foundation

- Corrected executable transactor spike and split pure-Go/race CI jobs.
- `internal/storage`: one database at `<dataDir>/swarm-deploy.sqlite`, private file,
  WAL, per-connection foreign keys/busy timeout, immediate write transactions,
  context DB getter and library savepoints, explicit close, atomic initial migration
  with checksum validation. Application startup opens this DB before initializing modules.
- `internal/modules/event/codec`: explicit versioned payloads for current event types.
  No arbitrary Event/Details serialization, raw Compose/environment, error/log or
  assistant prompt persistence. Deployment payload currently carries stack, revision
  and service names/images only. This format must be extended deliberately when
  introducing domain Deployment events and safe snapshot references.
- `internal/modules/event/outbox`: publication in the caller's transaction (or its
  own short transaction), stable registry, atomic claim, token/deadline fencing,
  independent retry/terminal failure/replay/discard, DB projection+child event+ack
  in one T3, external handler outside write transaction, immediate final-ack deletion,
  cancellation/recovery and Prometheus queue-state collector.
- Infrastructure tests use real SQLite and generated `go.uber.org/mock` subscribers.

## SQLite repository integration

- Runtime wiring now uses SQLite for GitOps state, Event History, Alerts,
  Recommendations, nodes, service metadata, secret metadata and assistant chats/turns.
- Repository reads carry context and return errors to API/tool callers. GitOps updates
  persist synchronously; a failed stack-state write prevents a success event. Assistant
  reads/writes fail the request instead of claiming a saved chat.
- History uses SQL filters/cursors with publication-sequence compatibility for older
  callers. Its safe projection skips new deployFailed/nodeDisconnected signals and
  enforces source uniqueness. Alert lifecycle changes and consumed-source markers
  commit together, including duplicate delivery after resolution.
- `internal/legacyimport` validates all sources before import, imports every repository
  and a completion marker atomically, and refuses an unmarked nonempty database.
  IDs, timestamps, sequence, alert lifecycle and chat usage/turns are preserved.
  Missing history IDs are assigned inside the transaction. Original files are untouched.
- The old asynchronous GitOps WarmupStore is removed. Legacy file implementations remain
  only for fixtures/backward-compatibility tests and must be retired before release.
- This checkpoint still uses QueueDispatcher in production wiring. DB state changes
  and event publication are **not yet atomic**. Outbox is not started alongside it.

## Audited dependencies and next integration boundary

| Existing API/consumer | Required coordinated replacement |
| --- | --- |
| `dispatcher.Dispatch(ctx,event)` returning nothing | `Publish(ctx,event) error`; update every producer to propagate errors |
| `QueueDispatcher`, `Slow()`, temporal global dedup | One Outbox worker with stable subscriptions; delete old queues/dedup |
| Event History SQL projection | Connect to Outbox T3; repository and SQL queries are implemented |
| Alert SQLSubscriber | Connect idempotent lifecycle to worker T3; include node signals |
| Notification Subscriber | Independent stable destination IDs and external adapters; remove failure-event retry loop |
| Recommendation Subscriber | Currently reads full `DeployEvent.StackDefinition`; requires safe snapshot/recommendation input |
| Service metadata Subscriber | Currently reads full Compose, then Docker/images/configs; use safe deployment reference and external adapter |
| Assistant RAG Subscriber | External embedding call; currently races service metadata because both subscribe to deploySuccess; needs an explicit follow-up event |
| GitOps context-based SQL Store | Add atomic success transaction with deployment/snapshot/publication |
| Stack pipeline | Preparation before running row, apply policies/init/deploy/prune, successful T1 before maintenance cleanup |
| `cmd/swarm-deploy/main.go` | DB/import are connected; replace queue registration/startup/shutdown with Outbox worker |

Do not connect the safe codec to the existing Compose-dependent consumers unchanged:
that would silently erase recommendation inputs and service metadata. Do not start
Outbox alongside the old queue. The next implementation step is the snapshot/domain-event contract needed for
coordinated Outbox and Deployment wiring.

## Still required

1. Production module wiring and removal of the old dispatcher; producer error handling.
2. Outbox wiring for SQL projections and independent notification destinations; node alerts.
3. Deployment/DesiredSnapshot model, semantic diff using envmasker, apply boundaries,
   crash recovery to interrupted, atomic success/runtime/snapshot/event transaction.
4. Retire legacy file-write APIs after fixture conversion; extend error-path and repository tests.
5. Extend the importer empty-database guard when Deployment/Snapshot tables are added.
   Do not import historical Deployments from Events.
6. API/Overview/Events & Alerts UI integration and config/example documentation.
7. Full regression and Docker verification of the integrated application; extend and
   consolidate all domain tables into the same `0001_initial` migration.

The migration checksum intentionally refuses a changed schema on an existing DB.
During branch development use temporary test databases; do not create a production DB
until the final initial schema and importer are complete. Never delete a user's DB to
bypass a checksum mismatch.

## Repository integration verification

- `CGO_ENABLED=0 go test ./...`: passed after repository and startup integration.
- Race tests passed for legacyimport, storage, event/history, gitops/modelstore,
  assistant (including SQLite chats), resources and alertmanagement.
- Import tests cover all legacy stores, restart, exact preservation of backup bytes,
  source validation, refusal to overwrite unmarked nonempty data and SQL failure
  after earlier writes. The latter rolls back every row and the completion marker.
- History SQL pagination matches legacy results for all four sort orders, filters
  and tied timestamps. Projection rollback, source deduplication, selection and
  retention are tested. Alert tests include replay after resolution.
- Runtime and chat tests cover rollback, restart, concurrent updates and injected
  SQL failures. Chat metadata, usage and ordered turns remain atomic.
- Linux amd64 `CGO_ENABLED=0 go build` passed; `file` confirmed a static ELF.
- `docker build -t swarm-deploy:sqlite-stores-check .` passed, including the UI build
  and pure-Go backend. `docker run --rm --network none swarm-deploy:sqlite-stores-check -h`
  passed. This is a build/smoke check, not a live Swarm deployment test.
- Lint across affected application packages reports only the two pre-existing
  unused `nolint:gosec` directives in notification custom/Telegram implementations.
- No remote CI run, push or merge to master. See
  [storage/migration notes](../../example/README-sqlite.md) before running this branch.

## Foundation verification (previous checkpoint)

- `experiments/sqlite-transactor`: `CGO_ENABLED=0 go test -v ./...` and
  `CGO_ENABLED=1 go test -race -v ./...` passed after correcting the getter test.
- Repository: `CGO_ENABLED=0 go test ./...` passed, including the final foundation changes.
- `CGO_ENABLED=1 go test -race -mod=mod ./internal/storage ./internal/modules/event/codec ./internal/modules/event/outbox` passed.
- Targeted golangci-lint for these three packages: **0 issues**.
- Full `make lint`: failed on two existing unused `nolint:gosec` directives in
  `internal/modules/notifications/notifiers/custom.go:83` and `telegram.go:171`.
  These notifier files were not changed by this checkpoint.
- `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=mod -o /private/tmp/swarm-deploy-sqlite-refactor ./cmd/swarm-deploy`
  passed; `file` confirmed a statically linked Linux ELF. This builds the current
  entrypoint; Outbox is covered separately by its pure-Go tests until wiring is implemented.
- Docker build attempted twice; both attempts failed before compiling project code
  because `https://auth.docker.io/token` returned **504 Gateway Timeout** while
  resolving the Dockerfile frontend. Container build is **not verified**.
- GitHub CI jobs were updated but have not been run remotely. No push or merge to master.
