# Native Compose-to-Docker API converter

The `internal/modules/deployments/deployer/converter` package converts a complete,
prepared `compose.Compose` into final Docker `swarm.ServiceSpec` values and network
specifications. It is the first part of the v0.15.0 migration away from
`docker stack deploy`. It is deliberately **not wired into deployment execution**.

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

The future apply pipeline must reconcile Compose networks and run the existing
config/secret reconciliation **before** final conversion, including on first
deployment. Network reconciliation must also precede Init Jobs that need those
networks. A future integration can reuse the package's network-spec conversion
when adding that reconciliation; this PR introduces no partial-plan API and no
reconciler. After resources exist, the same Convert call produces final specs
without any Deployer enrichment.

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
Docker CLI/Compose library is added to the runtime dependency graph.

## Follow-up to replace Docker CLI

1. Reconcile Compose-owned networks before Init Jobs/final conversion, reusing
   this package's network naming/spec logic. Retain the existing config/secret
   reconciler and ensure its resolved metadata can be reused to avoid rereads.
2. Add service create/update/remove operations through `internal/swarm`, including
   version conflicts and pruning. Consume the final ServiceSpecs as returned.
3. Reuse `internal/registry.AuthManager` for Docker operation options. Registry
   authentication is not a ServiceSpec field; image digest/platform discovery is
   also separate from Compose conversion and remains future work.
4. Wire the API executor into Deployer while retaining InitJobRunner and
   ServiceWaiter, verify real-cluster compatibility, then remove CLI configuration.
