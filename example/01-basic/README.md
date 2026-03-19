# Basic configuration example

This example contains basic configurations for deploy public repositories.
- `pull` mode
- non-auth polling
- init jobs (for secrets rotation)
- UI on `8082` port
- API Server on `8080`
- Health Server on `8081` port

Your steps:
- Add secret `docker secret create db_password`
- Run `docker stack deploy --with-registry-auth -c docker-compose.yaml swarm-deploy --detach=false`
