# Services catalog

After a successful deployment, swarm-deploy updates the service catalog from
`DeploySuccess` through the event subscriber. The catalog is persisted in the
`services` table of `<dataDir>/swarm-deploy.sqlite`, not in a JSON file. Since
event subscribers run asynchronously, the catalog may briefly lag deployment
completion.

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
3. Label `org.opencontainers.image.description` (service -> container -> image)
4. Label `org.opencontainers.image.title` (service -> container -> image)

## Type resolving strategy

Priority (top to bottom):

1. Service label `org.swarm-deploy.service.type`
2. Container label `org.swarm-deploy.service.type`
3. Built-in image dictionary by normalized image name (for example `postgres -> database`, `traefik -> reverseProxy`)
4. Fallback to `application`
