# Native deployment compatibility

Reference: `swarm-deploy-docker-cli-comparison.txt`, audit of dev
`d744a869ca03d47ddc81233bad87667672d353d9` against Docker CLI/SDK v28.3.3.
The dev head was still this commit when this work started. The confirmed defects
were all present; the CLI infrastructure remains removed.

## Addressed findings

| Audit | Result |
| --- | --- |
| 1: command/entrypoint | Use the same `google/shlex` parser as CLI. Preserve empty arguments, quotes, escapes and comments; list arguments are not tokenized. No repeated interpolation. |
| 2: ForceUpdate | Preserve the current counter for comparison and every submitted update, including optimistic-conflict retries. |
| 3: image drift | Stack image labels alone no longer establish equivalence. Check normalized repository, tag and explicit digest. |
| 4: convergence retry | Verify services actually created or updated by the native applier. Unrelated unchanged services retain their historical update status without blocking an apply. Unchanged known failed deployments remain failed until explicit retry; retries restart terminal failed updates before verification. |
| 5: Compose fields | Add the service options below, job modes, discrete generic reservations and network IPAM. Fix the examples without stripping unsupported fields in tests. |
| 6: registry resolution | Consistent `changed` policy, preserving the resolved image/platforms on unrelated changes. Registry auth still comes from AuthManager; SDK handles resolution. |
| 7: networks | Plan only referenced networks, including Init Job references and implicit default. Reuse existing stack-owned immutable networks. Accept predefined external host/none networks; arbitrary external local bridge networks remain invalid. |
| 8: warnings | Log create/update response warnings, including SDK registry-resolution warnings, with the existing bounded secret-masking diagnostic function. Warnings are not failures. |
| 9: environment/ports | Resolve bare/null keys during source loading; persist effective values. Preserve explicit empty strings and literal `"null"`. Expand short port ranges with the same nat parser used by CLI. |

## Supported options added

`user`, `working_dir`, `hostname`, `dns`, `dns_search`, `extra_hosts`, `read_only`,
`init`, `tty`, `stdin_open`, `stop_signal`, `sysctls`, `ulimits`, service-level
`tmpfs`, `isolation`, `credential_spec`, `oom_score_adj`,
`deploy.resources.reservations.generic_resources`, `deploy.mode: replicated-job`
and `deploy.mode: global-job`.

Scalar/list DNS and tmpfs, mapping/list sysctls and extra_hosts, scalar and
soft/hard ulimits are accepted. Host list order and duplicate hostnames survive.
Service-level tmpfs supports targets and `size`, `mode`, `ro`, `rw` options;
other short tmpfs options produce errors. Existing long-syntax tmpfs mounts
remain supported. Credential specs accept exactly one of `config`, `file` or
`registry`; config aliases use the reconciled (including rotated) config identity
and a runtime config reference, also for single-service deployments.

IPAM includes driver, options, subnet, IP range, gateway and auxiliary addresses.
Existing networks retain their actual settings and ID: changed immutable settings
require an operator-managed migration, and do not cause automatic recreation.
Network identity, Swarm scope and stack ownership are still checked.

Replicated jobs use Compose replicas as both MaxConcurrent and TotalCompletions,
as CLI does, including zero. Job convergence uses Docker's current-iteration
completed task counts and waits for scheduler/job iteration propagation. A global
job with no observable target tasks remains pending until the existing timeout,
like an uninitialized global-service scheduler, rather than reporting success
immediately on API acceptance.

## Ignored and unsupported options

Service-level `privileged`, `depends_on` (short or long form) and `restart` are
accepted and ignored, matching the previous Swarm deployment workflow. Each
present field produces one structured warning with its full Compose path during
pipeline validation. They do not change the generated ServiceSpec or effective
desired-state comparison. No dependency ordering or alternative privileged mode
is implemented. Configure Swarm restarts through `deploy.restart_policy`.

`build` and other unsupported Compose options continue to fail validation.

## Regression checks

Desired-state comparison tokenizes scalar commands/entrypoints with the same
parser as deployment conversion and normalizes config/secret reference syntax
and defaults. Parser metadata remains available for loading and conversion but
is not exposed as `is_list` or `short_form` changes, including when comparing
against an older persisted baseline. Executable values remain masked.

The native applier compares unordered environment entries (with unique names),
capabilities, placement constraints and network attachments/aliases independently
of order. Docker defaults and runtime ForceUpdate remain normalized. Command
arguments, DNS order and duplicate environment names retain their ordering.

The waiter checks actual applied services and accepted updates still in progress.
Explicit retries also reverify unchanged accepted specifications. Terminal errors
include the latest failed/rejected task
ID, state, runtime error and exit code where available. A task lookup failure
does not replace or suppress an already observed terminal update failure.

## Image policy

On create or a changed image reference, use the Docker SDK's registry resolution
with existing AuthManager credentials. Digest-pinned references are already
resolved and do not need a registry query.

For an unchanged Compose image label, reuse the current actual reference and
SDK-generated platform metadata only if it is a valid resolution of that desired
reference. Repository and tag must match (including normalized Docker Hub/default
latest spelling); an explicitly desired digest must match exactly. Copy the
resolved image/platforms into the actual update request, not only comparison
copies. Recompute this decision after every version conflict.

Thus moving a mutable tag does not change the running image on a repeated deploy
or an unrelated env/configuration change. Change the desired image reference or
pin a new digest to roll out a new image. A different repository/tag or a digest
that contradicts a pinned desired digest is real drift and is corrected.

A tag alone cannot independently prove that an externally substituted digest
under the *same repository and tag* differs from its previous registry resolution.
That value remains a valid resolution under this policy. Pin the desired digest
when enforcing an exact image identity is required. No new image baseline store,
registry polling or custom manifest resolver is introduced.

## Intentional differences and audit corrections

- Environment lookup happens once in FileLoader during source preparation, before
  GitOps persists its effective snapshot. Bare or YAML-null names capture the
  process value if available. Unset names remain explicit unset entries (`NAME`,
  not `NAME=null` or `NAME=`) through JSON/YAML, overriding env_file values. The
  converter and Parse do not read the process environment. Explicit empty values
  remain empty; CLI v28.3.3 resolves those from its environment. Init Jobs retain
  their existing environment/interpolation semantics.
- Matching short port ranges and target-only ranges are expanded with Docker's
  nat parser. Invalid or mismatched ranges fail. Host-IP publishing is still
  unsupported by Swarm and produces the existing converter error.
- Independent tests show that CLI v28.3.3 loads service-level tmpfs but its service
  converter omits those mounts. Native intentionally implements them because the
  Swarm API supports tmpfs mounts. CLI's v3.8 schema rejects `oom_score_adj`, even
  though its converter and SDK have the field. Native supports the API field.
- Warnings are available in existing structured deployment-context logs, without
  a new persistence model. They are not separately stored in deployment records.
- Config/secret reconciliation and rotation, Init Jobs, controller lifecycle,
  events, pruning and volumes retain swarm-deploy behavior. No database migration,
  asynchronous execution or new orchestration layer is involved.

## Verification

`make check` runs the repository linter, all Go tests, and differential tests in
`tools/compatibility`. That separate module pins Docker CLI v28.3.3; it adds no
production loader/converter or CLI executable dependency. Fixtures compare actual
converted API specs for commands, service fields, ports, jobs, credentials and
resources, and explicitly test environment and CLI field limitations.

`SWARM_DEPLOY_E2E=1 go test -count=1 -timeout=10m -v ./e2e` uses the existing real
Swarm infrastructure. The extended stack scenario checks unchanged service/task
identity, external `docker service update --force`, a failed/paused rollout and
explicit retry with a new task generation, recovery, representative container options, immutable
network reuse, and unused external networks. Existing rotation tests remain.
Set `SWARM_DEPLOY_E2E_REGISTRY` to a pushable registry prefix reachable by Swarm
nodes to also push two BusyBox versions under a mutable tag, verify unrelated
changes preserve its digest, and deploy a changed image reference.

Live Swarm and registry scenarios could not run in the implementation environment:
there is no Docker executable or Docker socket. E2E compiles and skips without
its opt-in flag. Windows credential/isolation behavior also requires a compatible
Windows Swarm node and has conversion coverage only.

## Convergence failures and retries

A terminal Swarm update failure (paused, failed, or rollback) produces a failed
Attempt at verification with `convergence_failed`. Docker API observation errors,
timeouts, and cancellations retain an interrupted, `verification_unknown` outcome.
Diagnostics retain the service, update state, and available task ID/exit code,
with existing sanitization.

The native applier supplies the exact created/updated service names to the waiter.
The waiter waits for the applied service version and a fresh rollout status before
accepting running tasks. Old running tasks do not satisfy a new task generation.
The caller supplies the complete verification target set; the waiter returns
unresolved targets independently of optional progress callbacks. In-memory
results explicitly distinguish known obligations from an unknown outcome.
Legacy null/array metadata remains compatible at the persistence boundary.
Unrelated unchanged services, including historical paused updates, are excluded.
Accepted updates still in progress are verified even when their specs are unchanged.
With no mutations or active updates, stack apply is a no-op rather than a health check.

Periodic reconciliation does not retry an unchanged non-retryable failure,
including `convergence_failed` and `policy_rejected`. Transient failures (such
as interrupted verification or explicitly classified temporary infrastructure
errors) are retried indefinitely with exponential delays of 1, 2, 4, ...
minutes, capped at 1 hour. The retry classification, persisted Attempt count,
and finish time preserve the delay across restarts. Unclassified apply, Init Job,
and cleanup failures are not assumed to be temporary.
Deferred cycles do not self-heal, prune, overwrite the GitOps failure projection,
or publish deployment completion events. Alert occurrences therefore count real
failed Attempts rather than scheduler ticks.

A new effective desired digest starts a new Deployment with fresh retry state. Manual Sync
retries a failed/interrupted deployment regardless of automatic retry backoff. During
retry, terminal failed services receive a new `ForceUpdate` generation, including
when the accepted ServiceSpec is already equal to desired. Retries reverify accepted unchanged specifications too, so interrupted observation
cannot turn an unfinished rollout into a successful no-op. Healthy unchanged
services keep their tasks. No automatic attempt limit, new configuration, or database schema is required.

## Externally managed replicas

Set the service deploy label `org.swarm-deploy.service.sync.policy.replicasManaged`
to `"false"` for external replica controllers of **replicated services only**
(default: `true`). Global and native job modes explicitly reject `false`; they keep
normal deployment/convergence behavior with the default policy. Native updates then
ignore replica-only changes, preserve live replicas and `ForceUpdate`, and rebase
those values on every optimistic retry. Replica drift is also excluded independently
of `selfHeal.replicas`; `selfHeal` still controls automatic repairs separately.

Replica ownership alone does not guarantee safe immediate task-template updates.
Task-recreating changes require zero replicas and no active tasks. A completed job
with nonzero configured replicas is not idle. Unsafe changes produce per-service
blocked errors while independent services continue applying; the deployment fails
at apply rather than claiming success. Retry explicitly during a scheduler-coordinated
idle window. Retrying an independent failed service leaves an unchanged external
job untouched, even if it has a historical terminal execution status. Existing
unresolved-service obligations still require verification and cannot become
successful no-ops. No retry forces a new external job generation.

For externally managed replicated services, verification proves expected specification
acceptance rather than running-task health. It still rejects stale snapshots,
unapplied specs and old rolling update status. The actual preserved force generation
and pre-write timestamp are carried from the applier (including conflict retries):
fresh terminal failures in that generation still fail verification. Later external
`ForceUpdate` activity cannot fail a matching, fresh managed specification, and an
unrelated scheduler update never proves acceptance of an unapplied spec. Progress and
returned `SpecificationOnlyServices` identify this limited verification; completion or
absence of job tasks is legitimate. Ordinary replicated/global verification is unchanged.
See [replica ownership and swarm-cronjob configuration](drift.md#replica-ownership-during-deployments)
for the policy relationships, recommended labels, and initial creation behavior.
