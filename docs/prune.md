# Service Prune Policy

When `sync.policy.prune=true`, swarm-deploy removes orphaned services that are no longer defined in desired compose, but only for services marked as managed in live state (`org.swarm-deploy.service.managed=true`).

Prune policy priority (highest to lowest):

1. Service label `org.swarm-deploy.service.sync.policy.prune` (from compose `deploy.labels`).
2. Stack `stacks[].sync.policy.prune`.
3. Global `sync.policy.prune`.
