# swarm-deploy

GitOps controller for Docker Swarm with an ArgoCD-inspired, but Swarm-native, configuration style.

## Current capabilities

- Operating modes:
  - `pull` (polling),
  - `webhook`,
  - `hybrid` (both modes at the same time).
- Stack deployment only when a diff is detected (`compose + referenced configs/secrets` digest). .
- UI and API are served by a single web server on `web.address`.
- Optional HTTP Basic authentication for UI and API via `web.security.authentication.basic.htpasswdFile`.
- [Notification hooks for successful and failed deployments](./docs/notifications.md)
- [Services catalog persisted on disk and available via API](./docs/services.md)
- [Init Deploy Jobs](./docs/init-deploy-jobs.md)
- [Secrets Rotation](./docs/secrets-rotation.md)

## Usage examples
- [Basic: deploy public repositories](./example/01-basic)
- TODO: Example of deploy private repositories
- TODO: Example of deploy private repositories via webhook
