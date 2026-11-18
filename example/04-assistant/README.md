# Basic configuration example

This example contains basic configurations for deploy public repositories.
- `pull` mode
- non-auth polling
- init jobs (for secrets rotation)
- UI and API on `8080` port (`GET /api/v1/stacks`, `POST /api/v1/sync`, `GET /api/v1/events`, `GET /api/v1/services`, `POST /api/v1/assistant/chat`)
- event history persisted on disk with `event.history.capacity` limit
- optional UI/API basic authentication via `web.security.authentication.basic.htpasswdFile`
- optional AI assistant (`assistant.enabled`) with long-poll chat API
- Health Server on `8082` port

Your steps:
- Optional for UI/API auth: create htpasswd secret, for example
  `docker run --rm httpd:2.4-alpine htpasswd -nbB admin change-me | docker secret create basic.htpasswd -`
- Run `docker stack deploy --with-registry-auth -c docker-compose.yaml swarm-deploy --detach=false`

## Check setup guidance on the configured model

Use a fresh chat for each prompt and check the actual answer, including its opening paragraph.

| Prompt | Expected answer |
|---|---|
| «Как установить Prometheus?» | Opens with one GitOps/Swarm installation. Provides the Compose service, a minimal `prometheus.yml` wired as a Swarm config, and a way to verify a scrape target after sync. No binary, standalone Docker, or Helm walkthrough. |
| «Как развернуть Redis?» | Gives the managed-stack Compose change and a check appropriate to Redis. No unsolicited platform survey. |
| «Сравни установку через Helm и через Docker Swarm» | Compares both requested environments. |
