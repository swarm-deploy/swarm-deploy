# swarm-deploy

![Docker Pulls](https://img.shields.io/docker/pulls/swarmdeployorg/swarm-deploy)

**Continuous Delivery and operations for Docker Swarm.**

swarm-deploy is a Swarm-native GitOps controller inspired by Argo CD. It keeps stacks and networks aligned with Git, provides a service catalog and cluster UI, and adds audit events, notifications, observability, runtime automation, and an optional AI assistant.

[Configuration wizard](https://swarm-deploy.github.io/swarm-deploy/docs/configurator/) · [Basic example](./example/01-basic) · [Monitoring examples](./monitoring)

## Features

### GitOps

- **Git as the desired state** for stack definitions, Compose manifests, referenced configs/secrets, and managed Docker networks.
- **Reconciliation can be triggered by polling, webhooks, or both**.
- **Diff-based deployments**: a stack is deployed only when its effective desired-state digest changes, including referenced config, secret, and env files.
- **Stack sync status** in the UI, including last sync result, Git revision, deployment status, and errors.
- **Desired vs. live manifests** for each stack, so the Git definition can be compared with the current Swarm state.
- **Drift detection** for desired services missing from the live cluster, with `OutOfSync` status and audit events.
- **Pruning** of orphaned managed services removed from Compose, with policy overrides at global, stack, and service level. See [prune policy](./docs/prune.md).
- [**Managed Docker networks**](./docs/networks.md) kept in Git and reconciled before stack deployment.
- **Secret/config rollout rotation** using content-based names, so services receive new Docker objects when source files change. See [secret rotation](./docs/secrets-rotation.md).

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
  policy:
    prune: true

stacks:
  file: ./stacks.yaml

networks:
  file: ./networks.yaml

web:
  address: ":8080"

healthServer:
  address: ":8082"
  metrics:
    path: /metrics
  healthz:
    path: /healthz
```

Stacks are declared separately:

```yaml
stacks:
  - name: myapp
    composeFile: myapp.yaml
```

For a fuller setup with notifications, secret rotation, Downward metadata, tracing, and runtime options, see [`example/01-basic/swarm-deploy.yaml`](./example/01-basic/swarm-deploy.yaml).

## Documentation

- [Authentication](./docs/authentication.md)
- [Managed Docker Networks](./docs/networks.md)
- [Downward Metadata](./docs/downward.md)
- [OpenTelemetry Tracing](./docs/tracing.md)
- [Prune policy](./docs/prune.md)
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
