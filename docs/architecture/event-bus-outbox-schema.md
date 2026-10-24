# Unified Event Bus and SQLite Outbox: contracts and schema

This expands [SQLite + Deployment architecture](sqlite-outbox-refactor.md). The SQL below is the Outbox subset of `migrations/sqlite/0001_initial.sql`.

## Decision: publication and processing are separate transactions

**Publishing an event never executes its subscribers.** There is **one** Event Bus with one processing path.

1. **Publisher transaction (T1):** a service updates its own state and calls `Bus.Publish(ctx, event)`. The bus records an event envelope and **one delivery row per matching subscriber**, atomically with the service's state. If the transaction rolls back, neither the event nor delivery rows survive.
2. **Worker claim transaction (T2):** once T1 commits, a worker claims a due delivery with an expiring lease and returns control without keeping a DB transaction open.
3. **Subscriber processing transaction (T3):** for a **DB-only subscriber**, perform its projection, publish any follow-up events, acknowledge that delivery, and delete fully processed Outbox events **atomically in one transaction distinct from T1**. On a handler error, T3 rolls back; the claimed delivery is then retried/reported by a separate short error-state transaction. For an **external I/O subscriber**, run HTTP/Telegram outside any DB transaction, then acknowledge/fail it in a short separate transaction. External delivery is at-least-once.
4. **No retention:** when the last delivery has been successfully acknowledged, delete `outbox_events` and its `outbox_deliveries` immediately within the acknowledgement transaction. An event with failed/pending/in-flight deliveries remains.
5. **No subscribers:** no outbox row is created.
6. `Event History` independently decides which events matter to users. It is a **normal Outbox subscriber**, not part of the publisher's transaction. It creates an indexed, independently retained user event history. Deployment failure, preparation failure, interruption and node-disconnection facts can be consumed by Alert Management but omitted from user Events.
7. `DeploymentService` does not invoke Alert Management, Event History or Notifications directly. The Event Bus is a generic durable, asynchronous dispatcher, not a cross-module transaction coordinator.

### Consequence: eventual consistency across modules

A successful deployment's `deployments` row and its `DeploymentSucceeded` outbox event are atomic **in T1**. The public `DeploySuccess` history row and alert resolution are **eventually** materialized by separate consumers after T1 commits. A subscriber failure cannot roll back an already committed successful deployment. Reconciliation and `Latest deployments` use the first-class Deployment repository, **not** an immediately available Event History record. UI and API should tolerate projection lag.

Event History uses a stable subscription and idempotent projection. Failed deliveries remain stored until explicitly replayed or discarded.

## Transaction propagation in Go

The shared `storage.Database` uses a context-aware transactor with nested savepoints.
Its DB getter selects the active transaction from context; callers do not pass `sql.Tx` explicitly.
One `storage.Database` owns the transactor/getter. `BEGIN IMMEDIATE` serializes writers;
WAL permits committed-state readers. Do not share a transaction context between concurrent goroutines.

`outbox.Bus.Subscribe(typeName, stableID, handler)` registers a DB consumer. Network
consumers use the `outbox.External(handler)` worker adapter. This does not change
publication or durability: every matching subscriber gets its own delivery record.
Event Notification transports additionally use `outbox.MonitorDelivery` to persist a
safe transport kind, opaque physical `destination_key`, and channel alias on their delivery row. The adapter is never used
for alert notifications or internal Outbox lifecycle events.
`Run` stops on cancellation; unacknowledged work remains on disk. Missing subscriptions
or incompatible payloads are parked as failed. `Replay` and `Discard` address a single
failed delivery. Prometheus collection reports queue depth by status and subscription.
IDs and schema versions are preserved across restarts. SQLite assigns each publication a monotonic
sequence independent of delivery time; per-resource projection high-water marks prevent delayed alert or
recommendation retries from regressing newer state.
Raw handler errors are not persisted or logged because they can contain credentials;
diagnostics expose event/subscription IDs, attempt, error type and a stable failure code.

- The repository and Bus use a common `DBGetter(ctx)` to select `*sql.Tx` when the context carries a transaction or `*sql.DB` otherwise.
- A root `Bus.Publish(ctx, event)` outside a transaction starts a **short** transaction to insert envelope + deliveries. It **never invokes a handler inline**.
- Subscriber handlers are invoked only by the worker after T1 committed. DB-only subscribers receive a worker-created transaction-bearing context; their repositories, and any `Publish` calls that emit follow-up events, join **T3**.
- Nested events are new outbox publications committed with the handler's database work; their subscribers process them in subsequent worker transactions. No synchronous recursive dispatch or in-transaction FIFO is needed.
- An external-I/O subscriber must not run while an open SQLite write transaction is held; it runs with a non-transactional context and its delivery is acknowledged afterwards.
- DB-only handlers' side effects and delivery acknowledgements commit together for crash-safe idempotence. External handlers run separately from their ACK and therefore require their own idempotency mechanism when they commit database side effects.
- Service Catalog is external because it inspects Docker. After inspection it atomically inserts `service_catalog_receipts`, replaces the catalog and publishes `ServiceCatalogUpdated`; a repeated source event skips both persistence and child publication.
- Publish failure and SQL commit errors propagate back to T1's caller. Subscriber errors are handled asynchronously and **cannot** alter T1's outcome.
- Safe, versioned codecs exclude arbitrary `compose.File`, secrets, Go `error`, and raw Docker objects from SQLite. Global temporal deduplication does not suppress authentic events.

## SQLite DDL: Outbox subset of `0001_initial`

All SQL timestamps are declared as `TIMESTAMP`, stored as UTC Unix nanoseconds in SQLite `INTEGER` values, and converted by the driver. SQLite requires `PRAGMA foreign_keys=ON` for every connection; use WAL, busy timeout, and a pure-Go compatible driver (`CGO_ENABLED=0` build).

```sql
CREATE TABLE outbox_events (
    sequence       INTEGER PRIMARY KEY AUTOINCREMENT,
    id             TEXT NOT NULL UNIQUE,
    event_type     TEXT NOT NULL,
    schema_version INTEGER NOT NULL CHECK (schema_version >= 1),
    occurred_at    TIMESTAMP NOT NULL CHECK (typeof(occurred_at) = 'integer'),
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
    available_at    TIMESTAMP NOT NULL CHECK (typeof(available_at) = 'integer'),
    lease_until     TIMESTAMP CHECK (lease_until IS NULL OR typeof(lease_until) = 'integer'),
    lease_token     TEXT,
    last_error      TEXT,
    updated_at      TIMESTAMP NOT NULL CHECK (typeof(updated_at) = 'integer'),
    monitor_delivery INTEGER NOT NULL DEFAULT 0 CHECK (monitor_delivery IN (0,1)),
    destination     TEXT,
    destination_key TEXT,
    channel         TEXT,
    terminal_failed_at TIMESTAMP
                    CHECK (terminal_failed_at IS NULL OR typeof(terminal_failed_at) = 'integer'),
    PRIMARY KEY (event_id, subscription_id),
    CHECK (
        (status = 'processing' AND lease_until IS NOT NULL AND lease_token IS NOT NULL)
        OR
        (status <> 'processing' AND lease_until IS NULL AND lease_token IS NULL)
    )
);

CREATE INDEX idx_outbox_delivery_due
    ON outbox_deliveries(status, available_at);
CREATE INDEX idx_outbox_delivery_expired_lease
    ON outbox_deliveries(status, lease_until);
```

**Why two tables?** One envelope can fan out to several subscribers with different progress. A successfully handled Event History delivery must not be retried solely because a Telegram destination is temporarily unavailable.

## Processing algorithm

### Publishing: T1

1. Resolve matching subscriptions against a stable registry. With zero matches, do not enqueue anything (optional unhandled-publication metric).
2. Encode/validate the safe versioned payload, allocate a unique event ID and let SQLite assign its publication sequence.
3. Insert into `outbox_events`, then insert **one** `outbox_deliveries` row for **every matching subscriber** with a stable `subscription_id`. Persist the structural monitoring decision and safe destination metadata for Event Notification transports; public event types are eligible automatically, while `AlertOpened`, `AlertResolved`, and Outbox lifecycle types are explicitly forced off.
4. Return without calling handlers. Event and delivery inserts share the publisher's transaction, selected from context.
5. Commit T1 atomically with the publisher's own business state.

### Worker: T2 and T3

1. Claim one due `pending` delivery (or `processing` with expired lease) atomically in a **short T2**, store lease token/deadline, increment attempt count, then commit T2.
2. Load event payload and the subscribed handler. Missing subscription/unsupported codec version is a visible delivery failure, **not** a silent acknowledgment.
3. DB-only subscriber: in **T3**, apply the projection, enqueue follow-up events if needed, and acknowledge the claimed delivery using the lease token. If this is the final success, delete the parent event + all its deliveries before T3 commits.
4. External subscriber: execute outside a DB transaction; then open a short **T3** solely to acknowledge/delete on success, or schedule retry on failure.
5. Handler failure: rollback its T3 work; use a separate short transaction to release or reschedule the claimed delivery with backoff, or mark it terminal `failed`. A monitored terminal transition and its `OutboxDeliveryFailed` publication share this transaction. Never leave a half-applied projection with an acknowledged delivery.
6. Recover expired leases on restart. A stale worker must not be allowed to acknowledge a lease held by a newer worker.
7. Terminal failures remain until operator replay/discard. Replay retains `terminal_failed_at`; only a successful acknowledgement can publish recovery. Recovery and discard consider every subscription with the same physical `destination_key`; the final outstanding terminal delivery determines resolution. Once **all** deliveries are successful, clean the event immediately with no retention period.

Migration `0004` deliberately does not infer monitoring metadata for pre-existing deliveries.
Their status, attempts, lease, and error fields are preserved, while `monitor_delivery` defaults
to false and destination metadata remains null. Pending and expired-processing work continues
with normal delivery semantics, and failed work remains available for diagnostics/replay/discard,
but none of it can retroactively emit failure or recovery lifecycle events.

Example T2 claim SQL:

```sql
WITH due AS (
    SELECT event_id, subscription_id
    FROM outbox_deliveries
    WHERE
        (status = 'pending' AND available_at <= ?)
        OR (status = 'processing' AND lease_until <= ?)
    ORDER BY available_at, event_id, subscription_id
    LIMIT 1
)
UPDATE outbox_deliveries
SET status = 'processing',
    attempts = attempts + 1,
    lease_token = ?,
    lease_until = ?,
    updated_at = ?
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
    INSERT outbox_events(DeploySuccess)
    INSERT deliveries(event-history, alert-management)
    COMMIT

T2: Worker claims (DeploySuccess, event-history)
    COMMIT

T3: Event History subscriber
    INSERT event_history(DeploySuccess)
    ACK delivery(event-history)
    COMMIT

T2/T3: Alert Management separately resolves alert

T2: Worker claims notification delivery
    Send Telegram outside a write transaction; acknowledge in separate T3

T2: Worker claims service catalog delivery
    Inspect Docker outside a write transaction
    Commit source receipt + catalog + ServiceCatalogUpdated child atomically
    ACK the parent delivery separately
    A retry after commit but before ACK is suppressed by the source receipt

T2/T3: RAG child delivery reads committed catalog and refreshes external index
```

The publishing transaction and every handler processing transaction are **different**. Event History and Alert Management can process the same source event independently. Avoid assuming a global order between siblings; explicit chained events express causal dependencies.
