# SQLite, transactional outbox, and deployment model

Application-owned persistent state resides in one SQLite database under `dataDir`.
The Deployment model records attempts and successful desired snapshots. Public Event
History, Alerts, and notification deliveries are separate projections of domain events.
Ordinary input and artifact files, including Git checkouts, Compose sources, rendered
Compose, configuration, and mounted secrets, remain files.

## Breaking storage cutover

SQLite is the only supported persistent application store. Startup opens
`<dataDir>/swarm-deploy.sqlite`, applies Goose migrations and initializes modules
without inspecting previous JSON repositories.

Existing files such as `controller.state.json`, `event-history.json`,
`alerts.state.json`, `recommendations.state.json`, `nodes.json`,
`services.json`, `secrets.state.json` and assistant chat JSON may remain on disk
as operator-owned archives. The application does not read, import, update or delete
them. No Deployment, Event History row or successful desired baseline is synthesized
from those files or from live Docker resources.

## Architectural boundaries

- `internal/storage` owns connection setup, pragmas, Goose migrations, context
  transactions, and shutdown. Module repositories own their business queries.
- The pure-Go SQLite driver supports the `CGO_ENABLED=0` build. WAL, foreign keys,
  busy timeout, and full synchronization are configured on pooled connections.
- The database uses a local persistent volume. One replica is pinned to one manager;
  an advisory lock beside the database elects the active controller. SQLite WAL does
  not support a shared multi-host database. See [SQLite operations](../operations/sqlite.md).

## Unified Event Bus and module isolation

**Decision:** one Event Bus, one universal two-table Outbox and one asynchronous subscriber processing model. **Publication and handling always use separate database transactions.** No `Transactional`, `Durable` or `AfterCommit` subscription modes.

- `DeploymentService` does not invoke other modules' services or repositories; it publishes a domain event.
- **T1 (publisher):** the application modifies its own state and inserts an Outbox envelope and one delivery per matching subscriber **atomically**. `Bus.Publish(ctx, event)` only enqueues, never invokes subscriber code.
- **T2 (worker claim):** after T1 commits, a worker claims one delivery using a short transaction and a lease.
- **T3 (handler):** DB-only subscribers (Event History, Alert Management) run in a **new** context-propagated DB transaction, then acknowledge delivery in **that same T3**. Subscriber-side state changes and any newly emitted Outbox events commit together with the ack. External notifications execute outside a DB write transaction, and are subsequently acknowledged in a short transaction.
- Each delivery is independent. Failed handlers retry/park without rolling back the previously committed publishing transaction or another successful subscriber.
- Event History decides which published events deserve user-visible history; it does not run within the Deployment publisher's transaction. Projections are **eventually consistent**. For example, `DeploymentSucceeded` can become a public `deploySuccess` after T1 commits; `DeploymentFailed` may update an Alert without becoming a public Event.
- Nested/follow-up events (currently `serviceCatalogUpdated`) are **published from a subscriber's T3**, and picked up in subsequent transactions. Never recursively run their handlers inline.
- Publications with no matching registered subscribers do not create Outbox records.
- Outbox events with matching subscribers are deleted immediately once **every** delivery succeeds; no retention, no deleting pending or terminal-failed work.
- Transactions are propagated through `context.Context` using a context-aware transactor and DB getter (`*sql.DB` or `*sql.Tx`). No explicit `Tx` parameters or `UnitOfWork`. `Bus.Publish` outside an active transaction wraps its own short enqueue transaction.
- Subscriber ids are stable. Delivery uses leases, restart recovery, per-subscriber idempotence, and secret-safe versioned codecs.
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
- Event payloads exclude secrets and sensitive Compose data. Global temporal deduplication does not suppress publications.
- Per-recipient delivery identities and notification deduplication/reply/threading remain owned by the Notifications domain, which consumes the same generic Outbox.

## Deployment lifecycle

- Deployment records a **real attempt to apply a changed effective desired state**, not every reconcile. Distinct retries create distinct deployment IDs. Reconciler owns whether a retry happens; a store must not suppress attempts with the same revision/digest.
- Preparation failures prior to an apply attempt do not create a Deployment. Runtime state and a safe `deployPreparationFailed` fact commit atomically so Alerts and Notifications still observe the failure. Once application has begun, policy rejection is a failed Deployment.
- States: `running`, `succeeded`, `failed`, `interrupted`.
- Apply boundary includes policies, init jobs, stack deployment, and authorized prune. Maintenance work after successful apply does not retroactively change a completed Deployment.
- Successful application means the deploy pipeline completed, **not** that the services are healthy or that actual live convergence was verified. Rollout tracking is explicitly deferred.
- Preserve user-visible `deploySuccess`. Create it at successful completion of a Deployment.
- Only a successful Deployment advances the per-stack `DesiredSnapshot` baseline. A failed or interrupted attempt must never replace a known-good baseline.
- A matching successful baseline is a no-op only when no failed/interrupted attempt followed it. Returning to the baseline after an attempt that may have changed Swarm creates a new attempt.
- Desired snapshots contain effective rendered Compose data suitable for semantic diffs. Commands, entrypoints, healthcheck commands and init-job scripts are opaque persisted values: displays contain only masks while unkeyed fingerprints detect changes. Snapshots, diffs, events, outbox payloads, and logs exclude unmasked secret contents.
- Effective command identity preserves Compose scalar versus list form because those forms produce different argv; presentation-only YAML formatting remains a no-op.
- Persist meaningful structured changes (`added`, `removed`, `changed` plus semantic details), rather than presenting only flat change counts. A failed attempt can show **planned** changes without claiming they were applied.

## Transactional outbox boundaries

External Docker operations and the database **cannot share a transaction**. The DB can guarantee only that *local state transitions* and their emitted durable records are committed together.

- Before an external apply, persist a `running` Deployment.
- On completed apply, use **one publisher DB transaction** to commit `Deployment=succeeded`, successful `DesiredSnapshot`, GitOps state and a `DeploymentSucceeded` Outbox event plus delivery records. **Do not** also require the Event History projection or Alert resolution in that transaction: they are performed by subscribers later in their own transactions.
- Failed/interrupted transitions and their durable side effects must use the same principle. `deployInterrupted` is a separate durable fact for an unknown external outcome; it is not reported as a known failure.
- If the current process knows the external outcome but completion persistence fails, the reconciler retries that exact success/failed transition before another apply. A running attempt with no known in-process outcome is atomically interrupted before retry policy is evaluated.
- If a retained completion races with a terminal transition, recovery rereads that exact deployment ID transactionally. A proven terminal row wins unchanged and the obsolete in-memory completion is discarded with a structured lifecycle-conflict log; missing, inconsistent, or still-running rows remain errors.
- After a crash between Docker API success and the success transaction, the Deployment remains `running` until it is detected as `interrupted`; do not invent success or overwrite the baseline. Actual-state convergence detection is future work.
- The Event Bus worker reads due subscriber records, retries with bounded backoff, and exposes terminal failures and queue metrics. Expired leases recover after restart.
- Model **separate durable delivery state per destination** (e.g. independent stable subscriber IDs) so partial success across Telegram/webhooks is not treated as all-or-nothing.
- Prevent duplicate scheduling via stable source/event + destination keys, and preserve deduplication semantics without suppressing legitimate new deployment attempts.
- Network notifications provide **at-least-once**, not exactly-once delivery: a crash after an external send and before DB acknowledgement may repeat a message. `ReplyTo`/threading is message metadata, not a notifier-specific new API and not where send results belong.
- All Event Bus publications with subscribers enter `outbox_events` in T1 and remain after its commit until the last acknowledgement in a separate T3. There are no subscription delivery modes. Event History alone controls user visibility.

## Events, alerts, and notifications

- Public Events = **selected user-significant projections** of published domain facts (e.g. `nodeJoined`, `deploySuccess`) chosen by Event History. Every source event is still in the outbox regardless of projection. `nodeJoined` means a genuinely new swarm node, not an existing node becoming Ready.
- `NodeDisconnected`, `DeployFailed`, `DeployPreparationFailed` and `DeployInterrupted` are internal signals / alert inputs, not automatically user-visible Event History rows.
- Alerts maintain their own lifecycle and deduplication identity. Events and Alerts share a UI page, but **remain separate tables**.
- Notifications are a distinct module/concern. Stable public notification semantics can combine technical failure causes so users do not have to guess which low-level event to subscribe to.
- Event History cursor pagination and filtering use indexed database queries.
- Event History retention and `ReadRecent` use the Outbox publication sequence, so
  delayed retries cannot evict a newer publication.
- Overview `Latest deployments` reads the Deployment repository.

## Schema and storage cutover

Goose applies the consolidated `0001_initial` schema migration to new databases and
records its version. Restarting an existing database preserves its state.

The initial schema contains `goose_db_version`, `gitops_runtime`, `nodes`,
`services`, `secret_metadata`, `recommendations`, `assistant_chats`,
`assistant_turns`, `deployments`, `desired_snapshots`, `event_history`,
`alerts`, `outbox_events`, `outbox_deliveries` and the supporting projection
tables. There is no import marker table.

This cutover intentionally does not offer an automatic or manual JSON converter.
Back up the data directory before rollout. A fresh SQLite database starts with empty
history and no successful desired baseline; the first real apply attempt creates the
first Deployment.


## Deployment detail presentation and comparison limits

Overview and the paginated Deployments page open the same Deployment Details dialog
by attempt ID. The list endpoint returns summaries only; detail is loaded on demand.
Git commit details remain a separate action. Changes are grouped by resource and
field, with before/after values or masks. Missing values and empty strings are
distinct. Sensitive executable fields and secret content remain masked.

`comparison_basis=none` means the first desired snapshot has no trustworthy
previous baseline. `successful_baseline` and `last_attempt` compare desired
snapshots, not measured live state. ListStackServices records a technical read
(`actual_state_status=observed`), while `verification_status=skipped` and
`comparison_status=unknown` remain until real convergence verification is built.
`succeeded` still means the apply pipeline completed. Failed and interrupted
attempts display planned desired changes, not asserted applied changes.
