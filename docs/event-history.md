## Event history

| Type                               | Severity | Category   | Trigger                             | Details keys                                                 |
|------------------------------------|----------|------------|-------------------------------------|--------------------------------------------------------------|
| `deploySuccess`                    | `info`   | `sync`     | Successful stack deployment         | `stack`, `commit`                                            |
| `deployFailed`                     | `alert`  | `sync`     | Failed stack deployment             | `stack`, `commit`, `error` (if present)                      |
| `servicePruned`                    | `info`   | `sync`     | Orphaned managed service was removed | `stack_name`, `service_name`, `commit`                       |
| `networkCreated`                   | `info`   | `sync`     | Managed network was created          | `network_name`, `network_id`, `driver`                       |
| `sendNotificationFailed`           | `error`  | `sync`     | Notification delivery failure       | `destination`, `channel`, `event_type`, `error` (if present) |
| `webhookReceived`                  | `info`   | `sync`     | Authenticated webhook received and reconciliation scheduling attempted | `queued` (`true` or `false`)                     |
| `syncManualStarted`                | `info`   | `sync`     | Manual sync run started             | `triggered_by` (if present)                                  |
| `nodeJoined`                       | `info`   | `swarm`    | New node joined the swarm           | `node_id`, `node_name` (if present), `role` (if present)      |
| `nodeConnected`                    | `info`   | `swarm`    | Swarm node became ready             | `node_id`, `node_name` (if present), `status`                 |
| `nodeDisconnected`                 | `alert`  | `swarm`    | Swarm node left ready state         | `node_id`, `node_name` (if present), `status`                 |
| `serviceMissed`                    | `alert`  | `sync`     | Desired service is absent in swarm state | `stack_name`, `service_name`, `commit`                  |
| `serviceReplicasIncreased`         | `info`   | `sync`     | Service replicas count increased    | `stack`, `service`, `previous_replicas`, `current_replicas`, `username` (if present) |
| `serviceReplicasDecreased`         | `info`   | `sync`     | Service replicas count decreased    | `stack`, `service`, `previous_replicas`, `current_replicas`, `username` (if present) |
| `serviceRestarted`                 | `info`   | `sync`     | Service restarted                   | `stack`, `service`, `username` (if present)                  |
| `userAuthenticated`                | `info`   | `security` | User passed web authentication      | `username`                                                   |
| `assistantPromptInjectionDetected` | `alert`  | `security` | Assistant prompt injection detected | `detector`, `prompt` (if present), `username` (if present)   |

On the SQLite integration branch, selected user-visible events are stored in
`event_history` in `<dataDir>/swarm-deploy.sqlite`. New `deployFailed` and
`nodeDisconnected` records are excluded from history; imported history is preserved
unchanged. The table above describes available event types, not a promise that every
signal appears in user history. Deployment failures feed Alert Management; node-alert
integration is still pending. New SQLite history records use the safe event codec,
which omits raw prompts, logs and error strings.

The startup importer reads the old `event-history.json` once, together with all other
legacy stores, and leaves it untouched as a backup. Runtime writes do not update JSON.
The application still uses QueueDispatcher at this intermediate checkpoint; reliable
Outbox delivery and the Deployment model are not yet integrated.

History can be viewed via API:

- `GET /api/v1/events` - returns latest stored events
  - optional query filters:
    - `severities` - list of severities (`info`, `warn`, `error`, `alert`)
    - `categories` - list of categories (`sync`, `security`, `swarm`)
    - `types` - list of event types
    - `since` - inclusive timestamp
    - `limit` - maximum events to return (1-100)
    - `sort` - `time` or `severity`; enables paginated sorting
    - `order` - `asc` or `desc` (default `desc` when paging)
    - `cursor` - opaque `nextCursor` returned by the previous page

When `sort`, `order`, or `cursor` is supplied, API pagination returns at
most 50 events by default, ordered by the requested field. If more events
exist, the response includes `nextCursor`; repeat the request with that cursor
and the same sort and filters to fetch the next page.

Existing requests without paging parameters keep the legacy oldest-first response
order (including `limit` requests used by Overview).

Runtime history size is bounded by `eventHistory.capacity`. New projections remove
oldest entries by publication sequence. Import preserves all legacy records without
truncation; normal retention resumes on the next new projection. SQL performs both
legacy filters and cursor queries; source event IDs prevent duplicate projection.

Config example:
```yaml
# Event history configuration.
eventHistory:
  capacity: 500
```
