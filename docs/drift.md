# Drift detection

swarm-deploy compares the desired service state from Git with live Docker Swarm state during reconciliation.

It detects:

- a desired service missing from the cluster;
- a replicated service whose live replica count differs from `deploy.replicas`.

Service sync statuses are:

- `Synced` — live state matches the checked desired fields;
- `OutOfSync` — drift is present, self-healing failed, or convergence could not be verified.

## Self-heal policy

Detection and repair are independent: drift is reported even when self-healing is
disabled. The existing `selfHeal` boolean controls automatic repair only.

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
Whitespace is trimmed. Invalid boolean values are Compose validation errors.

Self-heal reapplies only the drifted service. It does not execute Init Jobs, redeploy or
prune unrelated services, or reconcile resources not referenced by that service. Referenced
networks, configs, secrets and named volumes are retained. Existing external-resource,
secret rotation, ownership and name-resolution behavior is reused. When a service omits
`networks`, a configured Compose `default` network is retained, including its external
name and options.

After applying all eligible repairs, swarm-deploy reloads the stack services with one
Docker API request and analyzes them again. The verified Swarm state is authoritative:
the service becomes `Synced` whenever verification confirms convergence, even if the
native Docker API application returned an error after applying the change. Persistent drift or an unavailable
verification read leaves the service `OutOfSync`, with details in `SyncError`;
the application error remains in logs and tracing.

Verification covers the complete desired stack. Drift first observed during that read is
recorded and emits the normal transition event, but is not repaired until the next
reconciliation.

## Replica drift policy and scheduled services

The service-level Compose deploy label
`org.swarm-deploy.service.sync.policy.selfHeal.replicas` controls **replica drift
detection**, independently of the `selfHeal` repair policy. It accepts the same boolean
values as the service-level `selfHeal` label.

When absent or `true`, replica comparison stays enabled, preserving existing behavior.
When `false`, desired and actual replica differences do not make the service
`OutOfSync` and do not generate replica drift events, alerts, or self-heal attempts.
Missing services are still detected. The same setting applies to the initial drift
analysis and the verification after self-heal.

## Replica ownership during deployments

`org.swarm-deploy.service.sync.policy.replicasManaged` is a service-level Compose
**deploy label**. It uses the same boolean parsing and validation as `selfHeal`.

| Policy | Default | Effect |
| --- | --- | --- |
| `selfHeal` | Inherits stack/global policy | Controls automatic repair. It does not disable normal deployments. |
| `selfHeal.replicas` | `true` | Controls replica drift detection only. It does not preserve replicas during normal deployments. |
| `replicasManaged` | `true` | swarm-deploy owns replica counts during deployment. `false` assigns ownership to an external controller and also excludes replica differences from drift detection, regardless of `selfHeal.replicas`. |

For `replicasManaged=false`, an existing replicated service keeps Docker's current
replica count when another field changes. Replica differences and external
`ForceUpdate` changes alone do not cause a service update. Optimistic version
conflict retries re-read and preserve both values. Initial service creation still
uses Compose's replica count; use `replicas: 0` to avoid starting an initial job.
Missing services are still detected; `selfHeal=false` prevents their automatic repair.

External replica ownership **does not guarantee safe immediate delivery of task-template
changes**. A template update can interrupt a running task or start an unscheduled
execution even after the previous task completed. swarm-deploy requires zero
configured replicas and no active tasks before applying task-recreating changes.
Nonzero replicas block these changes even when no tasks are running. Service labels
and rollout policy changes can be applied while busy; other specification changes
are conservatively treated as potentially task-recreating.

An unsafe update returns an explicit per-service blocked error. Independent services
are still applied, but the deployment fails at apply (`apply_failed`); it is never
reported as successful. There is no deferred queue: arrange an idle window with the
external controller, then explicitly retry deployment. Manual Sync does not force
an external job to restart: an unchanged terminal failed rollout requires external
controller recovery. Docker's current `ForceUpdate` remains preserved.

Convergence for externally managed **replicated** services verifies the requested
specification was accepted, allowing Docker defaults, resolved images, and
runtime-owned replicas/`ForceUpdate`. Stale pre-apply snapshots or an unrelated
scheduler mutation cannot prove application of a different requested spec. Terminal
update failures and stale rolling-update protection remain enforced. Completed jobs
and zero running tasks do not prevent specification acceptance. This is **not a task
health check**: progress explicitly says so, and in-memory waiter/deployer results
identify `SpecificationOnlyServices`. Deployment success means accepted configuration,
not successful execution of a scheduled job. Ordinary replicated and global services
retain their strict task convergence checks.

### Recommended swarm-cronjob configuration

External controllers such as `crazy-max/swarm-cronjob` change replicas and
`ForceUpdate` to execute scheduled jobs. Disable both self-healing and deployment
replica ownership:

```yaml
services:
  scheduled-job:
    image: example/job:1.0.0
    deploy:
      replicas: 0
      restart_policy:
        condition: none
      labels:
        swarm.cronjob.enable: "true"
        swarm.cronjob.schedule: "0 * * * *"
        org.swarm-deploy.service.sync.policy.selfHeal: "false"
        org.swarm-deploy.service.sync.policy.replicasManaged: "false"
```

Setting `selfHeal.replicas=false` remains supported but is redundant when
`replicasManaged=false`. Using only `selfHeal=false` leaves replica drift reporting
and replica management during normal deployments enabled.

The existing “Disable self-healing for scheduled service” recommendation appears
when `swarm.cronjob.enable=true` is set in Compose deploy labels and either effective
self-healing or replica management is enabled. It suggests only settings that still
need changing and disappears when both are disabled. Native Docker job modes alone
do not trigger it. The deployment engine does not interpret scheduler labels.

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
