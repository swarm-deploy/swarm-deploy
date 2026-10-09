# SQLite, transactional outbox, and deployment model refactor

Status: **integration-branch design baseline**  
Branch: `feat/sqlite-outbox-refactor`  
Target: one coordinated merge to `master`, after schema consolidation and full regression testing.

## Scope and delivery model

This is one platform refactor, not a chain of small, partially compatible releases on `master`.

1. Replace **all durable JSON-file repositories** with repositories backed by a single per-instance SQLite database under `dataDir`.
2. Add a first-class `Deployment` model and successful `DesiredSnapshot` baseline. `Latest deployments` reads deployments, not event history.
3. Separate public event history, internal runtime signals, alerts, and notification deliveries.
4. Make the transitions that generate durable events and notification requests **transactional** using a DB-backed outbox.
5. Consolidate all schema changes within this branch into **one initial schema migration** before merging into `master`.
6. Preserve the data deployed on existing clusters with a **one-time, restart-safe JSON import**.

Do not convert ordinary input/artifact files (Git checkout, compose source files, rendered compose required by the current deployer, config files, secrets mounted as files) into DB entities merely because they are files. Only replace application-owned *persistent stores*.

## Existing file-backed stores to migrate

| Module | Current storage | Database responsibility |
|---|---|---|
| GitOps | `controller.state.json` | current controller/reconciliation runtime state |
| Event history | `event-history.json` | historical user-visible events, indexed pagination, retention |
| Alert management | `alerts.state.json` | alert lifecycle and open-fingerprint uniqueness |
| Recommendations | `recommendations.state.json` | recommendations by stack/ID |
| Resources / nodes | `nodes.json` | last observed node snapshot |
| Resources / services | `services.json` | persisted service metadata catalog |
| Resources / secrets | `secrets.state.json` | secret **metadata**, never secret contents |
| Assistant history | `assistant/chats/index.json` and per-chat JSON files | persistent chats, turns and token usage |

The assistant's TTL-bound in-memory context cache is **not a file store**; it may remain in memory. Also audit any additional persistence added to `master` while the integration branch is in progress.

## Architectural boundaries

- Introduce a shared DB lifecycle package, e.g. `internal/storage`: connection, pragmas, schema migrations, transaction handling, import orchestration, shutdown.
- Retain repositories near their owning module; replace `FileStore` with SQLite implementations behind module-defined interfaces. The shared storage package must not know GitOps/Alerts/Assistant business logic.
- Prefer `database/sql` and a SQL query builder over an ORM; isolate SQLite-only connection setup so PostgreSQL can be considered later. A query builder alone is **not** a promise of zero-work Postgres portability.
- The current Docker build sets `CGO_ENABLED=0`; select a SQLite driver compatible with that build or explicitly revise and test the build matrix.
- Use a single database at `<dataDir>/swarm-deploy.sqlite`. Configure WAL, foreign-key enforcement, busy timeout, and appropriate checkpointing/backup behavior. Document that SQLite database files live on a persistent **local** volume and the app uses one active writer/controller per database. SQLite WAL is not a shared multi-host database.
- Design schemas from query patterns. Index event list filters/cursors, deployment history by stack/time, unresolved alerts by fingerprint, and pending notifications by due time.
- Preserve public API response contracts where possible, but remove obsolete *internal* JSON-file repository APIs. Fix silent write failures: for example, `gitops/modelstore.FileStore.Update` currently logs persistence failures without returning an error.

## Deployment lifecycle

- Deployment records a **real attempt to apply a changed effective desired state**, not every reconcile. Distinct retries create distinct deployment IDs. Reconciler owns whether a retry happens; a store must not suppress attempts with the same revision/digest.
- Preparation failures prior to an apply attempt do not create a Deployment. Once application has begun, policy rejection is a failed Deployment.
- States: `running`, `succeeded`, `failed`, `interrupted`.
- Apply boundary includes policies, init jobs, stack deployment, and authorized prune. Maintenance work after successful apply does not retroactively change a completed Deployment.
- Successful application means the deploy pipeline completed, **not** that the services are healthy or that actual live convergence was verified. Rollout tracking is explicitly deferred.
- Preserve user-visible `deploySuccess`. Create it at successful completion of a Deployment.
- Only a successful Deployment advances the per-stack `DesiredSnapshot` baseline. A failed or interrupted attempt must never replace a known-good baseline.
- Snapshot may contain an effective rendered compose representation suitable for semantic diffs; sensitive values must be redacted or represented safely. Never persist unmasked secret contents in snapshots, diffs, events, outbox payloads, or logs. Avoid introducing a new secret hashing key. Improvements to `envmasker` are a separate track.
- Persist meaningful structured changes (`added`, `removed`, `changed` plus semantic details), rather than presenting only flat change counts. A failed attempt can show **planned** changes without claiming they were applied.

## Transactional outbox boundaries

External Docker operations and the database **cannot share a transaction**. The DB can guarantee only that *local state transitions* and their emitted durable records are committed together.

- Before an external apply, persist a `running` Deployment.
- On completed apply, use **one DB transaction** to commit all of: `Deployment=succeeded`, successful `DesiredSnapshot`, the `deploySuccess` public event, and the outbox record(s) required for downstream notification processing.
- Failed/interrupted transitions and their durable side effects must use the same principle.
- After a crash between Docker API success and the success transaction, the Deployment remains `running` until it is detected as `interrupted`; do not invent success or overwrite the baseline. Actual-state convergence detection is future work.
- An outbox worker reads due records, attempts delivery, and marks completion/retry in the database. Define bounded retries/backoff, terminal handling, metrics, and startup recovery.
- Model one notification intent and a **separate delivery state per destination** so partial success across Telegram/webhooks is not treated as all-or-nothing.
- Prevent duplicate scheduling via stable source/event + destination keys, and preserve deduplication semantics without suppressing legitimate new deployment attempts.
- Network notifications provide **at-least-once**, not exactly-once delivery: a crash after an external send and before DB acknowledgement may repeat a message. `ReplyTo`/threading is message metadata, not a notifier-specific new API and not where send results belong.
- Keep internal dispatcher/telemetry non-durable where appropriate; do **not** dump all runtime signals into user Event History.

## Events, alerts, and notifications

- Public Events = auditable facts (e.g. `nodeJoined`, `deploySuccess`). `nodeJoined` means a genuinely new swarm node, not an existing node becoming Ready.
- `NodeDisconnected` and `DeployFailed` are internal signals / alert inputs, not automatically user-visible Event History rows.
- Alerts maintain their own lifecycle and deduplication identity. Events and Alerts share a UI page, but **remain separate tables**.
- Notifications are a distinct module/concern. Stable public notification semantics can combine technical failure causes so users do not have to guess which low-level event to subscribe to.
- Existing Event History cursor pagination/filtering must become a database query rather than loading, sorting, and filtering the full history in memory.
- Overview `Latest deployments` reads the new deployment repository.

## Schema and one-time import

Keep a single `0001_initial` schema migration in the integration branch; before merging, fold any intermediate schema edits into it. New installations create only the final schema.

Plan for these conceptual tables (naming/details can change during implementation):

`schema_migrations`, `legacy_imports`, `gitops_runtime`, `nodes`, `services`, `secret_metadata`, `recommendations`, `assistant_chats`, `assistant_turns`, `deployments`, `desired_snapshots`, `event_history`, `alerts`, `outbox`, `notification_deliveries`.

Import existing JSON on startup before starting collectors/reconcilers/subscribers. Required properties:

1. Preflight every file for readability and validity; **do not interpret malformed existing data as an empty store**.
2. Import all legacy sources and record a completion marker in a **single SQLite transaction**. Failure rolls back and leaves JSON untouched for retry.
3. Preserve IDs, timestamps, chat usage, alert state/fingerprints, historical event ordering, and current controller data. If a legacy record genuinely has no ID, generate one once, inside the transaction.
4. Legacy JSON remains an untouched recovery backup. A successful import must not be repeated or interleaved with live mutations after a restart.
5. If any import validation or DB integrity check fails, fail startup safely instead of silently starting with empty state. Provide rollback instructions.
6. Do not reconstruct past Deployments from `deploySuccess` event history: there is not enough reliable structured data. Initialize the deployment model cleanly while preserving historic Events. Avoid inventing a successful DesiredSnapshot baseline from unverifiable historical state.

## Engineering sequence (all commits on the integration branch)

1. Shared DB lifecycle + consolidated schema + executable integration tests for transactions, locking, restart behavior and importer.
2. Migrate existing file-backed repositories and stop wiring JSON stores; extend interfaces where writes currently hide failures.
3. Introduce Deployments, effective-state semantic diffs and successful DesiredSnapshot.
4. Separate public Events, Alerts and internal signals; route durable effects through DB transactions.
5. Introduce outbox processing, per-destination notification states, retries and deduplication/thread metadata.
6. Rewire API/Overview/Event & Alerts UI, adjust docs/config, and remove obsolete file-write code.
7. Verify data import from real-looking fixture sets, crash/restart at every important boundary, migration replay, full CI and Docker build. Consolidate schema to a single initial migration and merge once.

Release gates: no silent data loss; no false deployment success; stable user-visible `deploySuccess`; no plaintext secret leakage through history/outbox; no duplicate logical notification jobs after restart; compatibility with the project's `CGO_ENABLED=0` build.
