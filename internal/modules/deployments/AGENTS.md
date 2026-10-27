# Deployments Module

This file supplements the repository-level `AGENTS.md`.

## Responsibilities

The Deployments module owns:

- Deployment and Attempt domain models.
- Desired-state snapshots and change comparison.
- SQLite persistence for deployments and attempts.
- Successful deployment baselines.
- Deployment history queries.

## Boundaries

The module MUST NOT:

- Depend on GitOps.
- Perform Git synchronization.
- Execute Docker operations.
- Implement reconciliation or drift detection.
- Orchestrate deployment lifecycle transitions.
- Introduce its own transaction infrastructure.

Deployment lifecycle orchestration currently remains in GitOps.

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
