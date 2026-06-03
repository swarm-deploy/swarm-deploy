# swarm-deploy

![Docker Pulls](https://img.shields.io/docker/pulls/swarmdeployorg/swarm-deploy)

GitOps controller for Docker Swarm with an ArgoCD-inspired, but Swarm-native, configuration style.

[Wizard for easy configuration](https://swarm-deploy.github.io/swarm-deploy/docs/configurator/)

## Capabilities

- Sync: stacks, services, networks
- Operating modes:
  - `pull` (polling),
  - `webhook`,
  - `hybrid` (both modes at the same time).
- Stack deployment only when a diff is detected (`compose + referenced configs/secrets` digest). .
- [Pruning of orphaned managed services removed from desired compose](./docs/prune.md).
- GitOps reconciliation for Docker networks from `networks.file` with managed label `org.swarm-deploy.network.managed=true`.
- UI and API are served by a single web server on `web.address`.
- Service Realtime page with task snapshot (node hostname, state, created/updated timestamps, and task ID).
- HTTP Basic authentication for UI and API via `web.security.authentication.basic.htpasswdFile`.
- [Event History & Audit](./docs/event-history.md)
- [Notification hooks for successful and failed deployments](./docs/notifications.md)
- [Services catalog persisted on disk and available via API](./docs/services.md)
- [AI assistant with long-poll chat API and RAG over service metadata](./docs/assistant.md)
- [Init Deploy Jobs](./docs/init-deploy-jobs.md)
- [Secrets Rotation](./docs/secrets-rotation.md)
- [Drift Detection](./docs/drift.md)
- [CLI for config validation](./docs/cli.md)

## Usage examples
- [Basic: deploy public repositories](./example/01-basic)
- TODO: Example of deploy private repositories
- TODO: Example of deploy private repositories via webhook
- [AI Assistant](./example/04-assistant)
- [Monitoring configurations: Grafana and Prometheus](./monitoring)

## Ecosystem
- [cloud-vector](https://github.com/swarm-deploy/cloud-vector) - logging for Docker Swarm
- [cloud-secrets](https://github.com/swarm-deploy/cloud-secrets) - background service for update secrets in Docker Swarm cluster
- [init-jobs](https://github.com/swarm-deploy/init-jobs) - Collection for ready init jobs. Example: [postgres](https://github.com/swarm-deploy/init-jobs/blob/master/postgres/README.md) for create database
- [node-agent](https://github.com/swarm-deploy/node-agent) - agent for nodes with metrics collecting and cleaning volumes, containers
