# swarm-deploy

GitOps controller for Docker Swarm with an ArgoCD-inspired, but Swarm-native, configuration style.

## Current capabilities

- Operating modes:
  - `pull` (polling),
  - `webhook`,
  - `hybrid` (both modes at the same time).
- Stack deployment only when a diff is detected (`compose + referenced configs/secrets` digest). .
- [Notification hooks for successful and failed deployments](./docs/notifications.md)
- [Init Deploy Jobs](./docs/init-deploy-jobs.md)
- [Secrets Rotation](./docs/secrets-rotation.md)

## Quick start

1. Copy [swarm-deploy.example.yaml](./swarm-deploy.example.yaml) to `swarm-deploy.yaml` and fill in values.
2. Copy [stacks.example.yaml](./stacks.example.yaml) to your `stacks.yaml` and describe stacks.
3. In `swarm-deploy.yaml`, set `stacksFile: ./stacks.yaml`.
4. Run:

```bash
go run ./cmd/swarm-deploy -config ./swarm-deploy.yaml
```

Or run with Docker Compose (including `docker/config.json` as a secret for registry auth):

```bash
mkdir -p ./secrets
cp ~/.docker/config.json ./secrets/docker-config.json
cp ~/.ssh/id_ed25519 ./secrets/deploy_key
printf '%s' '<telegram-bot-token>' > ./secrets/telegram_bot_token
printf '%s' '<webhook-secret>' > ./secrets/webhook_secret
docker compose up --build -d
```

`docker-compose.yaml` mounts `registry_auth_config` to `/run/secrets/config.json` and sets `DOCKER_CONFIG=/run/secrets`, so `docker stack deploy --with-registry-auth` uses this file. Webhook secret is read from `sync.webhook.secretPath` (in examples: `/run/secrets/webhook_secret`).

5. Web servers are split by ports:
- Frontend (`/` and `/ui/`) is available on `web.frontendAddress`.
- API (`GET /api/v1/stacks`, `POST /api/v1/sync`) is available on `web.apiAddress`.
- Webhook (`POST /api/v1/webhooks/git`) is available on `sync.webhook.address`.

6. Health/metrics server is available on `healthServer.address`:
- `GET healthServer.healthz.path` (if `healthServer.healthz.enabled=true`)
- `GET healthServer.metrics.path` (if `healthServer.metrics.enabled=true`)

7. Init jobs require access to Docker Engine API (typically via `/var/run/docker.sock`).
