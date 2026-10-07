# Managed Docker Networks

swarm-deploy can keep shared Docker networks in Git together with the stacks that use them. This is useful for networks that must exist before stack deployment or are shared by services from multiple stacks.

Configure the network definition file in `swarm-deploy.yaml`:

```yaml
networks:
  file: ./networks.yaml
```

The file is resolved from the synchronized Git repository and contains the desired networks:

```yaml
networks:
  - name: shared-backend
    driver: overlay
    attachable: true
    internal: false
    labels:
      team: platform
    options:
      encrypted: "true"
```

`driver` defaults to `overlay` when omitted.

Supported fields are:

- `name` — Docker network name;
- `driver` — network driver;
- `attachable` — whether standalone containers can attach to the network;
- `internal` — whether the network is internal-only;
- `labels` — additional Docker network labels;
- `options` — driver-specific options.

## Reconciliation

During synchronization, swarm-deploy creates a desired network if it does not exist.

Networks created and managed by swarm-deploy receive the label:

```text
org.swarm-deploy.network.managed=true
```

If a network with the same name already exists but does not have this managed label, swarm-deploy does not adopt or modify it and reports an error instead.

For an existing managed network, swarm-deploy checks the driver, `attachable`, `internal`, desired labels, and desired driver options. A mismatch is reported as network drift rather than automatically recreating the network.

Extra labels or driver options that are not declared in the desired configuration do not cause drift.

## Removing networks

Removing a network from `networks.yaml` does not delete the Docker network from the cluster. Network deletion is intentionally not part of the current reconciliation behavior and must be performed separately when it is safe to do so.
