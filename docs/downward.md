# Downward Metadata

swarm-deploy can automatically inject Docker Swarm runtime metadata into every service as environment variables. This gives applications access to their stack, service, task, slot, and node identity without declaring these values manually in each Compose file.

## Enable

Enable Downward injection in `swarm-deploy.yaml`:

```yaml
containers:
  downward: {}
```

After it is enabled, swarm-deploy adds the following environment variables to services before deployment.

| Environment variable | Injected value | Description |
| --- | --- | --- |
| `SWARM_STACK_NAME` | stack name from swarm-deploy configuration | Stack that owns the service |
| `SWARM_SERVICE_ID` | `{{.Service.ID}}` | Docker Swarm service ID |
| `SWARM_SERVICE_NAME` | `{{.Service.Name}}` | Docker Swarm service name |
| `SWARM_TASK_ID` | `{{.Task.ID}}` | Current task ID |
| `SWARM_TASK_NAME` | `{{.Task.Name}}` | Current task name |
| `SWARM_TASK_SLOT` | `{{.Task.Slot}}` | Current task slot |
| `SWARM_NODE_ID` | `{{.Node.ID}}` | ID of the node running the task |
| `SWARM_NODE_NAME` | `{{.Node.Hostname}}` | Hostname of the node running the task |

The `{{...}}` values are Docker Swarm template expressions. Docker resolves them for each task when the container is created, so task- and node-specific variables contain the values of the actual running task.

## Existing environment variables

swarm-deploy does not overwrite Downward variables declared by the service. If a service already defines any of the variables listed above, automatic Downward injection is skipped for that service.

Applications can read these variables directly or use the [`swarm-deploy/downward`](https://github.com/swarm-deploy/downward) helpers.

## OpenTelemetry for Go

Go applications using OpenTelemetry can also use [`swarm-deploy/downward-otel`](https://github.com/swarm-deploy/downward-otel). It provides an OpenTelemetry `ResourceDetector` that reads Downward metadata and maps it to standard OpenTelemetry resource attributes while preserving the Swarm-specific values under `docker.swarm.*`.

```go
res, err := resource.New(
    ctx,
    resource.WithDetectors(downwardotel.NewDetector()),
)
```

This lets traces and other telemetry carry Swarm service, task, and node identity without adding application-specific environment parsing.
