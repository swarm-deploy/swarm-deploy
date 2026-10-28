# Authentication

swarm-deploy can protect the web UI and REST API with built-in HTTP Basic authentication or delegate authentication to a trusted reverse proxy.

Authentication is configured under `web.security.authentication`.

When `web.basePath` is configured, authentication covers the UI and API below that prefix and the authentication session cookie is scoped to the same path.

## HTTP Basic authentication

Basic authentication is useful for small or internal installations where a separate identity-aware proxy is not needed.

```yaml
web:
  security:
    authentication:
      basic:
        htpasswdFile: /run/secrets/swarm-deploy.htpasswd
```

`htpasswdFile` must contain bcrypt password hashes. Mounting the file as a Docker Secret is recommended.

For example, create a bcrypt entry with:

```bash
htpasswd -nB admin
```

Requests to the UI and API must then use HTTP Basic credentials from that file.

## Trusted reverse-proxy authentication

When swarm-deploy is deployed behind an authentication proxy or SSO gateway, authentication can be delegated to that proxy.

```yaml
web:
  security:
    authentication:
      authProxy:
        loginHeader: X-Forwarded-User
```

The configured header must contain the authenticated user's login. swarm-deploy uses this value as the current user identity for authenticated requests and audit events.

This mode trusts the reverse proxy completely. The swarm-deploy web server must not be reachable directly by untrusted clients, and the proxy must remove or overwrite the configured login header so clients cannot spoof another identity.

This setup can be used with forward-auth and identity-aware proxies that authenticate users before forwarding requests to swarm-deploy.

## No authentication

If neither `basic.htpasswdFile` nor `authProxy.loginHeader` is configured, the UI and API are served without authentication.

For exposed or shared environments, configure one authentication method or place swarm-deploy behind another trusted access-control layer.

The task details endpoint (`GET /api/v1/swarm/tasks/{taskID}`) exposes the environment captured in the inspected Docker task. Sensitive values are Base64-encoded for safe default presentation, but Base64 is not encryption and callers can recover the original values. Installations that serve the Web API without authentication must restrict access at the network or proxy layer.
