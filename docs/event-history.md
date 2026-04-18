## Event history

| Type                               | Trigger                             | Details keys                                                 |
|------------------------------------|-------------------------------------|--------------------------------------------------------------|
| `deploySuccess`                    | Successful stack deployment         | `stack`, `commit`                                            |
| `deployFailed`                     | Failed stack deployment             | `stack`, `commit`, `error` (if present)                      |
| `sendNotificationFailed`           | Notification delivery failure       | `destination`, `channel`, `event_type`, `error` (if present) |
| `syncManualStarted`                | Manual sync run started             | `triggered_by` (if present)                                  |
| `serviceReplicasIncreased`         | Service replicas count increased    | `stack`, `service`, `previous_replicas`, `current_replicas`, `username` (if present) |
| `serviceReplicasDecreased`         | Service replicas count decreased    | `stack`, `service`, `previous_replicas`, `current_replicas`, `username` (if present) |
| `userAuthenticated`                | User passed web authentication      | `username`                                                   |
| `assistantPromptInjectionDetected` | Assistant prompt injection detected | `detector`, `prompt` (if present), `username` (if present)   |

All runtime events are persisted to disk in `.swarm-deploy/event-history.json` and can be viewed via API:

- `GET /api/v1/events` - returns latest stored events

History size is bounded by `eventHistory.capacity` in config. When limit is reached, the oldest event is removed.

Config example:
```yaml
# Event history configuration.
eventHistory:
  capacity: 500
```
