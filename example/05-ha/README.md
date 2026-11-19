# Optional HA experiments

This directory is reserved for **experimental**, opt-in high availability deployment examples for swarm-deploy.

The default installation remains unchanged: swarm-deploy uses a local SQLite database, without extra services, libraries, or a mandatory HA layer. See [the basic example](../01-basic).

## Database configuration

For an optional, external SQLite storage arrangement, the application supports this startup configuration:

```yaml
system:
  database:
    dsnPath: /run/secrets/dsn
```

`dsnPath` is the path to a mounted file containing the **complete SQLite driver DSN**, not the path to the SQLite database file. This setting is optional; when absent, swarm-deploy continues to use its local database in the default data directory. File contents are read on startup. Provide the required SQLite PRAGMA options in a custom DSN (see [SQLite storage documentation](../../docs/operations/sqlite.md#optional-sqlite-dsn)).

This setting alone does **not** implement replication, failover, shared controller ownership, or support for a different database driver. The existing controller lock remains tied to the local data directory.

## Constraints

- HA infrastructure is provisioned and operated separately. No example in this directory is required for a standard swarm-deploy deployment.
- Keep the existing SQLite driver and application transaction API. In particular, examples must preserve interactive transactions across multiple queries (for example, `BEGIN → SELECT → application logic → UPDATE → COMMIT`).
- Do not add a new application database driver, embedded consensus library, or required runtime dependency.
- Treat database replication/failover and coordination of swarm-deploy reconcilers as separate problems. Database HA alone does not make multiple active controllers safe.
- For active/passive setups, document ownership of the active controller, fencing, and prevention of split-brain.
- Do not expose a SQLite WAL database for concurrent access across hosts on a generic network filesystem.

## Experiment requirements

Each proposed approach should have its own subdirectory and document:

1. External infrastructure requirements, supported environment, and exact startup/teardown commands.
2. How data is persisted and replicated; whether writes are synchronous or asynchronous.
3. How failover and recovery work, including RPO/RTO limitations and possible data loss.
4. What happens to in-flight transactions and how the active controller is selected.
5. Tests for interactive transactions, node failure, network partition, and recovery.
6. Known limitations and which behaviors were actually verified.

**No HA implementation is included yet.** Do not treat this directory as a production-ready HA deployment guide until an approach has been tested and documented.
