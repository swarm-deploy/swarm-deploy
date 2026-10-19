# SQLite storage cutover

SQLite is the only supported persistent application store. This is an
intentional breaking change: data from the previous JSON repositories is not migrated.

At startup, swarm-deploy:

1. creates or opens `<dataDir>/swarm-deploy.sqlite`;
2. applies embedded Goose migrations;
3. initializes modules and workers against that database.

It does not inspect, parse, import, update or delete old files such as
`controller.state.json`, `event-history.json`, `alerts.state.json`,
`recommendations.state.json`, `nodes.json`, `services.json`,
`secrets.state.json` or `assistant/chats/*.json`. Those files may be retained as
an operator-owned archive.

A clean SQLite database therefore starts with empty Events, Alerts,
Recommendations, Assistant history, resource projections and Deployment history.
No previous Deployment or successful desired baseline is reconstructed from JSON or
from the current Docker Swarm state.

## Schema lifecycle

The complete initial schema is in `migrations/sqlite/0001_initial.sql`. Goose applies it on a new database and records
version 1 in `goose_db_version`. Restarting with an existing database preserves
its state and applies only pending future migrations. This squashed baseline does not
upgrade databases that stopped at an older schema version; migrate those databases
through the previous release before deploying this one.

Back up the complete data directory before rollout. Do not delete the SQLite database
or edit Goose migration history to bypass a startup failure.

Migration `0002_deployment_attempts.sql` is forward-only and strictly additive. It
only creates `deployment_attempts` and its indexes; it does not alter or backfill
existing tables. Historical Deployments therefore have no Attempts. The application
reads them from the original Deployment fields, derives supersession from newer
Deployments in the same stack, and starts numbering newly executed retries at one.
Legacy `running` Deployments receive an Attempt only when the new controller recovers
and interrupts that in-flight execution.

Application rollback across this migration is supported: stop the newer task and
run the older binary against the same additive schema. The older binary ignores the
new table and continues reading and writing `deployments`; records it creates remain
readable by the new binary without synthetic historical Attempts. Schema downgrade is
intentionally unsupported. Do not run Goose down or remove Attempt history. Restore
a pre-upgrade backup only when a true database rollback is unavoidable, accepting
that all data committed after that backup will be absent.

## Consistent backup and incident evidence

Do not copy only `swarm-deploy.sqlite` while the application is running: committed data may still be in
`swarm-deploy.sqlite-wal`. Prefer SQLite's online backup API, for example from a host with `sqlite3`:

```shell
sqlite3 /path/to/data/swarm-deploy.sqlite ".backup '/safe/path/swarm-deploy-before-upgrade.sqlite'"
sqlite3 /safe/path/swarm-deploy-before-upgrade.sqlite "PRAGMA integrity_check;"
```

If an online backup is unavailable, stop the process cleanly and copy the database together with any `-wal` and `-shm`
files as one set before it is restarted. Retain the original files unchanged. Run diagnostic queries against the backup,
or open the live database using `mode=ro` and immediately enable `PRAGMA query_only=ON`.

Before restarting a controller with a stuck deployment, capture the following read-only evidence because the pending
completion itself is process memory and disappears with the process:

- current and previous Swarm task/container IDs, node IDs, start times, desired/current states, and application logs;
- `/api/v1/deployments` plus `/api/v1/deployments/{id}` for every relevant Deployment ID and its nested Attempts;
- the controller runtime sync reason, revision, result, and full recovery error;
- `deployments` rows plus `deployment_attempts`, including statuses, phases, timestamps, commit, error codes,
  Deployment/Attempt IDs, and the `details_payload` needed to diagnose each execution;
- `desired_snapshots`, matching `outbox_events`/`outbox_deliveries`, and the current Git revision/digest;
- the consistent SQLite backup and its integrity-check result.

Do not confuse the Deployment ID used by UI routes with the Attempt ID retained for completion recovery. Correlate both IDs,
commit, timestamps, logs, and persisted status.

## Controller ownership and recovery

The deployment controller holds `swarm-deploy-controller.lock` beside the SQLite database for its entire active
lifetime. A second `start-first` task may serve health/UI traffic, but it cannot reconcile, apply, or interrupt a
deployment until it owns that lock. Clean shutdown keeps the lock while an in-flight reconciliation gets a bounded
grace period. Process exit closes the descriptor automatically; the next owner then atomically marks genuine orphaned
`running` attempts `interrupted` exactly once before its startup reconciliation.

This mechanism requires one replica pinned to one manager and the same node-local `dataDir` volume for overlapping
old/new tasks. Label exactly one manager (for example `swarm-deploy.data=true`) and constrain the service to that label.
SQLite WAL on a multi-host/network filesystem and replicas with separate local volumes are unsupported. During the
first upgrade from a version without this lock, capture the evidence and backup above, then use a one-time `stop-first`
update or otherwise prove the old task has stopped; later upgrades can use `start-first` safely.

On restart, the lock owner marks genuinely orphaned `running` Attempts
`interrupted` before reconciliation. Already-terminal rows remain unchanged. A
retry receives a new Attempt ID under the same unchanged Deployment; only a successful Attempt advances the desired
baseline. If recovery fails, preserve the database and diagnostic evidence rather
than editing deployment or Goose rows by hand.

## First deployment

The first real apply attempt has `comparison_basis=none` because no successful
desired snapshot exists. The UI presents this as **Initial Deployment Snapshot** and
shows the safe recorded target configuration without claiming that every field or
resource was newly created in Swarm. A successful apply still does not prove service
health or live convergence.

Later Deployments with a trustworthy comparison basis show the normal structured
`added` / `changed` / `removed` semantic diff.

## Deployments and eventual consistency

Deployment completion and its Outbox publication commit atomically. Event History,
Alerts, Recommendations and other subscribers process that publication later in
separate transactions. A successful Deployment means that the apply pipeline
completed; it does not mean that live Swarm convergence was verified.
