# Basic configuration example

This example contains basic configurations for deploy public repositories.
- `pull` mode
- non-auth polling
- init jobs (for secrets rotation)
- UI and API on `8080` port (`GET /api/v1/stacks`, `POST /api/v1/sync`, `GET /api/v1/events`, `GET /api/v1/services`, `POST /api/v1/assistant/chat`)
- event history persisted on disk with `eventHistory.capacity` limit
- optional UI/API basic authentication via `web.security.authentication.basic.htpasswdFile`
- optional AI assistant (`assistant.enabled`) with long-poll chat API
- Health Server on `8082` port

Your steps:
- Add secret `printf 'change-me' | docker secret create db_password -`
- Add config `docker config create api_env ./api_env`
- Optional for UI/API auth: create htpasswd secret, for example
  `docker run --rm httpd:2.4-alpine htpasswd -nbB admin change-me | docker secret create basic.htpasswd -`
- Run `docker stack deploy --with-registry-auth -c docker-compose.yaml swarm-deploy --detach=false`
