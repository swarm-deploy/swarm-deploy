# Reconciliation

swarm-deploy continuously reconciles the desired state from Git with the Docker Swarm cluster.

A reconciliation run loads the configured Compose file, computes its effective desired state, applies swarm-deploy-specific mutations, deploys when necessary, reads the live cluster state, prunes managed orphaned services, and analyzes drift.

## Reconciliation pipeline

The current pipeline is:

1. Load Compose and interpolate source YAML values using Docker stack CLI semantics (see [source interpolation](./compose-converter.md#source-interpolation)).
2. [Check image policy when at least one image rule is enabled](./image-policy.md).
3. Populate service environment from `env_file`.
4. [Inject Downward metadata when enabled](./downward.md).
5. Add the swarm-deploy managed label.
6. [Rotate configs and secrets when enabled](./secrets-rotation.md).
7. Submit the effective in-memory desired state to Deployments when the source digest changed or the desired state was mutated. Deployments validates desired state, reconciles networks, reconciles configs/secrets, converts Compose to Docker API specs, runs Init Jobs, captures the service baseline, applies services through the Docker API, and waits for convergence.
8. Load live Swarm state.
9. [Prune managed orphaned services when applicable](./prune.md).
10. [Analyze drift](./drift.md).
11. Reapply drifted services when their [self-heal policy](./drift.md#self-heal-policy) is enabled, without Init Jobs.
12. Reload stack services once and verify every attempted repair against actual Swarm state.

Some steps are conditional. For example, the image policy step is absent when every image rule is disabled, and environment population runs only when at least one desired service declares `env_file`.

## Source digest

swarm-deploy calculates a source digest for each stack. The digest includes:

- the Compose file content;
- referenced config file content;
- referenced secret file content;
- normalized key/value variables loaded from referenced `env_file` files.

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

For one file, the scalar shorthand `env_file: production.env` is also supported.

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

The source Compose in Git is the declarative input. Preparation produces an effective
in-memory definition, which both stack deployments and single-service remediation
submit directly to Deployments. No intermediate Compose file is written or read.
Execution traces identify the original source Compose path. Legacy rendered files
from older versions are removed after the controller acquires ownership.

## Configuration compatibility

`swarm.command` has been removed: Deployments uses the Docker API and no longer accepts a Docker executable setting. Remove it from existing YAML configuration, along with obsolete `swarm.stackDeployArgs` entries. No migration is required. The `swarm.initJobPollEvery` and `swarm.initJobMaxDuration` settings and their defaults are unchanged. The runtime image no longer installs Docker CLI; CA certificates and timezone data remain included.
