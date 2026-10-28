# Webhooks

swarm-deploy can trigger reconciliation from incoming webhooks when `sync.mode` is `webhook` or `hybrid`.

The webhook endpoint is configured under `sync.webhook`:

```yaml
sync:
  mode: hybrid
  webhook:
    enabled: true
    address: :8082
    path: /api/v1/webhooks/git
    maxBodyBytes: 1048576
    rateLimit:
      requestsPerSecond: 5
      burst: 10
    auth:
      - type: github
        secretPath: /run/secrets/github-webhook-secret
      - type: header
        header: X-Swarm-Deploy-Secret
        secretPath: /run/secrets/swarm-deploy-webhook
      - type: bearer
        secretPath: /run/secrets/swarm-deploy-webhook
```

## Authentication

`auth` is an explicit list of accepted authentication methods. The list uses **any-of** semantics: a request is accepted when at least one configured method succeeds.

swarm-deploy never selects an authentication method based on headers that happen to be present in the incoming request. Only methods configured by the operator are evaluated.

Supported methods:

- `github`: validates `X-Hub-Signature-256` using HMAC-SHA256 over the raw request body and the configured secret.
- `header`: compares the configured request header with the configured secret.
- `bearer`: validates `Authorization: Bearer <token>` against the configured secret.

The old top-level `sync.webhook.secretPath` setting is not supported. Authentication must be configured explicitly through `sync.webhook.auth`.

## Rate limiting

The webhook endpoint uses a global token-bucket rate limiter.

- `requestsPerSecond` controls the refill rate.
- `burst` controls the maximum burst size.
- Requests over the limit receive HTTP `429 Too Many Requests`.

The rate limit is applied before the request body is read or authentication is evaluated.

## Request body limit

`maxBodyBytes` limits how much request body data swarm-deploy reads. Requests exceeding the limit receive HTTP `413 Request Entity Too Large`.

The default is 1 MiB.

For GitHub authentication, the body is read only to validate the HMAC signature. swarm-deploy does not parse GitHub event payloads or use `X-GitHub-Event` to decide whether to trigger reconciliation.

After successful authentication, the existing webhook flow schedules a normal webhook reconciliation, including pulling the configured Git repository.
