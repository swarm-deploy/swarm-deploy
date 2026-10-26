# Drift detection

swarm-deploy compares the desired service state from Git with live Docker Swarm state during reconciliation.

It detects:

- a desired service missing from the cluster;
- a replicated service whose live replica count differs from `deploy.replicas`.

Service sync statuses are:

- `Synced` — live state matches the checked desired fields;
- `OutOfSync` — drift was detected and self-heal is disabled;
- `SyncFailed` — self-heal was attempted but application or verification did not restore confirmed convergence.

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

Self-heal reapplies only the drifted service. It does not execute Init Jobs, redeploy or
prune unrelated services, or reconcile resources not referenced by that service. Referenced
networks, configs, secrets and named volumes are retained. Existing external-resource,
secret rotation, ownership and name-resolution behavior is reused.

After applying all eligible repairs, swarm-deploy reloads the stack services with one
Docker API request and analyzes them again. A successful deploy command is not convergence:
the service becomes `Synced` only after this verification. An apply failure, persistent
drift, or an unavailable verification read produces `SyncFailed`.

## Events

Drift reconciliation emits state transitions, not attempts:

- `ServiceMissed` when a desired service is absent;
- `ServiceDrifted` when an existing service differs from desired state (currently replica count only);
- `ServiceDriftFixed` after live state is confirmed converged, whether repaired by
  self-heal, an external Docker change, or a normal deployment.

Repeated observation of the same unresolved episode does not emit another detection
event. A new episode after `ServiceDriftFixed` does. `ServiceDrifted` also includes the
drift `reason`. Failed self-heal does not have a separate public failure event.

## Service drift alert

Confirmed unresolved drift opens one `ServiceDrift` alert (`service_drift`) for resource
type `service` and resource ID `<stack>/<service>`. Repeated detections update the active
alert. `ServiceDriftFixed` resolves it. A drift repaired and verified in the same
reconciliation emits the detection and fixed history transitions but does not open a
transient alert.
