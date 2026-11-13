# Reconciliation

swarm-deploy continuously reconciles the desired state from Git with the Docker Swarm cluster.

A reconciliation run loads the configured Compose file, computes its effective desired state, applies swarm-deploy-specific mutations, deploys when necessary, reads the live cluster state, prunes managed orphaned services, and analyzes drift.

## Reconciliation pipeline

The current pipeline is:

1. Load Compose and its referenced files from the reconciliation commit without interpolating repository-controlled values.
2. [Check image policy when at least one image rule is enabled](./image-policy.md).
3. Populate service environment from `env_file`.
4. [Inject Downward metadata when enabled](./downward.md).
5. Add the swarm-deploy managed label.
6. [Rotate configs and secrets when enabled](./secrets-rotation.md).
7. Submit the effective in-memory desired state to Deployments when the source digest changed or the desired state was mutated. Deployments validates desired state, reconciles networks, reconciles configs/secrets, converts Compose to Docker API specs, runs Init Jobs, captures the service baseline, applies services through the Docker API, and waits for convergence.
8. Load live Swarm state.
9. [Prune managed orphaned services when applicable](./prune.md).
10. [Analyze drift](./drift.md).
11. Reapply drifted services when their [self-heal policy](./drift.md#self-heal-policy) is enabled, without Init Jobs.
12. Reload stack services once and verify every attempted repair against actual Swarm state.

Some steps are conditional. For example, the image policy step is absent when every image rule is disabled, and environment population runs only when at least one desired service declares `env_file`.

## Pause Sync policy

GitOps owns one persistent Pause Sync policy per stack. While active, it preserves every service in that
stack. Pause is separate from the existing `Synced` and `OutOfSync` status.

Pause records contain only the GitOps stack name and an optional absolute expiration. They do not copy
service specifications or runtime state. Expired records are removed transactionally with their expiration events at
the beginning of the next ordinary GitOps sync; no expiration scheduler or worker runs between syncs. An
operation already running when a pause is created is not canceled.

For each stack reconciliation, GitOps calculates the service names that its current pause policy must
preserve. It passes those names to Deployments through the neutral `PreservedServices` execution contract.
Deployments does not read or interpret Pause Sync policy: it excludes preserved services from service apply,
convergence waiting, service-level mutations, and pruning, while continuing to deploy other services in the
stack. The no-Attempt pruning and GitOps self-heal paths use the same set. This also keeps a paused live
service running when it has been removed from Git until its effective pause is removed.

Each Deployment and Attempt records the service names intentionally skipped for that execution. The paused
count is calculated from the active stack pause and GitOps-managed services rather than persisted as a second
policy projection. Realtime and the assistant can enable the stack pause before scaling one of its services;
scaling starts immediately after the pause is persisted and retries only scaling if that second step fails.

## Sync runs and the stack queue

A sync run (startup, Git poll with a new revision, webhook, manual sync or the periodic `gitops.sync.interval`) does
only the work that needs a stable Git working tree:

1. pull Git (or read `HEAD` for interval runs);
2. reload `stacks` and `networks` configuration;
3. reconcile managed networks;
4. replace the work waiting in the stack queue with every configured stack at the run's revision, changed
   stacks first.

The run then finishes without waiting for deployments. Every stack operation reads its Compose file and the
`env_file`, config and secret files it references from the commit of its revision, never from the working
tree, so a later Git pull can never change what a queued or running operation applies. Symlinks inside the
repository, both for directories and files, are resolved within the same revision. Absolute symlink targets
and targets outside the repository are rejected, and cycles fail after 40 hops.

The stack queue runs at most `gitops.sync.parallelism` stacks at the same time (default `1`) and never runs two
operations for the same stack at once. By default stacks are processed sequentially; set `parallelism`
to a higher value explicitly to allow independent stacks to run simultaneously. A slow stack does not
hold up Git polling or later sync runs, but with `parallelism: 1` it still blocks other stack operations:

```yaml
gitops:
  sync:
    # Maximum number of stacks reconciled at the same time.
    parallelism: 1
```

The queue is keyed by stack name, like a Kubernetes work queue, and every successful sync run atomically
replaces the whole set of waiting reconciliations with every configured stack at its revision. As a result:

- while a stack is busy, only the latest revision waits for it and runs as soon as the current operation
  finishes; a manual request stays manual when it is superseded by a later run;
- waiting work of stacks that are no longer configured disappears, so a removed or renamed stack is never
  deployed from an older revision after the new configuration took effect;
- a stack never runs with a specification or revision that a newer successful run has already replaced;
- stacks changed in Git run first, and a waiting stack keeps its changed mark until it runs, so a later run
  without a diff (such as the periodic one) does not push it back;
- within the changed and the unchanged group the queue is first-in, first-out: a waiting stack keeps its
  place across runs, and a stack that already had its turn is queued behind the stacks still waiting. A pass
  over all stacks that takes longer than `gitops.sync.interval` therefore never starves the stacks at the end of the
  configuration;
- when a run fails after the new configuration was accepted (for example, while reconciling networks), all
  waiting work is dropped instead of running under that configuration; the next successful run queues every
  stack again, preserving the changed priority of both pending work and changes detected in the failed run.
- a run whose revision and stack specification match the operation already running for a stack does not
  schedule another reconciliation, and drops any work still waiting for that stack: for example, a revision
  or specification that was reverted while it waited is never applied. A manual request (including one
  that was waiting) still runs again after the current operation; a new revision or updated specification
  waits behind it.

Operations that are already running are never interrupted and finish within their normal lifecycle.
Stack-operation traces are independent spans linked to their dispatch span, not children of a dispatch
span that may have ended before the work started.
Periodic rotated-resource cleanup is queued only for idle stacks, so it never overlaps a reconciliation of the
same stack, and a later run supersedes a cleanup that is still waiting. Cleanup uses the stacks and revision of
the latest successful dispatch, not the Git `HEAD` or a configuration accepted by a run that failed afterwards.

With the default `parallelism: 1`, stack operations run one by one in dispatch order. Unlike the former
fully synchronous run, Git polling, configuration reload and later runs can proceed while an earlier stack
is still running; a newer revision may supersede pending work. Setting parallelism to one does not
restore the old whole-run barrier.

**Compatibility / monitoring change:** the internal successful sync-run result means that Git, configuration,
network reconciliation and queue dispatch succeeded (a run during shutdown, whose stacks the stopped queue
no longer accepts, is reported as `error`), **not** that any stack deployed successfully.
The `GET /api/v1/stacks` response exposes this input-side state as a compact `sync` object:

- `last_poll` is present only in `pull` and `hybrid` modes and reports the latest polling attempt as
  `updated`, `no_change`, or `error` with its sanitized diagnostic;
- `last_webhook` is present only when webhooks are enabled and reports the latest retained
  `WebhookReceived` event. Its `queued` value means accepted for reconciliation scheduling, not deployed;
- `last_revision` is the full revision most recently dispatched to the stack queue.

An enabled status source with no recorded data is represented by an empty object. An unavailable revision is
omitted. Poll completion, webhook receipt, and a dispatched revision are not proof that any stack deployment
succeeded. A run that fails before dispatch (configuration reload, network reconciliation or a stopped queue)
keeps the previous `last_revision`; the failed revision appears only in the logs of that run.
`partial_error` is no longer emitted. Dashboards and alerts using
`swarm_deploy_sync_runs_total{result="partial_error"}` must instead monitor stack operation failures via
`swarm_deploy_sync_stack_operations_total{operation="reconcile",result="error"}` (counter increase),
and inspect per-stack `last_error`, deployment records and the stack status. Overview displays the compact
poll, webhook, and dispatched-revision status bar separately from deployment results. These changes affect monitoring even
when `parallelism: 1`.

The Git repository serializes reads and pulls for go-git safety. Git pulls are bounded by
`gitops.git.pull.timeout` (default `2m`), including time spent waiting for the repository lock (a pull that times
out while waiting reports `Timed out waiting for access to the local Git repository`);
revision readers can abandon the lock wait
when their context is canceled. A pull in progress can delay reads for up to the configured timeout, but cannot
block them indefinitely due solely to a stalled remote.

Queue state is exported as
`swarm_deploy_sync_stack_queue_pending` and `swarm_deploy_sync_stack_queue_running`, and every stack operation
is counted in `swarm_deploy_sync_stack_operations_total` and timed in
`swarm_deploy_sync_stack_operation_duration_seconds`.

On shutdown, operations that have not started are dropped and running ones get the graceful shutdown window to
finish; the next start reconciles every stack again.

## Source digest

swarm-deploy calculates a source digest for each stack. The digest includes:

- the Compose file content;
- referenced config file content;
- referenced secret file content;
- normalized key/value variables loaded from referenced `env_file` files.

A change to a referenced file therefore triggers reconciliation even when the Compose YAML itself did not change.

The source digest is calculated from the source definition before swarm-deploy mutates the desired state.

Compose files and their `env_file`, config, and secret sources must be committed to Git. Relative paths and symlinks can resolve anywhere within the same commit. Absolute paths, absolute symlink targets, and paths that leave the repository are rejected before deployment.

## `env_file` normalization

During reconciliation, swarm-deploy expands service `env_file` entries into the service `environment` map before the remaining mutation steps run.

Given:

```yaml
services:
  api:
    env_file:
      - default.env
      - production.env
    environment:
      LOG_LEVEL: debug
```

the files are applied in declaration order. Values from later files override values from earlier files, then explicit values from `environment` override values resolved from the files.

For one file, the scalar shorthand `env_file: production.env` is also supported.

After resolution, `env_file` is removed from the effective desired state. Downstream reconciliation therefore operates on a self-contained service environment.

Values from env files are treated literally. swarm-deploy does not perform interpolation, substitution, quote removal, or escaping while populating `environment`.

### Bare keys are rejected

Unlike Docker CLI env-file handling, swarm-deploy does not allow a key without an explicit `=`.

Allowed:

```dotenv
FOO=bar
EMPTY=
```

Rejected:

```dotenv
TOKEN
```

This behavior is intentional. A bare key in Docker CLI can read the value of the same variable from the environment of the process running the CLI. Allowing this in swarm-deploy would let repository content reference environment variables from the swarm-deploy controller process.

The reconciliation boundary therefore enforces the following invariant:

> Repository-controlled Compose content, including `env_file`, must never read values from the swarm-deploy process environment.

If a bare key is present, reconciliation fails with an error instead of resolving the value from the controller environment. The same rule applies to service `environment` lists. Null mapping values become empty strings, and `$VAR` / `${VAR}` expressions remain literal.

## Desired-state mutations

swarm-deploy may mutate the loaded source Compose before deployment. Today this includes:

- resolving `env_file` into `environment`;
- injecting Downward metadata;
- adding the managed-service label;
- rotating config and secret object names.

The source Compose in Git is the declarative input. Preparation produces an effective
in-memory definition, which both stack deployments and single-service remediation
submit directly to Deployments. No intermediate Compose file is written or read.
Execution traces identify the original source Compose path. Legacy rendered files
from older versions are removed after the controller acquires ownership.

## Configuration compatibility

Deployments uses the Docker API. Set `deployments.pollInterval` for Init Job and service convergence polling (default `2s`) and `deployments.initJobs.maxDuration` for the Init Job execution limit (default `10m`). The runtime image includes CA certificates and timezone data.
