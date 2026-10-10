# SQLite, transactional outbox, and deployment model refactor

Status: **implemented in the integration branch; local verification recorded in the ledger**
Branch: `feat/sqlite-outbox-refactor`  
Target: one coordinated merge to `master`, after schema consolidation and full regression testing.

## Scope and delivery model

This is one platform refactor, not a chain of small, partially compatible releases on `master`.

1. Replace **all durable JSON-file repositories** with repositories backed by a single per-instance SQLite database under `dataDir`.
2. Add a first-class `Deployment` model and successful `DesiredSnapshot` baseline. `Latest deployments` reads deployments, not event history.
3. Separate public event history, internal runtime signals, alerts, and notification deliveries.
4. Persist events with at least one matching subscriber in the SQLite outbox; all subscribers use the same asynchronous delivery mechanism. Investigate publishers without subscribers as potentially unnecessary. Event History independently selects user-visible events.
5. Consolidate all schema changes within this branch into **one initial schema migration** before merging into `master`.
6. Preserve the data deployed on existing clusters with a **one-time, restart-safe JSON import**.

Do not convert ordinary input/artifact files (Git checkout, compose source files, rendered compose required by the current deployer, config files, secrets mounted as files) into DB entities merely because they are files. Only replace application-owned *persistent stores*.

## Legacy file-backed stores imported at startup

| Module | Legacy storage | Database responsibility |
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
- Preserve public API response contracts where possible, but remove obsolete *internal* JSON-file repository APIs. Propagate persistence errors: GitOps updates now return errors instead of claiming a successful write. Legacy file implementations remain fixture/compatibility helpers, not runtime storage.

## Unified Event Bus and module isolation

**Decision:** one Event Bus, one universal two-table Outbox and one asynchronous subscriber processing model. **Publication and handling always use separate database transactions.** No `Transactional`, `Durable` or `AfterCommit` subscription modes.

- `DeploymentService` does not invoke other modules' services or repositories; it publishes a domain event.
- **T1 (publisher):** the application modifies its own state and inserts an Outbox envelope and one delivery per matching subscriber **atomically**. `Bus.Publish(ctx, event)` only enqueues, never invokes subscriber code.
- **T2 (worker claim):** after T1 commits, a worker claims one delivery using a short transaction and a lease.
- **T3 (handler):** DB-only subscribers (Event History, Alert Management) run in a **new** context-propagated DB transaction, then acknowledge delivery in **that same T3**. Subscriber-side state changes and any newly emitted Outbox events commit together with the ack. External notifications execute outside a DB write transaction, and are subsequently acknowledged in a short transaction.
- Each delivery is independent. Failed handlers retry/park without rolling back the previously committed publishing transaction or another successful subscriber.
- Event History decides which published events deserve user-visible history; it does not run within the Deployment publisher's transaction. Projections are **eventually consistent**. For example, `DeploymentSucceeded` can become a public `deploySuccess` after T1 commits; `DeploymentFailed` may update an Alert without becoming a public Event.
- Nested/follow-up events (currently `serviceCatalogUpdated`) are **published from a subscriber's T3**, and picked up in subsequent transactions. Never recursively run their handlers inline.
- Publications with no matching registered subscribers do not create Outbox records. Consider removing genuinely unused event publishers.
- Outbox events with matching subscribers are deleted immediately once **every** delivery succeeds; no retention, no deleting pending or terminal-failed work.
- Transactions are propagated through `context.Context` using a context-aware transactor and DB getter (`*sql.DB` or `*sql.Tx`). No explicit `Tx` parameters or `UnitOfWork`. `Bus.Publish` outside an active transaction wraps its own short enqueue transaction.
- Subscriber ids must be stable. Durable at-least-once delivery, lease/recovery, per-subscriber idempotence and secret-safe versioned codecs are mandatory.
- There are **no commit hooks** needed for event dispatch: the worker only sees committed Outbox work.

See [Event Bus contracts and SQL schema](event-bus-outbox-schema.md).

### Example flow

```text
Deployment apply completed (Docker API outside DB transaction)
  -> T1: Deployment=succeeded + DesiredSnapshot + GitOps state
       + outbox event deploySuccess and deliveries
  -> COMMIT T1

  -> T2: worker claims Event History delivery; COMMIT T2
  -> T3: Event History persists deploySuccess + ack; COMMIT T3

  -> T2: worker claims Alert Management delivery; COMMIT T2
  -> T3: Alert Management resolves alert + ack; COMMIT T3

  -> independent notification delivery outside a write transaction, then ack
  -> service catalog consumer commits catalog + serviceCatalogUpdated child
  -> separate worker delivery refreshes RAG from the committed catalog
```

**Consistency boundary:** successful Deployment state and its source Outbox event are atomic; subsequent Event History/Alert state is eventually consistent. Failure of a subscriber must not turn a successful Deployment into a failed one. Docker API operations and SQLite cannot share a transaction; a crash between Docker success and T1 commit leaves Deployment `running` and recovery marks it `interrupted` without inventing success.

## Universal outbox and delivery lifecycle

The Outbox is an **operational queue**, not an audit log. It is generic infrastructure belonging to Event Bus, not to Notifications.

- `outbox_events`: one safe versioned event envelope, committed with publisher's state.
- `outbox_deliveries`: one status/lease/retry record per matching subscriber with unique `(event_id, subscription_id)`.
- A worker claims, handles and acknowledges deliveries independently. For **DB-only subscribers**, their database changes, follow-up events and ack must be atomic in their **own processing transaction**. For external effects, never hold a SQLite transaction during network requests: acknowledge later, with at-least-once semantics.
- Retries use bounded backoff. Terminal `failed` stays visible and can be replayed/discarded explicitly. Missing subscriber registrations or incompatible payloads are not silently dropped.
- Successful deliveries do not repeat merely because another subscriber fails. After the **last** successful ack, immediately delete the envelope and all delivery records in that acknowledgment transaction (`ON DELETE CASCADE`), **without retention**.
- Source events with only Event History/Alert consumers still remain in Outbox **until** their independent processing transactions succeed. Never insert-and-delete them in the publisher transaction.
- Protect event payloads from secrets and sensitive Compose data. The legacy `Type.Window()` global deduplication must not suppress actual publications.
- Per-recipient delivery identities and notification deduplication/reply/threading remain owned by the Notifications domain, which consumes the same generic Outbox.

### Tests required

- A subscriber is never invoked before the publisher transaction commits.
- Publisher rollback removes both state change and the queued event.
- DB-only handler updates + ack are atomic in a separate transaction; handler rollback schedules retry without partial projection.
- Two consumers of one event process independently, with retries/leases and no accidental cross-ack.
- Internal `DeploymentFailed` can open an Alert without appearing in Event History; successful `DeploymentSucceeded` can create `deploySuccess` eventually.
- Fully processed events are deleted immediately with no retention; pending/terminal-failed deliveries prevent deletion.
- Events with no subscribers are not enqueued; unknown subscriber/codec failures are observable.

## Deployment lifecycle

- Deployment records a **real attempt to apply a changed effective desired state**, not every reconcile. Distinct retries create distinct deployment IDs. Reconciler owns whether a retry happens; a store must not suppress attempts with the same revision/digest.
- Preparation failures prior to an apply attempt do not create a Deployment. Runtime state and a safe `deployPreparationFailed` fact commit atomically so Alerts and Notifications still observe the failure. Once application has begun, policy rejection is a failed Deployment.
- States: `running`, `succeeded`, `failed`, `interrupted`.
- Apply boundary includes policies, init jobs, stack deployment, and authorized prune. Maintenance work after successful apply does not retroactively change a completed Deployment.
- Successful application means the deploy pipeline completed, **not** that the services are healthy or that actual live convergence was verified. Rollout tracking is explicitly deferred.
- Preserve user-visible `deploySuccess`. Create it at successful completion of a Deployment.
- Only a successful Deployment advances the per-stack `DesiredSnapshot` baseline. A failed or interrupted attempt must never replace a known-good baseline.
- A matching successful baseline is a no-op only when no failed/interrupted attempt followed it. Returning to the baseline after an attempt that may have changed Swarm creates a new attempt.
- Snapshot may contain an effective rendered compose representation suitable for semantic diffs; sensitive values must be redacted or represented safely. Commands, entrypoints, healthcheck commands and init-job scripts are opaque persisted values: displays contain only masks while existing unkeyed fingerprints detect changes. Never persist unmasked secret contents in snapshots, diffs, events, outbox payloads, or logs. Avoid introducing a new secret hashing key. Improvements to `envmasker` are a separate track.
- Effective command identity preserves Compose scalar versus list form because those forms produce different argv; presentation-only YAML formatting remains a no-op.
- Persist meaningful structured changes (`added`, `removed`, `changed` plus semantic details), rather than presenting only flat change counts. A failed attempt can show **planned** changes without claiming they were applied.

## Transactional outbox boundaries

External Docker operations and the database **cannot share a transaction**. The DB can guarantee only that *local state transitions* and their emitted durable records are committed together.

- Before an external apply, persist a `running` Deployment.
- On completed apply, use **one publisher DB transaction** to commit `Deployment=succeeded`, successful `DesiredSnapshot`, GitOps state and a `DeploymentSucceeded` Outbox event plus delivery records. **Do not** also require the Event History projection or Alert resolution in that transaction: they are performed by subscribers later in their own transactions.
- Failed/interrupted transitions and their durable side effects must use the same principle. `deployInterrupted` is a separate durable fact for an unknown external outcome; it is not reported as a known failure.
- If the current process knows the external outcome but completion persistence fails, the reconciler retries that exact success/failed transition before another apply. A running attempt with no known in-process outcome is atomically interrupted before retry policy is evaluated.
- After a crash between Docker API success and the success transaction, the Deployment remains `running` until it is detected as `interrupted`; do not invent success or overwrite the baseline. Actual-state convergence detection is future work.
- A generic Event Bus outbox worker reads due subscriber records, attempts delivery, and marks completion/retry in the database. Define bounded retries/backoff, terminal handling, metrics, and startup recovery.
- Model **separate durable delivery state per destination** (e.g. independent stable subscriber IDs) so partial success across Telegram/webhooks is not treated as all-or-nothing.
- Prevent duplicate scheduling via stable source/event + destination keys, and preserve deduplication semantics without suppressing legitimate new deployment attempts.
- Network notifications provide **at-least-once**, not exactly-once delivery: a crash after an external send and before DB acknowledgement may repeat a message. `ReplyTo`/threading is message metadata, not a notifier-specific new API and not where send results belong.
- All Event Bus publications with subscribers enter `outbox_events` in T1 and remain after its commit until the last acknowledgement in a separate T3. There are no subscription delivery modes. Event History alone controls user visibility.

## Events, alerts, and notifications

- Public Events = **selected user-significant projections** of published domain facts (e.g. `nodeJoined`, `deploySuccess`) chosen by Event History. Every source event is still in the outbox regardless of projection. `nodeJoined` means a genuinely new swarm node, not an existing node becoming Ready.
- `NodeDisconnected`, `DeployFailed`, `DeployPreparationFailed` and `DeployInterrupted` are internal signals / alert inputs, not automatically user-visible Event History rows.
- Alerts maintain their own lifecycle and deduplication identity. Events and Alerts share a UI page, but **remain separate tables**.
- Notifications are a distinct module/concern. Stable public notification semantics can combine technical failure causes so users do not have to guess which low-level event to subscribe to.
- Existing Event History cursor pagination/filtering must become a database query rather than loading, sorting, and filtering the full history in memory.
- Event History retention and legacy `ReadRecent` use the Outbox publication sequence, so a delayed retry cannot evict a newer publication. Legacy import reserves the earlier sequence range and preserves file order.
- Overview `Latest deployments` reads the new deployment repository.

## Schema and one-time import

Keep a single `0001_initial` schema migration in the integration branch; before merging, fold any intermediate schema edits into it. New installations create only the final schema.

Plan for these conceptual tables (naming/details can change during implementation):

`goose_db_version`, `legacy_imports`, `gitops_runtime`, `nodes`, `services`, `secret_metadata`, `recommendations`, `assistant_chats`, `assistant_turns`, `deployments`, `desired_snapshots`, `event_history`, `alerts`, `outbox_events`, `outbox_deliveries`, plus optional notification-domain tables.

Import existing JSON on startup before starting collectors/reconcilers/subscribers. Required properties:

1. Preflight every file for readability and validity; **do not interpret malformed existing data as an empty store**.
2. Import all legacy sources and record a completion marker in a **single SQLite transaction**. Failure rolls back and leaves JSON untouched for retry.
3. Preserve IDs, timestamps, chat usage, alert state/fingerprints, historical event ordering, and current controller data. If a legacy record genuinely has no ID, generate one once, inside the transaction.
4. Legacy JSON remains an untouched recovery backup. A successful import must not be repeated or interleaved with live mutations after a restart.
5. If any import validation or DB integrity check fails, fail startup safely instead of silently starting with empty state. Provide rollback instructions.
6. Do not reconstruct past Deployments from `deploySuccess` event history: there is not enough reliable structured data. Initialize the deployment model cleanly while preserving historic Events. Avoid inventing a successful DesiredSnapshot baseline from unverifiable historical state.

## Engineering sequence (all commits on the integration branch)

1. Verify the SQLite/transactor spike; implement shared DB lifecycle and the single initial schema.
2. Implement the universal Event Bus, safe codec and worker with separate T1/T2/T3 boundaries.
3. Migrate DB-only subscribers and notification adapters; replace the old dispatcher in application wiring.
4. Introduce Deployments, effective-state semantic diffs and successful DesiredSnapshot.
5. Migrate remaining repositories and add the atomic preflighted legacy importer before enabling production DB startup.
6. Rewire API/Overview/Event & Alerts UI, adjust docs/config, and remove obsolete file-write code.
7. Verify data import from real-looking fixture sets, crash/restart at every important boundary, migration replay, full CI and Docker build. Consolidate schema to a single initial migration and merge once.

Release gates: no silent data loss; no false deployment success; stable user-visible `deploySuccess`; no plaintext secret leakage through history/outbox; no duplicate logical notification jobs after restart; compatibility with the project's `CGO_ENABLED=0` build.

## Implementation checkpoint

All listed runtime repositories use the shared SQLite database. Startup imports legacy
JSON before module initialization and marks unfinished deployments interrupted before
the controller runs. QueueDispatcher and its global temporal deduplication are removed.
A bounded Outbox worker pool handles subscriptions while preserving publication order
within each subscription, so external notification latency does not serialize DB projections.

The stack pipeline prepares effective state before creating a running attempt.
Completion atomically commits the attempt, successful baseline, runtime state and
source event; a failed completion write is retried before the next apply, and unknown
running outcomes become durable interrupted facts. Maintenance cleanup runs afterwards. Compose-dependent consumers load
redacted desired state by deployment ID. Service metadata inspection runs outside a
write transaction, then commits the catalog and a `serviceCatalogUpdated` child event
together. RAG consumes that child, not the parent deployment event.

Overview and `/deployments` read the Deployment repository. Events & Alerts share a
page but remain separate tables and repositories. Operator replay/discard commands
are supplied by the `sd` binary. See [the implementation ledger](sqlite-outbox-progress.md)
and [migration/operations notes](../../example/README-sqlite.md). No production rollout,
remote CI run or merge is implied by local verification.
