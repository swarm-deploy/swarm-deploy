# Secrets rotation

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
    interval: 10m
    keepLast: 2
    minAge: 1h
```

Cleanup is stateless and runs after orphaned service pruning. Periodic cleanup is throttled by `interval`; a new desired digest or manual sync triggers cleanup immediately. Config and secret metadata are loaded from Docker only when cleanup actually runs, so regular reconciliations do not issue cleanup-specific `ConfigList`/`SecretList` calls.

Cleanup uses resource `CreatedAt` metadata and config/secret IDs referenced by both the current service spec and Swarm's previous spec used for rollback. A generation is preserved when it is the current desired resource, is still referenced by a service or rollback spec, is among the newest `keepLast` generations, or is younger than `minAge`.

Cleanup is best effort. A failed Docker remove is logged and does not fail reconciliation or prevent cleanup of other resources. There is no cleanup-specific retry or backoff; the next cleanup interval, new digest, or manual sync evaluates the resource again. This also covers the expected race where Docker still reports a resource as in use shortly after a service was pruned.

Defaults are `interval: 10m`, `keepLast: 2`, and `minAge: 1h`. Cleanup requires rotation to be enabled. External resources, external-driver secrets, and resources without the managed rotation label are never removed. If ownership, desired state, or live references cannot be determined safely, cleanup fails closed and reconciliation continues.

Limitations:
- this is not cryptographic key rotation, but rotation of the object **name** to force rollout.

This project implements the same idea (hash-based naming), but with SHA-256.
