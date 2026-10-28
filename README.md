# swarm-deploy

![Docker Pulls](https://img.shields.io/docker/pulls/swarmdeployorg/swarm-deploy)

**Continuous Delivery and operations for Docker Swarm.**

swarm-deploy is a Swarm-native GitOps controller inspired by Argo CD. It keeps stacks and networks aligned with Git, provides a service catalog and cluster UI, and adds audit events, notifications, observability, runtime automation, and an optional AI assistant.

[Configuration wizard](https://swarm-deploy.github.io/swarm-deploy/docs/configurator/) · [Basic example](./example/01-basic) · [Monitoring examples](./monitoring)

## Features

### GitOps

- **Git as the desired state** for stack definitions, Compose manifests, referenced configs/secrets, and managed Docker networks.
- **Reconciliation can be triggered by polling, webhooks, or both**. See [reconciliation](./docs/reconciler.md) and [webhooks](./docs/webhooks.md).
- **Diff-based deployments**: a stack is deployed only when its effective desired-state digest changes, including referenced config, secret, and env files.
- **Stack sync status** in the UI, including last sync result, Git revision, deployment status, and errors.
- **Desired vs. live manifests** for each stack, so the Git definition can be compared with the current Swarm state.
- **Drift detection and optional self-heal** for missing services and replica-count divergence, with sync status and audit events. See [drift detection](./docs/drift.md).
- **Pruning** of orphaned managed services removed from Compose, with policy overrides at global, stack, and service level. See [prune policy](./docs/prune.md).
- Optional **image policy** for requiring tags or digests and rejecting `latest`. See [image policy](./docs/image-policy.md).
- [**Managed Docker networks**](./docs/networks.md) kept in Git and reconciled before stack deployment.
- **Secret/config rollout rotation** using content-based names, so services receive new Docker objects when source files change. See [secret rotation](./docs/secrets-rotation.md).
- **age-encrypted Docker Secrets** decrypted in memory from Compose `.age` files, with optional name rotation. See [encrypted secrets](./docs/secrets-rotation.md#age-encrypted-secrets).

### Service Catalog

- **Persistent service catalog** built from deployed services and available through the API. See [service catalog](./docs/services.md).
- Automatic service **description and type detection** from swarm-deploy labels, OCI labels, container metadata, and built-in image knowledge.
- Service metadata for **image, repository, web routes, links, labels, networks, secrets, CPU/RAM requests and limits**.
- **Realtime task view** with task ID, node, state, timestamps, and task errors.
- **Deployment history** per service with image version, commit, status, and timestamps.
- **Dependency graph** with service endpoints and inferred dependencies, including known reverse-proxy relationships.

### Cluster & Operations

- **Cluster node inventory** with readiness, availability, manager status, CPU, RAM, address, Docker Engine version, labels, and node ID.
- **Docker networks inventory** in the web UI.
- **Docker secrets inventory** in the web UI without exposing secret values.
- Manual synchronization through the API and the assistant.
- Service runtime inspection through the API and assistant, including service spec and recent logs.
- Service scaling and restart operations available to the assistant when the corresponding tools are enabled.

### Runtime Automation

- **Init Deploy Jobs** for one-off tasks that must complete before an application rollout, such as database migrations, schema/bootstrap preparation, or other pre-deploy checks and setup steps. See [Init Deploy Jobs](./docs/init-deploy-jobs.md).
- [**Downward environment injection**](./docs/downward.md) for Swarm metadata such as stack, service, task, slot, and node identity.
- **Config and secret rotation** based on source content changes.
- CLI configuration validation. See [CLI](./docs/cli.md).

### Observability & Audit

- **Persisted event history** with severity and category filtering. See [Event History & Audit](./docs/event-history.md).
- Audit events for deployments, pruning, missing services, replica changes, service restarts, node connectivity, authentication, notification failures, and assistant security events.
- **Notifications by event type** through Telegram and custom HTTP hooks. See [notifications](./docs/notifications.md).
- **Prometheus metrics** and a dedicated health endpoint.
- [**OpenTelemetry tracing**](./docs/tracing.md) across GitOps reconciliation, Git access, Compose loading, filesystem operations, Swarm calls, network reconciliation, events, notifications, and web requests.

### Security & Access

- [**HTTP Basic authentication**](./docs/authentication.md#http-basic-authentication) for the UI and API using an `htpasswd` file.
- [**Trusted reverse-proxy authentication**](./docs/authentication.md#trusted-reverse-proxy-authentication) using a configurable login header, suitable for forward-auth/SSO proxies.
- Authentication events are written to the audit history.
- Sensitive integration credentials can be loaded from files/Docker Secrets instead of being stored directly in YAML.

### AI Assistant

The optional assistant uses the service catalog as context and can inspect live Swarm state through built-in tools. See [AI Assistant](./docs/assistant.md).

It can work with:

- event history and deployment failures,
- service specs and recent logs,
- Swarm nodes, networks, plugins, and secrets,
- service dependency graph and web-route checks,
- Git history,
- registry image versions,
- swarm-deploy Prometheus metrics,
- manual sync, service scaling, and service restart actions.

Tool access is configurable through an allow-list. User input is checked for prompt-injection patterns and rejected requests are recorded as security events.

## Web UI & API

The UI and REST API are served by the same web server. The UI includes:

- stack overview and sync state,
- desired/live stack manifests,
- service catalog and service details,
- service dependency graph,
- cluster resources: nodes, networks, and secrets,
- event and deployment information,
- optional AI assistant.

For frontend development without a running backend, use the
[OpenAPI mock workflow](./ui/README.md#development-with-the-mock-api).

## Quick start

Start with the [basic example](./example/01-basic) or generate a configuration with the [configuration wizard](https://swarm-deploy.github.io/swarm-deploy/docs/configurator/).

A minimal configuration looks like this:

```yaml
git:
  repository: https://github.com/your-org/your-stacks-repo.git
  branch: main
  auth:
    type: none

sync:
  mode: pull
  pollInterval: 30s
  interval: 1m
  policy:
    prune: true
    selfHeal: true

stacks:
  file: ./stacks.yaml

networks:
  file: ./networks.yaml

web:
  address: ":8080"
  # Optional URL prefix for both UI and API. Defaults to /.
  basePath: /swarm-deploy

healthServer:
  address: ":8082"
  metrics:
    path: /metrics
  healthz:
    path: /healthz
```

`web.basePath` defaults to `/`. A trailing slash is normalized, so `/infrastructure/swarm/` serves the UI at `/infrastructure/swarm/` and the API below `/infrastructure/swarm/api/`. Path segments must use URL-unreserved ASCII characters (letters, digits, `-`, `_`, `.`, `~`); percent-encoding and ServeMux wildcard syntax are not accepted. The configured value is authoritative; `X-Forwarded-Prefix` is not used. Health, metrics, and webhook endpoints keep their independently configured paths.

Stacks are declared separately:

```yaml
stacks:
  - name: myapp
    composeFile: myapp.yaml
```

For a fuller setup with notifications, secret rotation, Downward metadata, tracing, and runtime options, see [`example/01-basic/swarm-deploy.yaml`](./example/01-basic/swarm-deploy.yaml).

### Recommended Swarm update configuration

SQLite uses a local persistent volume. Pin the single swarm-deploy replica to one explicitly labelled manager so every
old/new task in a `start-first` handoff opens the same database and controller lock:

```shell
docker node update --label-add swarm-deploy.data=true <manager-node>
```

Run the swarm-deploy service with `start-first` updates and a 30-second stop grace period:

```yaml
services:
  swarm-deploy:
    stop_grace_period: 30s
    deploy:
      replicas: 1
      update_config:
        order: start-first
      placement:
        constraints:
          - node.labels.swarm-deploy.data == true
```

With `start-first`, Swarm starts the new task before stopping the current one. During graceful shutdown,
the current swarm-deploy instance stops starting new reconciliations while an already running reconciliation
gets time to finish status updates, event delivery, and post-deploy processing. This is especially important
when swarm-deploy manages its own service. An advisory controller lock in `dataDir` prevents the new task from
reconciling or recovering deployments until the old task has completed its handoff. A crashed task releases the lock,
and the next owner marks any orphaned `running` attempt `interrupted` before reconciling.

Do not run replicas on different nodes with node-local volumes: they have different SQLite databases and cannot share
the lock. A remote/network filesystem is not a supported substitute for local SQLite WAL. When upgrading from a build
that predates the controller lock, first capture the deployment diagnostics and SQLite backup described in
[`docs/operations/sqlite.md`](./docs/operations/sqlite.md), then use a one-time `stop-first` update (or otherwise prove the
old task has stopped) before returning to `start-first` for later releases.

### Docker registry authentication

If managed stacks use images from private registries, make Docker registry credentials available inside the
swarm-deploy container. The recommended setup is to expose Docker `config.json` as a Docker Secret and set
`DOCKER_CONFIG=/run/secrets`.

See [Docker registry authentication](./docs/registry-authentication.md) for the recommended Compose configuration,
supported alternatives, and security considerations.

## Documentation

- [SQLite operations and backup](./docs/operations/sqlite.md)
- [Reconciliation](./docs/reconciler.md)
- [Webhooks](./docs/webhooks.md)
- [Authentication](./docs/authentication.md)
- [Docker Registry Authentication](./docs/registry-authentication.md)
- [Managed Docker Networks](./docs/networks.md)
- [Downward Metadata](./docs/downward.md)
- [OpenTelemetry Tracing](./docs/tracing.md)
- [Prune policy](./docs/prune.md)
- [Image policy](./docs/image-policy.md)
- [Drift detection](./docs/drift.md)
- [Service catalog](./docs/services.md)
- [Event History & Audit](./docs/event-history.md)
- [Notifications](./docs/notifications.md)
- [Init Deploy Jobs](./docs/init-deploy-jobs.md)
- [Secrets Rotation](./docs/secrets-rotation.md)
- [AI Assistant](./docs/assistant.md)
- [CLI](./docs/cli.md)

## Examples

- [Basic deployment](./example/01-basic)
- [AI Assistant](./example/04-assistant)
- [Grafana and Prometheus monitoring](./monitoring)

## Ecosystem

- [cloud-secrets](https://github.com/swarm-deploy/cloud-secrets) — synchronize external secrets into Docker Swarm.
- [cloud-vector](https://github.com/swarm-deploy/cloud-vector) — logging for Docker Swarm.
- [node-agent](https://github.com/swarm-deploy/node-agent) — per-node metrics and maintenance agent.
- [init-jobs](https://github.com/swarm-deploy/init-jobs) — reusable init jobs, including PostgreSQL database initialization.
- [downward](https://github.com/swarm-deploy/downward) — Swarm downward metadata helpers and conventions.
- [downward-otel](https://github.com/swarm-deploy/downward-otel) — OpenTelemetry resource detection from Swarm downward metadata.
