# swarm-deploy

GitOps controller for Docker Swarm with ArgoCD-inspired, but Swarm-native, configuration style.

## Что умеет сейчас

- `go-git` синхронизация репозитория по `http/https` и `ssh`.
- Режимы работы:
  - `pull` (polling),
  - `webhook`,
  - `hybrid` (оба режима одновременно).
- Деплой стека только при diff (`compose + referenced configs/secrets` digest).
- Хуки нотификаций при:
  - успешном деплое,
  - ошибке деплоя.
- Расширяемая архитектура нотификаторов:
  - Telegram,
  - custom webhook.
- Для Telegram поддерживаются:
  - `chatThreadId` (форумы/топики),
  - `message` на Go template.
- Prometheus-метрики:
  - `swarm_deploy_total{stack,service,status}`,
  - `swarm_git_updates_total{repo,result}`,
  - `swarm_sync_runs_total{reason,result}`,
  - `swarm_sync_duration_milliseconds{reason,result}`.
- `x-init-deploy-jobs` в compose:
  - init jobs запускаются до `docker stack deploy`,
  - в сетях сервиса,
  - с попыткой подключения secrets/configs сервиса и job.
- Init jobs выполняются через Docker Engine API.
- Graceful shutdown через `github.com/artarts36/go-entrypoint`.
- MVP UI без аутентификации.

## Быстрый старт

1. Скопируйте [swarm-deploy.example.yaml](./swarm-deploy.example.yaml) в `swarm-deploy.yaml` и заполните значения.
2. Скопируйте [stacks.example.yaml](./stacks.example.yaml) в свой `stacks.yaml` и опишите стеки.
3. В `swarm-deploy.yaml` укажите `stacksFile: ./stacks.yaml`.
4. Запустите:

```bash
go run ./cmd/swarm-deploy -config ./swarm-deploy.yaml
```

Либо через Docker Compose (с передачей `docker/config.json` в secret для auth в Registry):

```bash
mkdir -p ./secrets
cp ~/.docker/config.json ./secrets/docker-config.json
cp ~/.ssh/id_ed25519 ./secrets/deploy_key
printf '%s' '<telegram-bot-token>' > ./secrets/telegram_bot_token
printf '%s' '<webhook-secret>' > ./secrets/webhook_secret
docker compose up --build -d
```

`docker-compose.yaml` монтирует `registry_auth_config` в `/run/secrets/config.json` и выставляет `DOCKER_CONFIG=/run/secrets`, поэтому `docker stack deploy --with-registry-auth` использует этот файл. Секрет вебхука берется из `sync.webhook.secretFile` (в примерах это `/run/secrets/webhook_secret`).

5. UI и API доступны на `web.address`:
- `GET /api/v1/stacks`
- `POST /api/v1/sync`
- `POST /api/v1/webhooks/git`

6. Health/metrics сервер доступен на `healthServer.address`:
- `GET healthServer.healthz.path` (если `healthServer.healthz.enabled=true`)
- `GET healthServer.metrics.path` (если `healthServer.metrics.enabled=true`)

7. Init jobs требуют доступ к Docker Engine API (обычно через `/var/run/docker.sock`).

## OpenAPI + ogen

Контракт находится в [`api/openapi.yaml`](./api/openapi.yaml). Генерация кода:

```bash
go generate ./api
```

`go:generate` использует `ogen`.

## Вынесенный список стеков

Можно хранить список стеков в отдельном файле через `stacksFile`.

Поддерживаются два формата:

```yaml
stacks:
  - name: app
    composeFile: app/docker-compose.yml
```

или

```yaml
- name: app
  composeFile: app/docker-compose.yml
```

## Telegram шаблоны

В канале Telegram можно задать:

- `botTokenSecretFile` для чтения токена бота из файла.
- `chatThreadId` для отправки в конкретный thread/topic.
- `message` с синтаксисом `text/template`.

Доступные поля в шаблоне:

- `.status`
- `.stack_name`
- `.service`
- `.image.full_name`
- `.image.version`
- `.commit`
- `.error`
- `.timestamp` (RFC3339)

Пример:

```yaml
notifications:
  telegram:
    - name: ops
      botTokenSecretFile: /run/secrets/telegram_bot_token
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

## Пример `x-init-deploy-jobs`

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

## Про ротацию секретов (по мотивам swarm-cd)

В `swarm-cd` ротация работает через изменение `configs/secrets.*.name` на `stack-object-hash` при изменении файла.

Плюсы:
- сервисы гарантированно получают новую версию объекта при изменении файла.

Ограничения:
- старые объекты не удаляются автоматически (нужна отдельная cleanup-стратегия),
- это не криптографическая ротация ключей, а ротация **имени** объекта для форсирования rollout.

В этом проекте реализована такая же идея (hash-based naming), но с SHA-256.
