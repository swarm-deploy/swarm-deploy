# Drift detection

swarm-deploy compares the desired service state from Git with live Docker Swarm state during reconciliation.

It detects:

- a desired service missing from the cluster;
- a replicated service whose live replica count differs from `deploy.replicas`.

Service sync statuses are:

- `Synced` — live state matches the checked desired fields;
- `OutOfSync` — drift was detected and self-heal is disabled;
- `SyncFailed` — self-heal was attempted but failed.

## Self-heal policy

Enable automatic remediation globally:

```yaml
sync:
  policy:
    selfHeal: true
```

Override the policy for one service with a Compose deploy label:

```yaml
services:
  api:
    image: registry.example.com/api:1.0.0
    deploy:
      replicas: 3
      labels:
        org.swarm-deploy.service.sync.policy.selfHeal: "false"
```

The service label takes precedence over `sync.policy.selfHeal`. Accepted values are
`true`, `1`, `yes`, `y`, `on`, `false`, `0`, `no`, `n`, and `off` (case-insensitive).

Self-heal reapplies only the drifted service and does not prune other stack services.

## Events

Drift reconciliation can emit:

- `ServiceMissed`;
- `ServiceReplicasDiverged`;
- `ServiceRestored`;
- `ServiceRestoreFailed`.

Each event includes `stack_name`, `service_name`, and `commit`.
