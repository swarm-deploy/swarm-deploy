# SQLite integration branch: storage and migration

The integration branch uses SQLite repositories, a single durable asynchronous
Outbox and first-class Deployment/DesiredSnapshot records. Review the local
[verification ledger](../docs/architecture/sqlite-outbox-progress.md) before rollout;
a successful local build is not a live Swarm rollout test.

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

The integration branch contains one consolidated `0001_initial` migration. A schema checksum
mismatch deliberately refuses startup. Use disposable copies of data for testing;
do not delete a real database or edit its checksum to bypass this protection.

## Deployments and eventual consistency

`GET /api/v1/deployments?limit=20&stack=app` returns recent apply attempts, newest
first. `GET /api/v1/deployments/{id}` includes safe planned field-level changes.
Overview uses this repository, not event history. Failed attempts do not advance
the successful desired baseline; an interrupted process leaves an interrupted attempt
after restart. A completion write that fails after Docker returns is retried by the next
reconciliation in the same process. If the outcome is unknown, the running attempt is
atomically marked interrupted before retry evaluation. Preparation errors and effective
no-op reconciliations create no attempt, but preparation failures still update runtime
and publish a secret-safe durable signal for Alerts and Notifications.
Returning to the last successful desired state after a failed or interrupted attempt
creates a new apply attempt because Swarm may have been changed by the later attempt.
The first comparison after JSON import uses the legacy source digest only to decide
whether there is a new attempt; it does not invent a historical successful snapshot.

Success means that apply/init/prune completed, not that Swarm service health converged.
Cleanup errors after success are maintenance diagnostics and cannot reverse success.
Events/Alerts/notifications appear asynchronously. A projection failure never reverses
the committed deployment.

Persisted commands, entrypoints, healthchecks and init-job scripts are opaque masked
values. Their unkeyed fingerprints preserve change detection without retaining plaintext.
Compose scalar and list command forms remain distinct when their execution semantics differ.

## Outbox operations

The image includes the `sd` administrative CLI. Run it against the same local volume
and application version. These commands refuse a missing database and never initialize
one based on an operator typo:

```sh
sd outbox-list /data
sd outbox-replay /data EVENT_ID SUBSCRIPTION_ID
sd outbox-discard /data EVENT_ID SUBSCRIPTION_ID
```

List output contains only failed delivery metadata and stable error categories, not
payloads or credentials. Replay retries exactly one failed destination; successful
siblings stay delivered. Discard permanently waives that failed delivery and, if it
was the last unfinished delivery, removes its event and all delivery rows immediately.
There is no outbox retention period. Discard cannot be undone without a backup.

For a running container, use `docker exec CONTAINER sd outbox-list /data` with your
actual configured data directory. Never run a second controller for the same database.
Monitor the outbox Prometheus queue metrics and logs containing event/subscription IDs.
Removing or renaming a notification destination leaves existing work visible as a
missing subscription, not silently discarded. Restore the matching configuration
before replay, or explicitly discard obsolete work.

Notifications remain at-least-once: a crash after a remote send and before ack can
repeat that send. No recursive `sendNotificationFailed` publications are generated;
that event type remains parseable for existing notification configurations.
