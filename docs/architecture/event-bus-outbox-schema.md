# Unified Event Bus and SQLite Outbox: contracts and schema

Status: **infrastructure implemented; application integration pending**. Integration branch: `feat/sqlite-outbox-refactor`.

This expands [SQLite + Deployment architecture](sqlite-outbox-refactor.md). The SQL below is the Outbox subset of `internal/storage/migrations/0001_initial.sql`.

## Decision: publication and processing are separate transactions

**Publishing an event never executes its subscribers.** There is **one** Event Bus with one processing path. The previously proposed `Transactional` / `Durable` delivery modes and `AfterCommit` mode are removed.

1. **Publisher transaction (T1):** a service updates its own state and calls `Bus.Publish(ctx, event)`. The bus records an event envelope and **one delivery row per matching subscriber**, atomically with the service's state. If the transaction rolls back, neither the event nor delivery rows survive.
2. **Worker claim transaction (T2):** once T1 commits, a worker claims a due delivery with an expiring lease and returns control without keeping a DB transaction open.
3. **Subscriber processing transaction (T3):** for a **DB-only subscriber**, perform its projection, publish any follow-up events, acknowledge that delivery, and delete fully processed Outbox events **atomically in one transaction distinct from T1**. On a handler error, T3 rolls back; the claimed delivery is then retried/reported by a separate short error-state transaction. For an **external I/O subscriber**, run HTTP/Telegram outside any DB transaction, then acknowledge/fail it in a short separate transaction. External delivery is at-least-once.
4. **No retention:** when the last delivery has been successfully acknowledged, delete `outbox_events` and its `outbox_deliveries` immediately within the acknowledgement transaction. An event with failed/pending/in-flight deliveries remains.
5. **No subscribers:** no outbox row is created; review unused event types/publishers rather than silently building an unused event log.
6. `Event History` independently decides which events matter to users. It is a **normal Outbox subscriber**, not part of the publisher's transaction. It creates an indexed, independently retained user event history. `DeploymentFailed` and `NodeDisconnected` can be consumed by Alert Management but omitted from user Events.
7. `DeploymentService` does not invoke Alert Management, Event History or Notifications directly. The Event Bus is a generic durable, asynchronous dispatcher, not a cross-module transaction coordinator.

### Consequence: eventual consistency across modules

A successful deployment's `deployments` row and its `DeploymentSucceeded` outbox event are atomic **in T1**. The public `deploySuccess` history row and alert resolution are **eventually** materialized by separate consumers after T1 commits. A subscriber failure cannot roll back an already committed successful deployment. Reconciliation and `Latest deployments` use the first-class Deployment repository, **not** an immediately available Event History record. UI and API should tolerate projection lag.

If Event History materialization is a required user-visible side effect, make its subscription mandatory at wiring time, surface failed deliveries and retries, and implement idempotent projections; do not pretend an async consumer can deliver same-transaction cross-module guarantees.

## Transaction propagation in Go

Use a context-aware transactor, not explicit `sql.Tx` method parameters or `UnitOfWork` APIs.
The executable spike in `experiments/sqlite-transactor` verifies `Thiht/transactor v1.1.0`
with `modernc.org/sqlite v1.34.5`: rollback, nested savepoints, concurrent writes and
connection pragmas passed both with `CGO_ENABLED=0` and separately with `-race`.
The DBGetter returns the library's savepoint wrapper, not a concrete `*sql.Tx`.
One `storage.Database` owns the transactor/getter. `BEGIN IMMEDIATE` serializes writers;
WAL permits committed-state readers. Do not share a transaction context between concurrent goroutines.

`outbox.Bus.Subscribe(typeName, stableID, handler)` registers a DB consumer. Network
consumers use the `outbox.External(handler)` worker adapter. This does not change
publication or durability: every matching subscriber gets its own delivery record.
`Run` stops on cancellation; unacknowledged work remains on disk. Missing subscriptions
or incompatible payloads are parked as failed. `Replay` and `Discard` address a single
failed delivery. Prometheus collection reports queue depth by status and subscription.
Raw handler errors are not persisted or logged because they can contain credentials;
diagnostics expose event/subscription IDs, attempt, error type and a stable failure code.

```go
type Publisher interface {
    Publish(ctx context.Context, event events.Event) error
}

type Subscriber interface {
    Handle(ctx context.Context, event events.Envelope) error
}

// Subscriber names/IDs must be stable across restarts.
type Bus interface {
    Publisher
    Subscribe(eventType events.TypeName, subscriptionID string, handler Subscriber) error
}

func (s *DeploymentService) Complete(ctx context.Context, id string) error {
    return s.transactor.WithinTransaction(ctx, func(ctx context.Context) error {
        if err := s.deployments.Complete(ctx, id); err != nil {
            return err
        }
        return s.events.Publish(ctx, events.DeploymentSucceeded{DeploymentID: id})
    })
}
```

- The repository and Bus use a common `DBGetter(ctx)` to select `*sql.Tx` when the context carries a transaction or `*sql.DB` otherwise.
- A root `Bus.Publish(ctx, event)` outside a transaction starts a **short** transaction to insert envelope + deliveries. It **never invokes a handler inline**.
- Subscriber handlers are invoked only by the worker after T1 committed. DB-only subscribers receive a worker-created transaction-bearing context; their repositories, and any `Publish` calls that emit follow-up events, join **T3**.
- Nested events (`AlertResolved`, `PublicEventRecorded`, etc.) are **new outbox publications in T3**; their subscribers process them in subsequent worker transactions. No synchronous recursive dispatch or in-transaction FIFO is needed.
- An external-I/O subscriber must not run while an open SQLite write transaction is held; it runs with a non-transactional context and its delivery is acknowledged afterwards.
- Distinguish DB-only and external subscribers at the worker/adapter boundary, **not** through `Transactional` and `Durable` event-subscription modes. A DB-only handler's side effects and its delivery acknowledgement must commit together for crash-safe idempotence.
- Publish failure and SQL commit errors propagate back to T1's caller. Subscriber errors are handled asynchronously and **cannot** alter T1's outcome.
- Use per-event safe, versioned codecs for persistence; do not marshal arbitrary `compose.File`, secrets, Go `error`, or raw Docker objects into SQLite. Global old `Type.Window()` dedup must not suppress authentic events.

## SQLite DDL: Outbox subset of `0001_initial`

All timestamps are UTC Unix milliseconds. SQLite requires `PRAGMA foreign_keys=ON` for every connection; use WAL, busy timeout, and a pure-Go compatible driver (`CGO_ENABLED=0` build).

```sql
CREATE TABLE outbox_events (
    id             TEXT PRIMARY KEY,
    event_type     TEXT NOT NULL,
    schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
    occurred_at_ms INTEGER NOT NULL,
    correlation_id TEXT,
    causation_id   TEXT,
    payload        TEXT NOT NULL CHECK (json_valid(payload))
);

CREATE TABLE outbox_deliveries (
    event_id        TEXT NOT NULL
                    REFERENCES outbox_events(id) ON DELETE CASCADE,
    subscription_id TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'processing', 'delivered', 'failed')),
    attempts        INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at_ms INTEGER NOT NULL,
    lease_until_ms  INTEGER,
    lease_token     TEXT,
    last_error      TEXT,
    updated_at_ms   INTEGER NOT NULL,
    PRIMARY KEY (event_id, subscription_id),
    CHECK (
        (status = 'processing' AND lease_until_ms IS NOT NULL AND lease_token IS NOT NULL)
        OR
        (status <> 'processing' AND lease_until_ms IS NULL AND lease_token IS NULL)
    )
);

CREATE INDEX idx_outbox_delivery_due
    ON outbox_deliveries(status, available_at_ms);
CREATE INDEX idx_outbox_delivery_expired_lease
    ON outbox_deliveries(status, lease_until_ms);
```

**Why two tables?** One envelope can fan out to several subscribers with different progress. A successfully handled Event History delivery must not be retried solely because a Telegram destination is temporarily unavailable.

## Processing algorithm

### Publishing: T1

1. Resolve matching subscriptions against a stable registry. With zero matches, do not enqueue anything (optional unhandled-publication metric).
2. Encode/validate the safe versioned payload and allocate a unique event ID.
3. Insert into `outbox_events`, then insert **one** `outbox_deliveries` row for **every matching subscriber** with a stable `subscription_id`; no special mode is required.
4. Return without calling handlers. Event and delivery inserts share the publisher's transaction, selected from context.
5. Commit T1 atomically with the publisher's own business state.

### Worker: T2 and T3

1. Claim one due `pending` delivery (or `processing` with expired lease) atomically in a **short T2**, store lease token/deadline, increment attempt count, then commit T2.
2. Load event payload and the subscribed handler. Missing subscription/unsupported codec version is a visible delivery failure, **not** a silent acknowledgment.
3. DB-only subscriber: in **T3**, apply the projection, enqueue follow-up events if needed, and acknowledge the claimed delivery using the lease token. If this is the final success, delete the parent event + all its deliveries before T3 commits.
4. External subscriber: execute outside a DB transaction; then open a short **T3** solely to acknowledge/delete on success, or schedule retry on failure.
5. Handler failure: rollback its T3 work; use a separate short transaction to release or reschedule the claimed delivery with backoff, or mark it terminal `failed`. Never leave a half-applied projection with an acknowledged delivery.
6. Recover expired leases on restart. A stale worker must not be allowed to acknowledge a lease held by a newer worker.
7. Terminal failures remain until operator replay/discard. Once **all** deliveries are successful, clean the event immediately with no retention period.

Example T2 claim SQL:

```sql
WITH due AS (
    SELECT event_id, subscription_id
    FROM outbox_deliveries
    WHERE
        (status = 'pending' AND available_at_ms <= ?)
        OR (status = 'processing' AND lease_until_ms <= ?)
    ORDER BY available_at_ms, event_id, subscription_id
    LIMIT 1
)
UPDATE outbox_deliveries
SET status = 'processing',
    attempts = attempts + 1,
    lease_token = ?,
    lease_until_ms = ?,
    updated_at_ms = ?
WHERE (event_id, subscription_id) IN
      (SELECT event_id, subscription_id FROM due)
RETURNING event_id, subscription_id, attempts, lease_token;
```

Acknowledge in T3 using `UPDATE ... WHERE status='processing' AND lease_token=?` and check exactly one affected row. Then immediately:

```sql
DELETE FROM outbox_events
WHERE id = ?
  AND NOT EXISTS (
      SELECT 1
      FROM outbox_deliveries
      WHERE event_id = outbox_events.id
        AND status <> 'delivered'
  );
```

Foreign-key cascade removes all delivery records. The delete is **not** a retention sweep.

## Event chain example

```text
T1: DeploymentService
    UPDATE deployments
    INSERT outbox_events(DeploymentSucceeded)
    INSERT deliveries(event-history, alert-management)
    COMMIT

T2: Worker claims (DeploymentSucceeded, event-history)
    COMMIT

T3: Event History subscriber
    INSERT event_history(deploySuccess)
    INSERT outbox_events(PublicEventRecorded) + downstream deliveries, if needed
    ACK delivery(event-history)
    COMMIT

T2/T3: Alert Management separately resolves alert
    and optionally publishes AlertResolved for Notifications

T2/T3: Notification subscriber eventually sends Telegram
```

The publishing transaction and every handler processing transaction are **different**. Event History and Alert Management can process the same source event independently. Avoid assuming a global order between siblings; explicit chained events express causal dependencies.

## Crash / consistency tests

1. T1 rollback produces **no** outbox work and no business transition; T1 commit makes all matching deliveries available.
2. A handler is **never called before T1 commit**, even if T1 pauses or later rolls back.
3. Failure of Event History after T1 commit does **not** undo successful Deployment; delivery retries and later materializes exactly one user event.
4. Handler DB writes and its delivery acknowledgment are atomic in T3; T3 rollback retries without partial history/alert state.
5. Follow-up publications created by a DB handler roll back with its T3 failure; their subscribers run in independent later transactions.
6. Multiple subscribers process/retry independently; a failed Telegram delivery does not replay successful Event History projection.
7. Expired lease recovery, stale-worker fencing, and concurrent worker claims prevent two simultaneous valid acknowledgements.
8. External I/O crash after send but before ack may resend; use receiver idempotency keys where supported.
9. Unknown/removed subscriber registrations and payload versions are parked/observable.
10. Fully processed events are deleted immediately, with no retention; pending and failed deliveries are never swept by age.
11. Publications with no subscribers do not create outbox records.
12. No plain secrets, unmasked env or raw compose definitions in persisted event payloads.

**Important:** this architecture intentionally trades cross-module atomicity for durable asynchronous projections. Never present Event History's eventual projection as the atomic result of deployment completion.
