## Project Context
- Name of project = Swarm Deploy
- This project implements Continuous Deployment for Docker Swarm

## Projects Rules
- Before starting any coding task, load and follow all files in `.cursor/rules/*.md`
- Treat frontmatter as policy:
- `apply: always` means rule is always active.
- `apply: by file patterns` + `globs` means apply only to matching files.
- `alwaysApply: true` means apply regardless of globs.
- In the first progress update, briefly state which `.cursor` rules were loaded.
- For Go changes, run `make check` before finishing. For UI changes, run `npm test` and `npm run build` in `ui/`. If a check cannot run, report why.
- If instructed to create a pull request, always assign appropriate repository labels to the PR before finishing the task. Use only the allowed labels from the file `.github/workflows/pr-labels.yaml`

## Architecture Decisions

**Agents MUST NOT make architectural decisions without explicit maintainer approval.**

- Preserve existing architecture, component boundaries, and established patterns.
- Prefer reusing existing components over introducing new abstractions.
- Local refactoring and implementation decisions are allowed if they do not change architectural responsibilities.
- Before making architectural changes, explain the motivation, alternatives, and trade-offs, then wait for approval.
- Approval of a task does not imply approval of architectural changes.

**When in doubt, ask before implementing.**

## Docker API Boundary

- **All new Docker Engine / Docker Swarm API operations MUST go through `internal/swarm`.** Other packages must use the managers exposed by `internal/swarm`; they must not call Docker SDK client methods directly or introduce parallel Docker API clients/adapters.
- If an operation is missing, extend the appropriate `internal/swarm` manager interface and implementation, including mocks and tests where relevant.
- Docker SDK **data types** (for example, `dockerswarm.ServiceSpec`, `mount.Mount`, and Docker network options) may be used directly by other packages. Do not introduce wrapper DTOs that merely duplicate these complex SDK types.
- Existing direct Docker API calls outside `internal/swarm` do not require unrelated refactoring in every task, but must not be copied or expanded. Migrate them when working on the corresponding integration or in an explicitly scoped refactor.

## Project structure
- `./ui` - Vue frontend
- `./internal/config` - YAML runtime configuration
- `./internal/modules/gitops` - GitOps orchestration, reconciliation, completion decisions, and recovery orchestration
- `./internal/modules/deployments` - Deployment execution, lifecycle persistence, domain events, and desired-state snapshots
- `./internal/modules/event` - Event Bus, Outbox, and Event History
- `./internal/modules/alertmanagement` - Alert Management
- `./internal/modules/notifications` - Notification delivery
- `./internal/modules/assistant` - AI assistant, routing, and tools
- `./internal/modules/resources` - Resource catalog
- `./internal/entrypoints/health` - Health and metrics server
- `./internal/entrypoints/webserver` - Web UI and REST API server
- `./internal/entrypoints/webhookserver` - Webhook server
- `./internal/swarm` - Docker Swarm API integration
- `./internal/storage` - SQLite persistence
- `./internal/metrics` - Application metrics
- `./api/api-server.yaml` - OpenAPI contract for the web API

## Assistant Tools

Built-in assistant tools are implemented in Go under
`./internal/modules/assistant/tools/`.

Tool names generally follow `{module}_{entity}_{action}`.
For example, the tool for listing Docker networks is `docker_network_list`.
Consult the existing tool definitions and registration for actual names.

