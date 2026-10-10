# SQLite storage cutover

PR #123 makes SQLite the only supported persistent application store. This is an
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

The integration branch contains one consolidated
`migrations/sqlite/0001_initial.sql`. Goose applies it on a new database and records
the version in `goose_db_version`. Restarting with an existing database preserves
its state and applies only pending future migrations.

Back up the complete data directory before rollout. Do not delete the SQLite database
or edit Goose migration history to bypass a startup failure.

## First deployment

The first real apply attempt has `comparison_basis=none` because no successful
desired snapshot exists. The UI presents this as **Initial Deployment Snapshot** and
shows the safe recorded target configuration without claiming that every field or
resource was newly created in Swarm. A successful apply still does not prove service
health or live convergence.

Later attempts with a trustworthy comparison basis show the normal structured
`added` / `changed` / `removed` semantic diff.

## Deployments and eventual consistency

Deployment completion and its Outbox publication commit atomically. Event History,
Alerts, Recommendations and other subscribers process that publication later in
separate transactions. A successful Deployment means that the apply pipeline
completed; it does not mean that live Swarm convergence was verified.

## Outbox operations

The `sd` CLI exposes safe operational commands:

- `outbox-list <dataDir>`
- `outbox-replay <dataDir> <eventID> <subscriptionID>`
- `outbox-discard <dataDir> <eventID> <subscriptionID>`

These commands expose identifiers and delivery state, not persisted event payloads.
