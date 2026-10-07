# CLI (`sd`)

`sd` is a lightweight command-line tool for validating `swarm-deploy` config files before deployment.

Install:
```
go install github.com/swarm-deploy/swarm-deploy/cmd/sd
```

## Run

From the repository root:

```bash
sd --help
```

Validate config file:

```bash
sd lint
sd lint ./example/04-assistant/swarm-deploy.yaml
```

If `configPath` is omitted, `lint` uses `./swarm-deploy.yaml`.

## Command reference

### `lint [configPath]`

Validates:

- `swarm-deploy` YAML structure and fields.
- Stack and network config.
- Referenced compose files (paths are resolved relative to the config file directory).

On success, prints a short summary with detected stacks, networks, and services.

Typical error messages:

- `Config is invalid: ...`
- `Compose file <path> is invalid: ...`

## Exit status

- `0` - validation passed.
- Non-zero - validation failed or command usage error.
