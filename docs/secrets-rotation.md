# Secrets rotation

Rotation is done by changing `configs/secrets.*.name` to `stack-object-hash` when a source file changes.

Benefits:
- services are guaranteed to receive a new object version when a file changes.

Limitations:
- old objects are not removed automatically (a separate cleanup strategy is required),
- this is not cryptographic key rotation, but rotation of the object **name** to force rollout.

This project implements the same idea (hash-based naming), but with SHA-256.

## SOPS-encrypted secrets

swarm-deploy can decrypt SOPS-encrypted secret files as part of secret rotation.

SOPS support is explicit:

```yaml
secretRotation:
  enabled: true
  hashLength: 10
  includePath: true
  sops:
    enabled: true
    age:
      keyFile: /run/secrets/swarm-deploy-sops-age-key
```

Only Docker secrets are supported. Config files are not decrypted with SOPS.

### Naming convention

A secret file is treated as SOPS-encrypted **only** when its file name ends with `.sops`.

```yaml
secrets:
  openai-api-key:
    file: ./secrets/openai-api-key.sops
```

swarm-deploy does not inspect file contents to detect SOPS encryption.

- `*.sops` + `secretRotation.sops.enabled: true` -> decrypt with SOPS,
- `*.sops` + SOPS disabled -> reconciliation fails,
- every other secret file -> existing plaintext file behavior.

The convention intentionally uses the SOPS binary store so the decrypted result is exactly the original secret bytes.

For example:

```bash
sops encrypt \
  --filename-override secrets/openai-api-key.sops \
  --input-type binary \
  --output-type binary \
  secrets/openai-api-key > secrets/openai-api-key.sops
```

### age key

The age private key is a bootstrap credential and must be provisioned independently of swarm-deploy.

A typical Docker Swarm setup is:

```bash
docker secret create swarm-deploy-sops-age-key ./age-key.txt
```

and mount it into the swarm-deploy service at the path configured in `secretRotation.sops.age.keyFile`.

The public age recipient can be stored in Git, for example in `.sops.yaml`.

### Reconciliation

For a `*.sops` secret, swarm-deploy:

1. decrypts the source with the configured age key,
2. calculates the rotation hash from the **decrypted** bytes,
3. temporarily materializes the decrypted bytes in a `0600` file for `docker stack deploy`,
4. points the rendered Compose secret at that temporary file,
5. removes the temporary file when reconciliation finishes.

Hashing plaintext means re-encrypting an unchanged secret does not trigger a new Docker Secret version merely because the SOPS ciphertext changed.

The decrypted secret is never written back into the Git checkout or persisted in the rendered Compose file itself; only the temporary file path is present there.
