# Basic configuration example

This example contains basic configurations for deploy public repositories.
- `pull` mode
- non-auth polling
- init jobs (for secrets rotation)
- UI and API on `8080` port (`GET /api/v1/stacks`, `POST /api/v1/sync`, `GET /api/v1/events`, `GET /api/v1/services`, `POST /api/v1/assistant/chat`)
- event history persisted on disk with `eventHistory.capacity` limit
- Health Server on `8082` port

Your steps:
- Run `docker stack deploy --with-registry-auth -c docker-compose.yaml swarm-deploy --detach=false`
