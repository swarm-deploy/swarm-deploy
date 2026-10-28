# Docker Registry Authentication

swarm-deploy needs Docker registry credentials when it deploys images from private registries.

Stack deployments are executed with `docker stack deploy --with-registry-auth`. The Docker CLI runs inside the
swarm-deploy container, so credentials available only on the host are not automatically visible to swarm-deploy.

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

## Alternative: read-only bind mount

For installations where the credential file is managed directly on the Swarm manager, mount it read-only:

```yaml
services:
  swarm-deploy:
    environment:
      DOCKER_CONFIG: /root/.docker
    volumes:
      - /root/.docker/config.json:/root/.docker/config.json:ro
```

The file must exist on every node where the swarm-deploy task may be scheduled. If the service is constrained to one
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
