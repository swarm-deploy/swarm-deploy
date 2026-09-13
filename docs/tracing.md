# OpenTelemetry Tracing

swarm-deploy can export distributed traces using OpenTelemetry Protocol (OTLP). Tracing is optional and is enabled when the `tracing` section is present in `swarm-deploy.yaml`.

## Overview

- Transports: [OTLP over HTTP](#otlp-over-http) and [OTLP over gRPC](#otlp-over-grpc).
- Exporter authentication: [Bearer token](#bearer-token), [`Authorization: Api-Key`](#api-key), and [`x-api-key`](#x-api-key).
- [Environment variables](#environment-variables) can be used for the endpoint and custom headers.
- [Resource attributes](#resource-attributes) include standard OpenTelemetry metadata and Swarm metadata from `downward-otel`.

## Enable tracing

### OTLP over HTTP

```yaml
tracing:
  transport: http
  exporter:
    endpoint: http://otel-collector:4318
```

### OTLP over gRPC

```yaml
tracing:
  transport: grpc
  exporter:
    endpoint: http://otel-collector:4317
```

`transport` must be either `http` or `grpc`.

The exporter endpoint must include an `http://` or `https://` scheme.

## Environment variables

The exporter endpoint and custom header values can reference environment variables:

```yaml
tracing:
  transport: http
  exporter:
    endpoint: $OTEL_EXPORTER_OTLP_ENDPOINT
    headers:
      x-tenant: $OTEL_TENANT
```

This is useful when the same configuration is reused across environments.

## Exporter authentication

Credentials can be read from files, including Docker Secrets mounted under `/run/secrets`.

### Bearer token

```yaml
tracing:
  transport: http
  exporter:
    endpoint: https://otel.example.com
    authentication:
      bearerPath: /run/secrets/otel-bearer
```

This sends:

```text
Authorization: Bearer <token>
```

### API key

```yaml
tracing:
  transport: http
  exporter:
    endpoint: https://otel.example.com
    authentication:
      apiKeyPath: /run/secrets/otel-api-key
```

This sends:

```text
Authorization: Api-Key <token>
```

### x-api-key

```yaml
tracing:
  transport: http
  exporter:
    endpoint: https://otel.example.com
    authentication:
      xApiKeyPath: /run/secrets/otel-x-api-key
```

This sends:

```text
x-api-key: <token>
```

`bearerPath` and `apiKeyPath` cannot be used together. `Authorization` is reserved for the authentication settings above and cannot be configured through `exporter.headers`.

## What is traced

Tracing covers the main swarm-deploy execution path, including:

- GitOps synchronization and stack reconciliation;
- stack deployment;
- Git repository access;
- Compose loading and filesystem reads;
- Docker Swarm service and network operations;
- managed network reconciliation;
- event dispatching and notifications;
- web requests to the UI/API server.

Spans include contextual attributes such as stack names, service names, network names, Git commits, and sync triggers where they are relevant.

## Resource attributes

swarm-deploy builds its OpenTelemetry resource from the standard OpenTelemetry SDK environment configuration and from [`downward-otel`](https://github.com/swarm-deploy/downward-otel).

If Swarm Downward environment variables are available to the swarm-deploy process, `downward-otel` maps that runtime identity to OpenTelemetry resource attributes. This makes it easier to identify the Swarm service, task, and node that produced a trace.

Standard OpenTelemetry resource environment variables can also be used to add deployment-specific metadata such as `service.name` or environment information.

## Trace propagation

swarm-deploy uses W3C Trace Context and Baggage propagation for HTTP requests. This allows incoming trace context to be continued through swarm-deploy and correlated with spans created during request processing.
