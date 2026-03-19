# swarm-deploy

GitOps controller for Docker Swarm with an ArgoCD-inspired, but Swarm-native, configuration style.

## Current capabilities

- `go-git` repository synchronization over `http/https` and `ssh`.
- Operating modes:
  - `pull` (polling),
  - `webhook`,
  - `hybrid` (both modes at the same time).
- Stack deployment only when a diff is detected (`compose + referenced configs/secrets` digest).
- Notification hooks for:
  - successful deployment,
  - failed deployment.
- Extensible notifier architecture:
  - Telegram,
  - custom webhook.
- Telegram supports:
  - `chatThreadId` (forums/topics),
  - `message` based on Go template.
- Prometheus metrics:
  - `swarm_deploy_total{stack,service,status}`,
  - `swarm_git_updates_total{repo,result}`,
  - `swarm_sync_runs_total{reason,result}`,
  - `swarm_sync_duration_milliseconds{reason,result}`.
- `x-init-deploy-jobs` in compose:
  - init jobs run before `docker stack deploy`,
  - in service networks,
  - with an attempt to attach service and job secrets/configs.
- Init jobs run via Docker Engine API.
- Graceful shutdown via `github.com/artarts36/go-entrypoint`.
- MVP UI without authentication.

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

## OpenAPI + ogen

The contract is in [`api/openapi.yaml`](./api/openapi.yaml). Code generation:

```bash
go generate ./api
```

`go:generate` uses `ogen`.

## External stack list

You can keep the stack list in a separate file using `stacksFile`.

Two formats are supported:

```yaml
stacks:
  - name: app
    composeFile: app/docker-compose.yml
```

or

```yaml
- name: app
  composeFile: app/docker-compose.yml
```

## Telegram templates

In a Telegram channel you can set:

- `botTokenPath` to read bot token from a file.
- `chatThreadId` to send to a specific thread/topic.
- `message` with `text/template` syntax.

Available template fields:

- `.status`
- `.success` (bool, `true` when `.status == "success"`)
- `.stack_name`
- `.service`
- `.image.full_name`
- `.image.version`
- `.commit`
- `.error`
- `.timestamp` (RFC3339)

Example:

```yaml
notifications:
  telegram:
    - name: ops
      botTokenPath: /run/secrets/telegram_bot_token
      chatId: "-1001234567890"
      chatThreadId: 42
      message: |
        deploy {{.status}}
        stack_name: {{.stack_name}}
        service: {{.service}}
        image.full_name: {{.image.full_name}}
        image.version: {{.image.version}}
        {{- if .error }}
        error: {{.error}}
        {{- end }}
```

## `x-init-deploy-jobs` example

```yaml
services:
  api:
    image: ghcr.io/company/api:v1.24.0
    networks:
      - backend
    secrets:
      - db_password
    x-init-deploy-jobs:
      - name: migrate
        image: ghcr.io/company/api:v1.24.0
        command: ["./bin/migrate", "up"]
        timeout: 5m
        environment:
          APP_ENV: production
```

## Secret rotation notes (inspired by swarm-cd)

In `swarm-cd`, rotation is done by changing `configs/secrets.*.name` to `stack-object-hash` when a source file changes.

Benefits:
- services are guaranteed to receive a new object version when a file changes.

Limitations:
- old objects are not removed automatically (a separate cleanup strategy is required),
- this is not cryptographic key rotation, but rotation of the object **name** to force rollout.

This project implements the same idea (hash-based naming), but with SHA-256.
