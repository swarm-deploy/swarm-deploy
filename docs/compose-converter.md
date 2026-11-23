# Native Compose-to-Docker API converter

The `internal/modules/deployments/deployer/converter` package converts a complete,
prepared `compose.Compose` into final Docker `swarm.ServiceSpec` values and network
specifications. The deployment pipeline uses this result to reconcile networks,
run Init Jobs against resolved resources, and create or update services through
the Docker API without invoking `docker stack deploy`.

```go
converter := converter.New(swarmService.Networks, swarmService.Configs, swarmService.Secrets)
spec, err := converter.Convert(ctx, stackName, preparedCompose)
```

## Boundaries and input contract

- `internal/compose` continues to own models, parsing and normalization. No Docker
  client or conversion logic is added there.
- `converter` owns application ServiceSpec construction, names, labels, mount
  options, validation and resolution of the IDs referenced by those specs.
- Docker reads use only `internal/swarm.NetworkManager.Get`, `ConfigManager.List`
  and `SecretManager.List`. No new manager methods or client abstraction are needed.
  Configs/secrets are looked up once per resource kind; network lookups are cached
  by name within each conversion. Exact names are checked even when Docker's list
  filters return prefix matches.
- The existing ResourceReconciler still creates configs/secrets. Secret decoding,
  file reads, rotation and external driver configuration remain upstream. The
  prepared definitions' `Name` fields select the exact rotated resources.
- All env files must have been populated and cleared, and source-path preparation
  must already be complete. Relative bind paths are
  rejected rather than resolved against the process working directory.
- The result contains no config/secret payloads or registry credentials. Docker
  lookup errors are replaced with resource-specific diagnostics that cannot echo
  response payloads. Ordinary container environment values are necessarily part
  of the ServiceSpec; callers must not log whole specifications.

## Resources and supported fields

Services support image, scalar/list command and entrypoint, environment, container
and service labels, capabilities, healthchecks (including start interval), stop grace period, ports,
VIP/DNSRR endpoints, network aliases and attachment driver options, mounts,
config/secret files, logging, replicated/global service and job modes, replicas,
placement, CPU/memory limits and reservations, discrete generic reservations,
PID limits, restart policies, and update and rollback policies.

Capabilities are normalized when Compose is parsed and when persisted deployment
snapshots are read, so older deployments are displayed in the same form: names are trimmed, upper-cased
and ordinary names (`A-Z`, `0-9`, `_`) are stored without the `CAP_` prefix; any other
value keeps the exact name Docker receives, so typos such as `CAP_CAP_SYS_ADMIN` are
still rejected by Docker. `RESET` is discarded, `ALL` replaces the rest
of its list, and a capability present in both `cap_add` and `cap_drop` is only added.
Conversion restores the `CAP_` prefix expected by the Docker API. Definitions that
spelled capabilities differently (for example `net_admin` or `CAP_NET_ADMIN`) get a
new effective digest once. That starts a Deployment which leaves ServiceSpecs
unchanged but, like every stack Deployment, runs the stack's Init Jobs.

Commands, entrypoints and healthcheck tests are normalized to exec form while Compose
is loaded (and when persisted deployment snapshots are read), next to capabilities.
Scalar commands are tokenized with the `google/shlex` package; they do not implicitly
invoke a shell. Exec-form arguments retain their boundaries. Scalar healthcheck tests
become `CMD-SHELL` tests. Invalid quoting in a scalar `command` or `entrypoint` is
therefore reported when the file is loaded or linted, as a decode error at
`services.<name>.command` or `services.<name>.entrypoint`, before any preparation or
deployment starts; the error does not include the command text. The deployment
snapshot YAML shows these fields in list form.

Names use `stack_alias` unless overridden by `name` or an external definition.
Services, containers, owned networks and owned volumes carry
`com.docker.stack.namespace`; services also carry `com.docker.stack.image`.
Application network aliases include the Compose service name. Explicit replica
zero and parallelism zero are preserved. Unspecified restart, update, rollback
and replica settings are left to Docker's defaults; a present update/rollback
configuration defaults its parallelism to one.

Networks include only Compose networks referenced by an application service or
Init Job, including the implicit default when referenced. Network specifications
preserve explicit names, external ownership, internal/attachable flags,
driver/options, IPAM settings and labels.
Every network must already exist with a nonempty ID and Swarm scope. Service
attachments use its resolved ID. This is separate from the GitOps reconciler for
networks declared in swarm-deploy's own configuration.

Named volumes become node-created mounts, with scoped/explicit/external names,
labels and driver options. Anonymous volumes, absolute bind mounts, tmpfs,
read-only mounts, consistency, bind propagation, volume nocopy/subpath and tmpfs
size/mode are supported. External volumes carry no creation options and are not
inspected on the manager: ordinary Swarm volumes belong to individual nodes.

Config and secret references contain the actual ID/name, target, UID/GID and
file mode (default `0444`, UID/GID `0`). Short-form and target-less config targets
are `/<alias>`; short-form and target-less secrets use the alias, which Docker
mounts under its secret directory. These rules and Docker's defaults are defined
once on `compose.ObjectRef` (`ServiceConfig`, `ServiceSecret`, `InitJobSecret`,
`FileOwnership`) and shared by conversion, InitJobRunner and the
effective digest. References are resolved while Compose is loaded, so the short
form is only Compose syntax: loaded definitions and snapshots contain explicit
targets.

Init Jobs are excluded from application service conversion. Their declared
networks are included and undefined dependencies are rejected. InitJobRunner
owns the temporary services and job lifecycle.

Init Jobs mount their own config and secret references and those inherited from
the service. All configs default to `/<alias>`, with their configured UID, GID
and mode. An explicit target sets the mount path. Earlier releases mounted
short-form configs in Init Jobs at `/run/secrets/<alias>` and attached long-form
configs without a target without a file. Set `target: /run/secrets/<alias>`
explicitly if a job still needs that path.

Init Job secret targets resolve to `/run/secrets/<source>` when empty and under
`/run/secrets/` when relative. Each source is attached once per mount path.
For example, config targets `app` and `/app` refer to the same file. When a job
and its service declare the same source at the same path, the job's UID, GID
and mode apply. Duplicate declarations within a job must agree on UID, GID
and mode. Compose validation rejects conflicting declarations.

Earlier releases ignored user environment variables, labels and resource names
that matched internal model fields such as `Keys`, `env_file` or `Map`. Stacks
with such definitions get a new effective digest once after upgrading, which
starts one Deployment that runs their Init Jobs.

The effective digest (`compose.Canonicalize`) applies the same typed normalization
to definitions built in code and sorts order-insensitive lists (ports, mounts,
networks, config and secret references, capabilities) by their JSON encoding with
sorted keys; Init Jobs keep their order. Parser metadata, such as the source order
of environment variables, the indexes of mounts and networks, or the form of a
command already converted to exec form, is not serialized. The digest keeps the
command encoding of earlier releases (`{"Args": [...], "IsList": true}`) so
existing baselines stay valid; deployment snapshots use the current encoding.

Map-derived collections and service/network results have deterministic ordering.
Conversion never mutates its input or shares mutable input maps, slices or
pointers with the returned specs.

## Missing resources and first deployment

`Convert` is read-only and returns an error and a nil result when a required
network, config or secret is missing. It does not substitute names for IDs,
select an older rotated resource, or return partial ServiceSpecs.

`Validate` checks all locally decidable conversion rules without Docker reads,
using the same service conversion functions as `Convert`. Invalid field values,
resource references and incompatible options therefore fail before any resources
are changed.

The apply pipeline validates the stack, uses `PlanNetworks` to reconcile
Compose-owned networks, and runs the existing config/secret reconciliation
**before** final conversion, including on first deployment. Network reconciliation
also precedes Init Jobs, which receive the reconciled network IDs. After resources
exist, the same `Convert` call produces final specs without Deployer enrichment.
Metadata lookup errors are sanitized without losing their temporary/deadline
retry classification.

## Validation and limitations

Unknown behavior-bearing fields retained in `Extra` are rejected at the affected
resource/field, including nested deployment, healthcheck, logging, network and
mount options. Compose `version` metadata and `x-*` extensions are allowed.
Init Job execution options remain owned by their existing preparation/runner.

The converter currently rejects, rather than approximates:

- Static service IPs, IPv6 network options and arbitrary local-scope networks
  (predefined external `host`/`none` are supported). Network IPAM driver,
  options, subnet, IP range, gateway and auxiliary addresses are supported.
- Host-IP/app-protocol port settings, invalid protocols/modes and ingress ports
  with DNSRR.
- PID reservations and unmodeled resource options such as device reservations
  or generic resource limits (discrete generic reservations are supported).
- Bind host-path creation, relative bind paths, image/cluster/npipe mounts,
  SELinux short mount modes and other unconverted mount options.
- Unmodeled service fields, including `build`. Only explicitly documented
  compatibility options are ignored: service-level `privileged` and `depends_on`
  are accepted with a non-fatal structured warning and have no
  effect on Docker service specifications. Service-level `restart` is a fallback for
  `deploy.restart_policy`, following Docker CLI v28.3.3 semantics.
  Dependency ordering is not implemented.
- CPU quantities beyond signed nanocpu range/precision, invalid durations and
  sizes, nonnumeric file owners, invalid file modes and conflicting options.

External network/volume definitions cannot specify creation options. Config and
secret creation-only fields are owned by the existing reconciler, not copied
into the conversion result. Manager lookups are not an atomic Docker snapshot:
resource deletion during a future apply must still be handled by its executor.

The native converter uses Docker CLI v28.3.3's
[service conversion](https://github.com/docker/cli/blob/v28.3.3/cli/compose/convert/service.go),
[volume conversion](https://github.com/docker/cli/blob/v28.3.3/cli/compose/convert/volume.go)
and [stack labels/network conversion](https://github.com/docker/cli/blob/v28.3.3/cli/compose/convert/compose.go)
as references. The differential tests use the version pinned in
[`tools/compatibility/go.mod`](../tools/compatibility/go.mod) and require no
Docker executable or cluster.

## Source value handling

Repository-controlled Compose values are literal. `FileLoader.Load` does not
interpolate `$VAR`, `${VAR}`, defaults or required-variable expressions, and
does not read the controller process environment. Service `environment` lists
require explicit `KEY=value` entries; null mappings become empty strings.
`env_file` values remain literal and require explicit `KEY=value` entries.

The effective definition is passed directly to the native Docker API converter,
so full-stack and single-service reconciliation use the same values. Source
digests continue to hash repository files rather than process environment values.

## Native apply behavior

Deployment-owned networks are created or validated before Init Jobs. External
networks must already exist and are never modified. Named volumes remain mount
specifications in each service, so Swarm workers create ordinary volumes locally;
the deployment path never creates, migrates, prunes, or deletes volumes.

Services are inspected by exact Docker name, ownership-checked, and compared after
normalizing Docker defaults, empty collections, registry-resolved image digests,
and registry-populated platforms. Changed services use optimistic versioned
updates with bounded conflict retries. Partial application is retained for the
next reconciliation rather than rolled back.

Registry credentials come from `internal/registry.AuthManager` and are passed only
in Docker create/update options. Docker's `QueryRegistry` behavior performs digest
and platform resolution. The deployment code does not implement separate manifest
resolution or platform selection, so registry availability and daemon resolution
behavior remain operational dependencies. When an inspected service retains the
same `com.docker.stack.image` tag, its daemon-resolved digest/platform metadata is
treated as equivalent to the original tag to avoid needless rollouts. Consequently,
moving a registry tag without a Compose/image-policy change is not itself a signal
to refresh that service in this deployment path.

The full-stack pipeline is: validate desired state → reconcile networks → reconcile
configs/secrets → convert Compose to Docker API specs → run Init Jobs → capture
service baseline → apply services through Docker API → wait for convergence.
Single-service remediation uses the same native components and projects the target
service and its referenced networks, configs, secrets and volumes without running Init Jobs.

The runtime image contains no Docker CLI binary. Repository Compose values are not processed by Docker CLI interpolation. Registry configuration still uses `config.json`
and `DOCKER_AUTH_CONFIG` (see [registry authentication](./registry-authentication.md)).
