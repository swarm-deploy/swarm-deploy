# Docker Registry Authentication

swarm-deploy needs Docker registry credentials when it deploys images from private registries.

Stack deployments create and update services through the Docker API. The native
service applier obtains registry credentials from `internal/registry.AuthManager`
and passes them in Docker service create/update options. Docker CLI is not needed
inside the container. Credentials available only on the host are not automatically
visible to swarm-deploy.

Registry authentication is cached in memory. On each image-auth lookup (service
create/update, Init Job create, or registry version lookup), swarm-deploy checks
the configuration file's identity, modification time, and size. Unchanged files
are not reread or reparsed; changed files are loaded automatically without a
restart. Credential files should be updated with an atomic file replacement
(or a write that changes the modification time).
Malformed or unreadable configuration causes the operation to fail with an explicit error,
rather than silently falling back to anonymous access. If no config file exists and
`DOCKER_AUTH_CONFIG` is unset, anonymous access remains available.

For services where a credential-only update is safe (see job execution safety below),
the applier checks credentials even when the service specification is unchanged.
Changed credentials are sent to Docker without changing the task template or resolving
an unchanged image again. Successful credential writes are tracked by an in-memory
fingerprint per service; after restart, configured nonempty credentials are refreshed
once because Docker does not expose its stored pull credentials. Fingerprints and
credentials are not persisted or logged. Removing credentials observed by this process
sends anonymous auth explicitly. If the process starts without configured credentials,
Docker's existing service credentials are retained (`RegistryAuthFromSpec`).

**Job execution safety:** Docker Swarm advances the execution iteration on every
update of a native `replicated-job` or `global-job`, even if only credentials change.
For ordinary services with `restart_policy.condition: none`, `on-failure`, or a finite
`max_attempts`, changing pull credentials can also rerun completed or exhausted tasks.
A task may finish between inspection and update, so checking running tasks cannot
make this safe. Credential-only updates are deferred for these services, without
acknowledging the new credentials, until an intentional specification update or
explicit failed-execution retry already requires a write. Ordinary replicated
services can also refresh safely once their current replicas are zero. Until then,
Docker retains the previous credentials; newly scheduled tasks (including global
jobs on added nodes) can still use them. Externally scheduled services retain their
existing replica and execution ownership.

The same registry authentication is also used when swarm-deploy:

- creates Init Deploy Job services that use private images;
- resolves image versions from a registry.

## Recommended setup

Provide the Docker CLI `config.json` to swarm-deploy as a Docker Secret and point `DOCKER_CONFIG` at the secrets
directory:

```yaml
services:
  swarm-deploy:
    image: swarmdeployorg/swarm-deploy:<version>
    environment:
      DOCKER_CONFIG: /run/secrets
    secrets:
      - source: registry_auth_config
        target: config.json

secrets:
  registry_auth_config:
    file: ./secrets/docker-config.json
```

With this configuration, swarm-deploy reads registry credentials from:

```text
/run/secrets/config.json
```

Create `docker-config.json` from a Docker CLI configuration that already contains the required registry login:

```sh
docker login registry.example.com
cp ~/.docker/config.json ./secrets/docker-config.json
```

Do not commit this file to Git. Docker `config.json` commonly contains reusable registry credentials or tokens.

Using a Docker Secret is preferred over a bind mount because it does not depend on a node-local path and limits the
credential file to the service that explicitly consumes the secret.

**Token rotation:** Docker Swarm secrets are immutable. Updating the source file does not
update a running task's mounted secret. To rotate credentials supplied by a Docker Secret,
create a new secret and update the swarm-deploy service to use it. For short-lived tokens
(such as AWS ECR authorization tokens), use a regularly refreshed bind-mounted config file
or another mechanism that replaces the file visible inside the container. The refreshed
credentials will be picked up by the next registry auth lookup when the file
metadata changes; this implementation does not refresh expired tokens itself.

## Alternative: read-only bind mount

For installations where credentials are refreshed on the Swarm manager, mount the containing directory read-only:

```yaml
services:
  swarm-deploy:
    environment:
      DOCKER_CONFIG: /run/registry-auth
    volumes:
      - /root/.docker:/run/registry-auth:ro
```

Replace `config.json` atomically within the mounted directory (write a temporary file, then rename it).
The container sees the new directory entry on its next lookup. Mounting the individual file instead
would pin the old inode and can hide atomic replacements.

The directory and file must exist on every node where the swarm-deploy task may be scheduled. If the service is constrained to one
manager node, it only needs to exist on that node.

## `DOCKER_AUTH_CONFIG`

swarm-deploy also supports the standard `DOCKER_AUTH_CONFIG` environment variable:

```yaml
services:
  swarm-deploy:
    environment:
      DOCKER_AUTH_CONFIG: '{"auths":{...}}'
```

This is supported, but it is not the recommended deployment method because credentials stored directly in the service
environment are easier to expose through service inspection and deployment configuration.

## Lookup order

Registry authentication is loaded in this order:

1. `DOCKER_AUTH_CONFIG`, when set;
2. `$DOCKER_CONFIG/config.json`, when `DOCKER_CONFIG` is set;
3. `$HOME/.docker/config.json`.

For containerized swarm-deploy installations, explicitly setting `DOCKER_CONFIG` is recommended so the credential
location is deterministic.

## Limitations

Only inline entries in `auths` are supported (including `auth`, `username`/`password`,
and `identitytoken` fields). Docker CLI credential helpers configured with `credsStore`
or `credHelpers` are **not** supported. A `config.json` containing only helper
references does not provide usable registry credentials to swarm-deploy. Use a
`config.json` with inline `auths` entries or `DOCKER_AUTH_CONFIG` instead.

`DOCKER_AUTH_CONFIG` takes precedence over the file and is read and parsed once when
`AuthManager` is created. It is not reloaded during deployment. Restart or recreate
the service when changing this variable.
