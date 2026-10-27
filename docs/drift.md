# Drift detection

swarm-deploy compares the desired service state from Git with live Docker Swarm state during reconciliation.

It detects:

- a desired service missing from the cluster;
- a replicated service whose live replica count differs from `deploy.replicas`.

Service sync statuses are:

- `Synced` — live state matches the checked desired fields;
- `OutOfSync` — drift is present, self-healing failed, or convergence could not be verified.

## Self-heal policy

Enable automatic remediation globally:

```yaml
sync:
  policy:
    selfHeal: true
```

Override the global policy for one stack in `stacks.yaml`:

```yaml
stacks:
  - name: app
    composeFile: app.yaml
    sync:
      policy:
        selfHeal: false
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

The service label takes precedence over `stacks[].sync.policy.selfHeal`, which takes
precedence over the global `sync.policy.selfHeal`. Accepted service-label values are
`true`, `1`, `yes`, `y`, `on`, `false`, `0`, `no`, `n`, and `off` (case-insensitive).

Self-heal reapplies only the drifted service. It does not execute Init Jobs, redeploy or
prune unrelated services, or reconcile resources not referenced by that service. Referenced
networks, configs, secrets and named volumes are retained. Existing external-resource,
secret rotation, ownership and name-resolution behavior is reused. When a service omits
`networks`, a configured Compose `default` network is retained, including its external
name and options.

After applying all eligible repairs, swarm-deploy reloads the stack services with one
Docker API request and analyzes them again. The verified Swarm state is authoritative:
the service becomes `Synced` whenever verification confirms convergence, even if the
Docker CLI returned an error after applying the change. Persistent drift or an unavailable
verification read leaves the service `OutOfSync`, with details in `SyncError`;
the CLI error remains in logs and tracing.

Verification covers the complete desired stack. Drift first observed during that read is
recorded and emits the normal transition event, but is not repaired until the next
reconciliation.

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
transient alert. Removing a stack resolves all of its service-drift alerts with
`resource_removed`.

Alert Management derives service drift only from events processed after this feature is
enabled; it does not backfill alerts from persisted GitOps synchronization status. The
current event contract also cannot safely identify an individual service removed from a
retained stack, so such alerts are not resolved automatically. Both cases require a future
event-contract discussion rather than cross-module state reads.
