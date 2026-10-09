# SQLite integration branch: storage and migration

This branch is **not release-ready**. SQLite repositories and legacy import are
connected, but the old event queue is still used. Durable Outbox delivery and the
Deployment/DesiredSnapshot model are pending.

The existing `dataDir` setting determines the database location:
`<dataDir>/swarm-deploy.sqlite`. No DSN, database password or additional service is
required. Use a persistent **local** volume and one active controller instance.
Do not share a WAL database between hosts or run old and new application versions
against the same directory.

At startup, before collectors or controllers run, the application validates:

- `controller.state.json`, `event-history.json`, `alerts.state.json`;
- `recommendations.state.json`, `nodes.json`, `services.json`;
- `secrets.state.json`;
- `assistant/chats/index.json` and all chat JSON files.

All records and the import-completed marker commit in one transaction. Invalid
JSON, missing indexed chats, duplicate identities or a SQL error abort startup.
Fix the reported source issue and restart; failed imports leave no partial rows.
An unmarked nonempty database is rejected rather than overwritten.

Original JSON files are never changed by the importer. Once the marker exists,
SQLite is authoritative; the old JSON files are not updated and are not reimported.
Historical Events do not create synthetic Deployments.

Before testing this branch on existing data, stop the old instance and take a
complete offline backup of its data directory. For later backups, stop the new
instance cleanly and back up the whole directory, including SQLite and any
`-wal`/`-shm` files. Do not copy only the main database file from a running writer.
For rollback, stop the new instance and restore a known complete offline backup;
starting the old binary against stale JSON would lose changes made after migration.

During development, `0001_initial` is still being consolidated. A schema checksum
mismatch deliberately refuses startup. Use disposable copies of data for testing;
do not delete a real database or edit its checksum to bypass this protection.
