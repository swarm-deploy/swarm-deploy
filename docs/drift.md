# Drift detection

swarm-deploy compares desired service state from Git with live Docker Swarm state during each sync run.

## Sync status model

Per service runtime status:

- `Synced` - runtime state matches specification.
- `OutOfSync` - runtime state diverges from specification.
  - Service is missing in cluster.
  - Service replicas count differs from desired replicas.
- `SyncFailed` - automatic remediation failed after drift was detected.

## Configuration

Use `sync.policy`:

```yaml
sync:
  policy:
    selfHeal: true
```

- `selfHeal` enables automatic remediation of detected drift.

## Service-level policy override

You can override self-heal policy per service with deploy labels:

```yaml
services:
  api:
    deploy:
      labels:
        org.swarm-deploy.service.sync.policy.selfHeal: "true"
```

Priority:

1. Service label `org.swarm-deploy.service.sync.policy.selfHeal`
2. Global `sync.policy.selfHeal`

## Drift events

When drift is detected and processed, swarm-deploy emits:

- `serviceMissed`
- `serviceRestored`
- `serviceRestoreFailed`
- `serviceReplicasDiverged`

See [Event History](./event-history.md) for event details.

For a missed service, the `serviceMissed` event includes `stack_name`, `service_name`, and `commit`.
