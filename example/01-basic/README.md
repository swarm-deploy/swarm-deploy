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

For a service whose replica count is controlled externally, configure Compose deploy
labels independently for automatic repair and replica drift detection:

```yaml
services:
  scheduled-job:
    image: example/job:1.0.0
    deploy:
      replicas: 0
      labels:
        org.swarm-deploy.service.sync.policy.selfHeal: "false"
        org.swarm-deploy.service.sync.policy.selfHeal.replicas: "false"
```

Replica drift detection stays enabled by default. Missing services are still reported;
`selfHeal=false` disables repair, while `selfHeal.replicas=false` suppresses replica
drift events and alerts. See [drift configuration](../../docs/drift.md) for policy
precedence and scheduled-service recommendations.
