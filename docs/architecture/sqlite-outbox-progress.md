# SQLite integration ledger

Branch: `feat/sqlite-outbox-refactor`. **Incomplete integration; not ready for release.**

## Implemented foundation

- Corrected executable transactor spike and split pure-Go/race CI jobs.
- `internal/storage`: one database at `<dataDir>/swarm-deploy.sqlite`, private file,
  WAL, per-connection foreign keys/busy timeout, immediate write transactions,
  context DB getter and library savepoints, explicit close, atomic initial migration
  with checksum validation. The application does not open this DB yet.
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

## Audited dependencies and next integration boundary

| Existing API/consumer | Required coordinated replacement |
| --- | --- |
| `dispatcher.Dispatch(ctx,event)` returning nothing | `Publish(ctx,event) error`; update every producer to propagate errors |
| `QueueDispatcher`, `Slow()`, temporal global dedup | One Outbox worker with stable subscriptions; delete old queues/dedup |
| Event History JSON + in-memory `Query` | SQLite projection with source-event uniqueness, SQL cursor/filter query |
| Alert `Subscriber` + FileStore | SQL repository and idempotent lifecycle in worker T3; include node signals |
| Notification Subscriber | Independent stable destination IDs and external adapters; remove failure-event retry loop |
| Recommendation Subscriber | Currently reads full `DeployEvent.StackDefinition`; requires safe snapshot/recommendation input |
| Service metadata Subscriber | Currently reads full Compose, then Docker/images/configs; use safe deployment reference and external adapter |
| Assistant RAG Subscriber | External embedding call; currently races service metadata because both subscribe to deploySuccess; needs an explicit follow-up event |
| GitOps Store `Update` without error | Context-based SQL reads/writes and error propagation; atomic success transaction with deployment/snapshot/publication |
| Stack pipeline | Preparation before running row, apply policies/init/deploy/prune, successful T1 before maintenance cleanup |
| `cmd/swarm-deploy/main.go` | Open one DB, import all stores before modules, register all subscriptions, start worker, stop producers/workers before close |

Do not connect the safe codec to the existing Compose-dependent consumers unchanged:
that would silently erase recommendation inputs and service metadata. Do not start
Outbox alongside the old queue. The next implementation step is the SQL consumer
repositories and the snapshot/domain-event contract needed for coordinated wiring.

## Still required

1. Production module wiring and removal of the old dispatcher; producer error handling.
2. SQLite Event History, Alerts and other projection repositories; notification destinations.
3. Deployment/DesiredSnapshot model, semantic diff using envmasker, apply boundaries,
   crash recovery to interrupted, atomic success/runtime/snapshot/event transaction.
4. SQLite GitOps, Recommendations, nodes/services/secrets, assistant chats/turns.
5. Preflight of **all** legacy files, atomic one-time import with completion marker,
   preserved IDs/timestamps/order and unchanged legacy backups. No historical deployments.
6. API/Overview/Events & Alerts UI integration and config/example documentation.
7. Full regression and Docker verification of the integrated application; extend and
   consolidate all domain tables into the same `0001_initial` migration.

The migration checksum intentionally refuses a changed schema on an existing DB.
During branch development use temporary test databases; do not create a production DB
until the final initial schema and importer are complete. Never delete a user's DB to
bypass a checksum mismatch.

## Local verification of this checkpoint

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
