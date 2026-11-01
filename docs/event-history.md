## Event history

Event details are a public attribute contract. Shared attributes always use the
following `snake_case` names:

| Attribute | Semantics |
| --- | --- |
| `stack_name` | Stack name |
| `service_name` | Service name without the stack prefix |
| `deployment_id` | Persisted desired-state deployment identifier |
| `attempt_id` | Persisted execution attempt identifier |
| `commit` | Source revision |
| `username` | User who initiated the action |
| `node_id`, `node_name` | Swarm node identifier and hostname |
| `network_id`, `network_name` | Swarm network identifier and name |
| `status` | Event-specific lifecycle status |
| `error` | Bounded, secret-masked human-readable diagnostic |
| `error_code` | Stable machine-readable failure category |
| `stage` | Stage responsible for the failure |

The obsolete `stack`, `service`, `triggered_by`, and `diagnostic` detail keys are
not emitted. Event-specific attributes not listed above also use `snake_case`.
Existing SQLite history rows are not migrated; the standardized keys apply to
newly persisted events.

| Type                               | Severity | Category   | Trigger                             | Details keys                                                 |
|------------------------------------|----------|------------|-------------------------------------|--------------------------------------------------------------|
| `DeploySuccess`                    | `info`   | `sync`     | Successful stack deployment         | `stack_name`, `commit`, `deployment_id`, `attempt_id`, `status` |
| `ServicePruned`                    | `info`   | `sync`     | Orphaned managed service was removed | `stack_name`, `service_name`, `commit`                       |
| `NetworkCreated`                   | `info`   | `sync`     | Managed network was created          | `network_name`, `network_id`, `driver`                       |
| `SendNotificationFailed`           | `error`  | `sync`     | Notification delivery failure       | `destination`, `channel`, `event_type`, `error` (if present) |
| `WebhookReceived`                  | `info`   | `sync`     | Authenticated webhook received and reconciliation scheduling attempted | `queued` (`true` or `false`)                     |
| `SyncManualStarted`                | `info`   | `sync`     | Manual sync run started             | `username` (if present)                                      |
| `NodeJoined`                       | `info`   | `swarm`    | New node joined the swarm           | `node_id`, `node_name` (if present), `role` (if present)      |
| `NodeConnected`                    | `info`   | `swarm`    | Swarm node became ready             | `node_id`, `node_name` (if present), `status`                 |
| `NodeDisconnected`                 | `alert`  | `swarm`    | Swarm node left ready state         | `node_id`, `node_name` (if present), `status`                 |
| `ServiceMissed`                    | `alert`  | `sync`     | Desired service is absent in swarm state | `stack_name`, `service_name`, `commit`                  |
| `ServiceDrifted`                   | `warn`   | `sync`     | Existing service differs from desired state | `stack_name`, `service_name`, `commit`, `reason`       |
| `ServiceDriftFixed`                | `info`   | `sync`     | Live service state is confirmed converged | `stack_name`, `service_name`, `commit`                  |
| `ServiceReplicasIncreased`         | `info`   | `sync`     | Service replicas count increased    | `stack_name`, `service_name`, `previous_replicas`, `current_replicas`, `username` (if present) |
| `ServiceReplicasDecreased`         | `info`   | `sync`     | Service replicas count decreased    | `stack_name`, `service_name`, `previous_replicas`, `current_replicas`, `username` (if present) |
| `ServiceRestarted`                 | `info`   | `sync`     | Service restarted                   | `stack_name`, `service_name`, `username` (if present)        |
| `UserAuthenticated`                | `info`   | `security` | User passed web authentication      | `username`                                                   |
| `AssistantPromptInjectionDetected` | `alert`  | `security` | Assistant prompt injection detected | `detector`, `prompt` (if present), `username` (if present)   |

Selected user-visible events are stored in
`event_history` in `<dataDir>/swarm-deploy.sqlite`. Internal deployment failure facts and
`NodeDisconnected` records remain alert inputs and are excluded from history. New event
identifiers use PascalCase.
The table above describes public event types, not a promise that every
signal appears in user history. `DeployFailed`, preparation failures, and interrupted attempts are internal
deployment lifecycle facts consumed by Deployment Model and Alert Management; they are
not public event names and are not available under `notifications.events`.
Known terminal service convergence failures publish internal `DeployFailed` with
`error_code=convergence_failed` and `stage=verification`; observation failures and
verification interruptions publish `DeployInterrupted` with `verification_unknown`.
Retry-deferred scheduler cycles publish neither event and do not increase alert
occurrences. These remain internal facts, excluded from Event History.
Deployment failures, node disconnections, and unresolved service drift feed Alert Management.
Success, reconnection, and confirmed drift recovery resolve the respective open incident. New SQLite history records use the safe event codec,
which omits raw prompts, logs and error strings.

`ConfigurationChanged` is an internal, payload-free signal emitted after an effective
in-memory configuration change. Subscribers inspect the current configuration when they
handle it; the signal is not a historical configuration snapshot and is excluded from history.

Startup does not read, import, update or delete `event-history.json`. The file may
remain as an operator-owned archive, but SQLite is the only active Event History store.
The Outbox uses a bounded worker pool after publication commits and preserves ordering
within each subscription. Failed projections retry independently. The internal `ServiceCatalogUpdated` event updates
RAG after service metadata is committed and is excluded from user history.

History can be viewed via API:

- `GET /api/v1/events` - returns latest stored events
  - optional query filters:
    - `severities` - list of severities (`info`, `warn`, `error`, `alert`)
    - `categories` - list of categories (`sync`, `security`, `swarm`)
    - `types` - list of event types
    - `stack_name` - list of exact `stack_name` attribute values
    - `service_name` - list of exact `service_name` attribute values
    - `since` - inclusive timestamp
    - `limit` - maximum events to return (1-100)
    - `sort` - `time` or `severity`; enables paginated sorting
    - `order` - `asc` or `desc` (default `desc` when paging)
    - `cursor` - opaque `nextCursor` returned by the previous page

When `sort`, `order`, or `cursor` is supplied, API pagination returns at
most 50 events by default, ordered by the requested field. If more events
exist, the response includes `nextCursor`; repeat the request with that cursor
and the same sort and filters to fetch the next page.

Existing requests without paging parameters keep the oldest-first response
order. Overview now reads `/api/v1/deployments` instead of history.

Runtime history size is bounded by `eventHistory.capacity`. New projections remove
oldest entries by publication sequence. SQLite filters and cursor queries run directly
against the projection; source event IDs prevent duplicate projection.

Config example:
```yaml
# Event history configuration.
eventHistory:
  capacity: 500
```
Event notifications use PascalCase event type identifiers. Synchronization failures are delivered
through the `sync_failed` alert lifecycle instead of Event Notifications. Durable payloads intentionally
omit raw Compose, environment, logs, arbitrary errors and assistant prompts.
`SendNotificationFailed` has no runtime publisher;
delivery diagnostics live in outbox status/metrics rather than recursive notifications.
