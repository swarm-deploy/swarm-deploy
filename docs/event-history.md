## Event history

All runtime events are persisted to disk in `.swarm-deploy/event-history.json` and can be viewed via API:

- `GET /api/v1/events` - returns latest stored events

History size is bounded by `eventHistory.capacity` in config. When limit is reached, the oldest event is removed.

Config example:
```yaml
# Event history configuration.
eventHistory:
  capacity: 500
```
