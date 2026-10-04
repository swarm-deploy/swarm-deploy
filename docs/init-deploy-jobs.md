# Init Deploy Jobs

Init jobs run before `docker stack deploy`:
- in service networks,
- with an attempt to attach service and job secrets/configs.

Before init jobs are started, swarm-deploy reconciles the declared Docker configs and secrets and resolves their Docker IDs for the init-job service specs. Regular services are still deployed afterwards by `docker stack deploy`, which performs its own resource lookup.

`entrypoint` overrides the image entrypoint. When `entrypoint` is set, `command` is passed as arguments to it. Without `entrypoint`, `command` keeps the existing init-job behavior for backward compatibility.

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
