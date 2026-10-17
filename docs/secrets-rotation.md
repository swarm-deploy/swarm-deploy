# Secrets rotation

## age-encrypted secrets

Docker Secrets can reference age-encrypted files using standard Compose syntax:

```yaml
services:
  database:
    image: postgres:17
    secrets:
      - db_password
secrets:
  db_password:
    file: ./secrets/db_password.age
```

Paths are resolved relative to the source Compose file, including when a rendered
Compose is used for deployment. Absolute paths are also supported. Only local
Secrets with the `.age` extension are decrypted; Configs and other secret files
retain their existing behavior. External Secrets are reused unchanged. A secret
file cannot be combined with an external secret driver.

Mount an age identity file into swarm-deploy and configure its path:

```yaml
secretRotation:
  enabled: true
  age:
    identityFile: /run/secrets/swarm_deploy_age_identity
```

The file uses the native age identity format: one private identity per line,
with optional blank lines and `#` comments. Binary and ASCII-armored age files
are supported. SSH identities, passphrase-encrypted identities and age plugins
are not supported. Provision the identity file separately from the manifests
repository; do not commit private keys alongside the encrypted files. The file
is read when swarm-deploy loads its configuration; restart after replacing it.

Decryption works even with `secretRotation.enabled: false`. A dedicated
`SecretResolver` reads files through the shared, traced filesystem
and selects the first matching decoder (age, then plaintext). It authenticates
and decrypts file-backed Secrets before the Docker API reconciler runs, including
when an existing Docker Secret will be reused. The resource reconciler receives
only resolved bytes and does not know their encryption format. Init Jobs and
regular services use the resulting Docker Secret IDs.

The Docker CLI receives external references for **all API-managed Secrets**,
including plaintext and driver-backed Secrets, preserving names and mount options.
Mutable labels of reused API-managed Secrets are synchronized through the Docker
API only when their `com.docker.stack.namespace` matches the current stack.
Conflicting or missing ownership fails reconciliation without changing Secret
labels; external Secrets remain untouched. Sanitized API failures retain their
`Temporary()` classification for Alert Management. No decrypted file is written to disk or
embedded in rendered
Compose, deployment snapshots, logs, traces or errors. The original Compose and
encrypted file remain unchanged. Decrypted payloads must fit Docker's 500 KiB
secret limit. Invalid keys, ciphertext and authentication failures stop creation
and deployment without including their contents in diagnostics.

Run deployment through swarm-deploy: running `docker stack deploy` directly
against the original Compose will not decrypt `.age` files.

Rotation and source digests use the **encrypted file bytes**. Changing or
re-encrypting a file produces a new name when rotation is enabled; unchanged
ciphertext reuses the existing Secret. Existing rotation ownership labels and
cleanup rules apply to age Secrets too. With rotation disabled, Docker's
immutable existing Secret is reused, so changing ciphertext does not update its
value. Enable rotation to roll out changed secret values. Plaintext Secrets can
coexist with age Secrets in the same stack.

Unit tests run with `make check`. Real Swarm E2E tests exercise the production
GitOps pipeline, mounted secret contents in services and Init Jobs, validation
before reuse, rotation and cleanup:

```sh
docker swarm init
docker pull busybox:1.36
SWARM_DEPLOY_E2E=1 go test -count=1 -timeout=10m -v ./e2e
```

## Name rotation

Rotation is done by changing `configs/secrets.*.name` to `stack-object-hash` when a source file changes.

Benefits:
- services are guaranteed to receive a new object version when a file changes.

Rotated resources receive ownership labels in the `org.swarm-deploy.rotation.*` namespace. Cleanup only considers resources carrying these labels and the matching Docker stack namespace. Legacy rotated resources created before these labels were introduced are never adopted or removed automatically.

```yaml
secretRotation:
  enabled: true
  hashLength: 8
  includePath: true
  cleanup:
    enabled: true
    interval: 4h
    keepLast: 2
    minAge: 1h
```

Cleanup is stateless. A dedicated controller ticker runs periodic cleanup every `interval` without running the regular reconciliation pipeline. A new desired digest or manual sync also triggers cleanup immediately after reconciliation. Config and secret metadata are loaded from Docker only when cleanup actually runs, so regular interval reconciliations do not issue cleanup-specific `ConfigList`/`SecretList` calls.

Cleanup uses resource `CreatedAt` metadata and config/secret IDs referenced by both the current service spec and Swarm's previous spec used for rollback. A generation is preserved when it is the current desired resource, is still referenced by a service or rollback spec, or is younger than `minAge`. While the logical config or secret still exists in desired state, the newest `keepLast` generations are also preserved. If the logical resource is removed from desired state, `keepLast` no longer applies, so unreferenced generations become eligible for cleanup after `minAge`.

Cleanup is best effort. A failed Docker remove is logged and does not fail reconciliation or prevent cleanup of other resources. There is no cleanup-specific retry or backoff; the next cleanup interval, new digest, or manual sync evaluates the resource again. This also covers the expected race where Docker still reports a resource as in use shortly after a service was pruned.

Defaults are `interval: 4h`, `keepLast: 2`, and `minAge: 1h`. Cleanup requires rotation to be enabled. External resources, external-driver secrets, and resources without the managed rotation label are never removed. If ownership, desired state, or live references cannot be determined safely, cleanup fails closed and reconciliation continues.

Limitations:
- this is not cryptographic key rotation, but rotation of the object **name** to force rollout.

This project implements the same idea (hash-based naming), but with SHA-256.
