# Reconciliation

swarm-deploy continuously reconciles the desired state from Git with the Docker Swarm cluster.

A reconciliation run loads the configured Compose file, computes its effective desired state, applies swarm-deploy-specific mutations, deploys when necessary, reads the live cluster state, prunes managed orphaned services, and analyzes drift.

## Reconciliation pipeline

The current pipeline is:

1. Load Compose.
2. Populate service environment from `env_file`.
3. Inject Downward metadata when enabled.
4. Add the swarm-deploy managed label.
5. Rotate configs and secrets when enabled.
6. Write a rendered Compose file when the desired state was mutated.
7. Deploy the stack when the source digest changed or the desired state was mutated.
8. Load live Swarm state.
9. Prune managed orphaned services when applicable.
10. Analyze drift.

Some steps are conditional. For example, environment population runs only when at least one desired service declares `env_file`.

## Source digest

swarm-deploy calculates a source digest for each stack. The digest includes:

- the Compose file content;
- referenced config file content;
- referenced secret file content;
- referenced `env_file` content.

A change to a referenced file therefore triggers reconciliation even when the Compose YAML itself did not change.

The source digest is calculated from the source definition before swarm-deploy mutates the desired state.

## `env_file` normalization

During reconciliation, swarm-deploy expands service `env_file` entries into the service `environment` map before the remaining mutation steps run.

Given:

```yaml
services:
  api:
    env_file:
      - default.env
      - production.env
    environment:
      LOG_LEVEL: debug
```

the files are applied in declaration order. Values from later files override values from earlier files, then explicit values from `environment` override values resolved from the files.

After resolution, `env_file` is removed from the effective desired state. Downstream reconciliation therefore operates on a self-contained service environment.

Values from env files are treated literally. swarm-deploy does not perform interpolation, substitution, quote removal, or escaping while populating `environment`.

### Bare keys are rejected

Unlike Docker CLI env-file handling, swarm-deploy does not allow a key without an explicit `=`.

Allowed:

```dotenv
FOO=bar
EMPTY=
```

Rejected:

```dotenv
TOKEN
```

This behavior is intentional. A bare key in Docker CLI can read the value of the same variable from the environment of the process running the CLI. Allowing this in swarm-deploy would let repository content reference environment variables from the swarm-deploy controller process.

The reconciliation boundary therefore enforces the following invariant:

> Repository-controlled `env_file` content must never read values from the swarm-deploy process environment.

If a bare key is present, reconciliation fails with an error instead of resolving the value from the controller environment.

## Desired-state mutations

swarm-deploy may mutate the loaded source Compose before deployment. Today this includes:

- resolving `env_file` into `environment`;
- injecting Downward metadata;
- adding the managed-service label;
- rotating config and secret object names.

When any of these steps changes the desired state, swarm-deploy writes a rendered Compose file and deploys that rendered form.

This distinction is important: the source Compose in Git is the declarative input, while the rendered Compose is the effective desired state sent to Docker.
