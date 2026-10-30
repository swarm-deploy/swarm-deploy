# Deployments Module

This file supplements the repository-level `AGENTS.md`.

## Responsibilities

The Deployments module owns:

- Deployment and Attempt domain models.
- Deployment and Attempt creation, retries, and supersession.
- Desired-state snapshots and change comparison.
- SQLite persistence for deployments and attempts.
- Successful deployment baselines.
- Deployment history queries.
- Stack and service deployment execution.
- Docker CLI invocation, Init Jobs, and resource reconciliation.
- Deployment secret resolution and execution tracing.

## Boundaries

The module MUST NOT:

- Depend on GitOps.
- Perform Git synchronization.
- Implement GitOps reconciliation or drift detection.
- Orchestrate deployment completion or reconciliation.
- Introduce its own transaction infrastructure.

Deployment completion, startup recovery orchestration, and the broader reconciliation lifecycle remain in GitOps.

## Persistence Rules

- Use the existing storage and transaction infrastructure.
- Preserve atomic database operations.
- Preserve compatibility with existing SQLite data.
- Never persist plaintext secrets, except for maintainer-approved original Docker Init Job logs stored only in
  `deployment_init_jobs`. This narrow exception does not apply to snapshots, diagnostics, events, tracing, or other data.
- Keep snapshot sanitization and deterministic comparison behavior.

## Development Rules

- Follow the root `AGENTS.md`.
- Preserve existing behavior unless explicitly requested otherwise.
- Do not change architectural responsibilities without maintainer approval.
- Prefer existing components over new abstractions.
- Add tests for model and repository changes.
- Keep the implementation minimal.
