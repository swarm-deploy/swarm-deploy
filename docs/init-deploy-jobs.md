# Init Deploy Jobs

Init jobs run before `docker stack deploy`:
- in service networks,
- with an attempt to attach service and job secrets/configs.

Before init jobs are started, swarm-deploy reconciles all referenced Docker configs and secrets and resolves their Docker IDs. Regular services later reuse the same resolved resources.

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
        entrypoint: ["/bin/sh", "-c"]
        command: ["./bin/migrate up"]
        timeout: 5m
        environment:
          APP_ENV: production
```
