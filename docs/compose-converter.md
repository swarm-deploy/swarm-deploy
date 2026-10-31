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
- All env files must have been populated and cleared, and interpolation and
  source-path preparation must already be complete. Relative bind paths are
  rejected rather than resolved against the process working directory.
- The result contains no config/secret payloads or registry credentials. Docker
  lookup errors are replaced with resource-specific diagnostics that cannot echo
  response payloads. Ordinary container environment values are necessarily part
  of the ServiceSpec; callers must not log whole specifications.

## Resources and supported fields

Services support image, scalar/list command and entrypoint, environment, container
and service labels, capabilities, healthchecks (including start interval), stop grace period, ports,
VIP/DNSRR endpoints, network aliases and attachment driver options, mounts,
config/secret files, logging, replicated/global modes, replicas, placement,
CPU/memory limits and reservations, PID limits, restart policies, and update and
rollback policies.

Scalar commands are tokenized with the existing dependency graph's `go-shlex`
package; they do not implicitly invoke a shell. Exec-form arguments retain their
boundaries. Scalar healthcheck tests become `CMD-SHELL` tests.

Names use `stack_alias` unless overridden by `name` or an external definition.
Services, containers, owned networks and owned volumes carry
`com.docker.stack.namespace`; services also carry `com.docker.stack.image`.
Application network aliases include the Compose service name. Explicit replica
zero and parallelism zero are preserved. Unspecified restart, update, rollback
and replica settings are left to Docker's defaults; a present update/rollback
configuration defaults its parallelism to one.

Networks include all declared networks and the implicit default when referenced
by an application service or Init Job. Network specifications preserve explicit
names, external ownership, internal/attachable flags, driver/options and labels.
Every network must already exist with a nonempty ID and Swarm scope. Service
attachments use its resolved ID. This is separate from the GitOps reconciler for
networks declared in swarm-deploy's own configuration.

Named volumes become node-created mounts, with scoped/explicit/external names,
labels and driver options. Anonymous volumes, absolute bind mounts, tmpfs,
read-only mounts, consistency, bind propagation, volume nocopy/subpath and tmpfs
size/mode are supported. External volumes carry no creation options and are not
inspected on the manager: ordinary Swarm volumes belong to individual nodes.

Config and secret references contain the actual ID/name, target, UID/GID and
file mode (default `0444`, UID/GID `0`). Short-form config references retain their syntax across JSON snapshots, so their
targets are unchanged after serialization. Short-form config targets are `/<alias>`;
short-form secrets use the alias under Docker's secret directory. This accounts
for the existing shared ObjectRef parser's secret-style short-form target without
changing its behavior for existing consumers.

Init Jobs are excluded from application service conversion. Their declared
networks are included and undefined dependencies are rejected. InitJobRunner,
its temporary services and its lifecycle remain unchanged.

Map-derived collections and service/network results have deterministic ordering.
Conversion never mutates its input or shares mutable input maps, slices or
pointers with the returned specs.

## Missing resources and first deployment

`Convert` is read-only and returns an error and a nil result when a required
network, config or secret is missing. It does not substitute names for IDs,
select an older rotated resource, or return partial ServiceSpecs.

The apply pipeline validates the stack, uses `PlanNetworks` to reconcile
Compose-owned networks, and runs the existing config/secret reconciliation
**before** final conversion, including on first deployment. Network reconciliation
also precedes Init Jobs, which receive the reconciled network IDs. After resources
exist, the same `Convert` call produces final specs without Deployer enrichment.

## Validation and limitations

Unknown behavior-bearing fields retained in `Extra` are rejected at the affected
resource/field, including nested deployment, healthcheck, logging, network and
mount options. Compose `version` metadata and `x-*` extensions are allowed.
Init Job execution options remain owned by their existing preparation/runner.

The converter currently rejects, rather than approximates:

- IPAM, static service IPs, IPv6 network options and local-scope networks.
- Host-IP/app-protocol port settings, invalid protocols/modes and ingress ports
  with DNSRR.
- Job deployment modes, PID reservations and unmodeled resource options such as
  device reservations or generic resources.
- Bind host-path creation, relative bind paths, image/cluster/npipe mounts,
  SELinux short mount modes and other unconverted mount options.
- Unmodeled service fields such as `privileged`, `build`, `depends_on`, hostname,
  working directory, DNS or stop-signal configuration. They must be added
  explicitly before migrating stacks that use them.
- CPU quantities beyond signed nanocpu range/precision, invalid durations and
  sizes, nonnumeric file owners, invalid file modes and conflicting options.

External network/volume definitions cannot specify creation options. Config and
secret creation-only fields are owned by the existing reconciler, not copied
into the conversion result. Manager lookups are not an atomic Docker snapshot:
resource deletion during a future apply must still be handled by its executor.

Compatibility assertions use Docker CLI v28.3.3's
[service conversion](https://github.com/docker/cli/blob/v28.3.3/cli/compose/convert/service.go),
[volume conversion](https://github.com/docker/cli/blob/v28.3.3/cli/compose/convert/volume.go)
and [stack labels/network conversion](https://github.com/docker/cli/blob/v28.3.3/cli/compose/convert/compose.go)
as references. Unit tests do not execute Docker CLI or require a cluster. No
complete Docker CLI/Compose loader is added to the runtime dependency graph.

## Source interpolation

`internal/compose` interpolates source YAML values once, before model decoding,
resource payload reads and GitOps image-policy checks. It uses Docker CLI
v28.3.3's `cli/compose/template` and `cli/compose/interpolation` packages directly;
the Converter receives effective values and never interpolates or unescapes them.
Full-stack deployment and single-service remediation (including JSON snapshots)
therefore use the same values, independently of subsequent process-environment changes.

The supported stack-CLI expressions are `$VAR`, `${VAR}`, `${VAR:-default}`,
`${VAR-default}`, `${VAR:?error}`, `${VAR?error}`, and `$$`. Unset simple variables
become empty strings. Defaults distinguish unset from empty variables. Inserted
values and defaults are not recursively expanded; nested expansions and newer
Compose operators such as `${VAR:+value}` are not added. Mapping keys are literal.
Scalar commands are interpolated before shell tokenization; list argument
boundaries remain intact, including `$$(...)` shell commands.

The interpolation environment is the controller process environment, as with the
previous stack CLI. Service `environment` and `env_file` are not interpolation
sources, and `.env` files are not loaded automatically. Env-file payloads remain
literal, with later files taking precedence and explicit `environment` overriding
them (including empty values). Config and secret payloads are never interpolated.
The controller's `x-init-deploy-jobs` extension retains its existing literal
command/environment semantics; those jobs have always executed directly rather
than through the stack CLI, so their `$()` shell expressions remain untouched.

Source digests still hash source YAML and referenced payloads, not process
environment values. Changing only the controller environment is not itself a
source-change trigger. Interpolation diagnostics omit input values and custom
required-variable messages to keep them out of logs and traces.

## Native apply behavior and remaining cleanup

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

The legacy Docker binary configuration and inactive CLI helper remain temporarily
for the planned PR 3 cleanup. They are not reachable from the active full-stack or
single-service deployment pipelines.
