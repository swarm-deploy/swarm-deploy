# Init Deploy Jobs

Init jobs run before `docker stack deploy`:
- in service networks,
- with an attempt to attach service and job secrets/configs.

Every Init Job execution that reaches the Docker service stage is recorded under
its Deployment Attempt. Deployment Details shows the jobs in execution order and
loads their Docker output only when **View Logs** is selected. The stored output is
the original, unfiltered tail (up to 64 KiB); the UI marks truncated output and
cases where Docker logs could not be retrieved.

Log collection and persistence are best-effort observability operations. A failure
in either operation does not change the Init Job result, fail a successful
Deployment, alter an existing failure, prevent later Init Jobs, or delay Docker
service cleanup beyond the bounded collection attempt.

Init Job output can contain credentials. Access uses the same authentication and
authorization as Deployment Details, and raw output is available only from the
execution-specific log endpoint. It is not included in Deployment list/detail
payloads, events, traces, diagnostics, or application errors.

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
