# Services catalog

After each `deploySuccess` event, swarm-deploy collects metadata for deployed services and stores it in:

- `.swarm-deploy/services.json`

You can retrieve collected metadata via API:

- `GET /api/v1/services`

Response item fields:

- `name` - service name inside stack
- `stack` - stack name
- `description` - resolved service description
- `type` - one of: `application`, `monitoring`, `delivery`, `reverseProxy`, `database`
- `image` - service image reference

## Description resolving strategy

Priority (top to bottom):

1. Service label `org.swarm-deploy.service.description`
2. Container label `org.swarm-deploy.service.description`
3. Image label `org.opencontainers.image.title`
4. Image label `org.opencontainers.image.description`

## Type resolving strategy

Priority (top to bottom):

1. Service label `org.swarm-deploy.service.type`
2. Container label `org.swarm-deploy.service.type`
3. Built-in image dictionary by normalized image name (for example `postgres -> database`, `traefik -> reverseProxy`)
4. Fallback to `application`
