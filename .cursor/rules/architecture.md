---
name: architecture
description: Existing module responsibilities, dependency boundaries, and architectural constraints for all coding tasks
apply: always
---

# Architecture boundaries

Read this rule before modifying code. Identify the affected modules, their callers,
and the contracts being used before choosing where an implementation belongs.
This describes the current architecture, not a redesign or a future deployment mode.

Responsibilities and interactions below describe **current implementation**. Invariants
preserve **established decisions** from repository instructions, documented contracts,
and their implementation. **Known issues** explicitly identify violations or boundaries
requiring maintainer clarification; their presence is not permission to copy or expand them.
Changes to ownership, dependency direction, or architectural contracts require explicit
maintainer approval. Approval of a task does not imply approval of architectural changes.

All eight directories under `internal/modules` are covered below. Supporting boundaries
are documented separately. `cmd/swarm-deploy/main.go` and each module's `module.go`
assemble dependencies through module-specific inputs. An exported Go package or a shared
database does not by itself authorize a new cross-module dependency.

## GitOps

Path: `internal/modules/gitops`.

### Responsibilities

- Own Git access, revision-pinned source reads, sync scheduling, stack queue admission, reconciliation, drift detection, and self-heal decisions.
- Prepare effective desired state: image policy, env files, bind paths, Downward injection, managed labels, and config/secret name rotation.
- Own GitOps runtime/sync state, configured-network reconciliation, and rotated-resource cleanup scheduling.

### Non-responsibilities

- Deployment/Attempt creation, terminal outcome selection, retry eligibility, and interrupted-Attempt recovery belong to Deployments.
- Native application ServiceSpec construction and service application belong to the Deployments execution pipeline.

### Dependencies and Interactions

- `controller/stackloop/reconciler.go` uses `deployments.Executor.Execute` and `ExecuteService`; the public execution call is synchronous even though sync runs dispatch stacks asynchronously.
- `controller/controller.go` acquires process ownership before calling `Executor.Recover` and starting reconciliation. GitOps coordinates when recovery runs; Deployments performs it.
- `deployment/projection.go` consumes completion events and reads the Deployments repository. Reconciliation also updates GitOps runtime state directly for preparation failures, drift, and no-Attempt results.
- Uses `git.Repository`, `compose.FileLoader`, `configstore.ConfigStore`, Swarm managers, Event publication, shared storage, and metrics. `networkloop` handles networks from runtime configuration; Deployments handles Compose networks. No-Attempt pruning reuses `deployments/pruner.ServicePruner`.

### Architectural Invariants

- Preserve one active operation per stack, latest waiting revision semantics, and revision-pinned input; a later pull must not change an active operation's inputs (`docs/reconciler.md`).
- Deployment completion reaches consumer projections through events, never a callback from Deployments. A sync dispatch or preparation failure is not a completed deployment.
- Preserve source/effective-state separation and self-heal/externally managed replica policies. Do not move drift decisions into Deployments or execution lifecycle decisions into GitOps.

## Deployments

Path: `internal/modules/deployments`.

### Responsibilities

- Own Deployment/Attempt lifecycle, execution deadlines, retries, supersession, interrupted-execution recovery, history, successful baselines, sanitized desired snapshots, and comparisons.
- Own synchronous stack/single-service execution, secret decoding, Init Jobs, native resource reconciliation, service application, convergence observation, failure classification, and deployment-specific pruning.
- Within `deployer`, `converter` constructs final application specs; `networks` and `resources` reconcile dependencies; `services` applies specs; `initjob` owns temporary job services; `waiter` evaluates rollout results and diagnostics.

### Non-responsibilities

- Git synchronization, desired-state preparation policy, stack scheduling, drift detection, and consumer projections belong outside this module.
- Alert lifecycle, notifications, and catalog updates belong to event consumers.

### Dependencies and Interactions

- `controller.go` exposes `Executor` (`Execute`, `ExecuteService`, `Recover`); `service.go` and `completion.go` persist lifecycle state and publish Event contracts through shared storage transactions.
- `deployer/pipeline.go` consumes prepared in-memory Compose and Swarm manager interfaces; registry auth comes from `registry.AuthManager`. Single-service remediation does not create a stack Deployment or run Init Jobs.
- Module wiring still reads GitOps configuration fields; see known issues. Docker operations use Swarm managers. It does not import the GitOps module.

### Architectural Invariants

- Preserve `internal/modules/deployments/AGENTS.md`: no GitOps dependency, Docker CLI, direct Docker SDK operations, parallel transaction infrastructure, or consumer-projection callbacks.
- Persist an Attempt before external apply; commit terminal state, successful baseline when applicable, and completion publication atomically. Do not derive completion from Event History or catalog freshness.
- Preserve converter ownership and the native pipeline contract in `docs/compose-converter.md`; convergence semantics include specification-only verification for externally managed replicas, not a universal health check.
- Keep secret payloads transient and snapshots/diagnostics/events sanitized. The existing maintainer-approved exception is original Init Job logs stored only in `deployment_init_jobs`; it does not extend to other persistence or telemetry.

## Event

Path: `internal/modules/event`.

### Responsibilities

- Own event types/envelopes, safe versioned codecs, subscriber registration, durable Outbox publication/delivery, replay/discard, and independently retained Event History.
- Provide event metrics and user enrichment.

### Non-responsibilities

- Selecting deployment outcomes, deciding alert lifecycle, or coordinating transactions across business-module side effects.
- Event History is not the Deployment repository or the Outbox delivery queue.

### Dependencies and Interactions

- Publishers use `dispatcher.Dispatcher.Publish`; subscribers implement `Handle(context.Context, events.Envelope)` with stable subscription IDs. `dispatcher.External` marks network-I/O consumers.
- `outbox.Repository` uses shared storage; `outbox.Worker` processes committed deliveries. `history.SQLStore` and event metrics are ordinary subscribers.
- `module.go` takes the history capacity and user-enrichment setting from the composition root and wires Event's own history and metrics subscribers.

### Architectural Invariants

- Publication never invokes subscribers inline. Preserve publisher transaction, worker claim, and subscriber/ack transaction separation (`docs/architecture/event-bus-outbox-schema.md`).
- DB-only projections and their ACK commit together; external I/O runs outside the database transaction and is at-least-once. Consumer failure cannot roll back the publisher's committed outcome.
- Preserve stable IDs, codec/schema compatibility, ordering/deduplication guards, and safe payloads. Do not serialize arbitrary Compose, raw errors, secrets, or Docker objects into the Outbox.

## Alert Management

Path: `internal/modules/alertmanagement`.

### Responsibilities

- Turn deployment/preparation, Git access, node connectivity, configuration, and drift events into persisted incidents.
- Own fingerprints, failure streaks, occurrence counts, open/resolved state, Findings, and alert lifecycle publications.

### Non-responsibilities

- Executing deployments, observing Docker directly, repairing services, or delivering messages.
- Low-level failure classification belongs to the source error producers; this module extracts `faults.Coded` rather than reinterpreting Docker strings.

### Dependencies and Interactions

- `module.go` registers Event subscribers. `subscriber.go` uses alert repository contracts, a publisher, and `config.StackProvider`; wiring supplies ConfigStore and notification recipient configuration.
- `sqlite_subscriber.go` orders/deduplicates projections through `modelstore.SQLStore`. `AlertOpened`/`AlertResolved` snapshots feed Notifications; API and assistant query the alert store.

### Architectural Invariants

- Preserve transactional alert state, source receipt, and follow-up publication; delayed/replayed events must not regress newer incident state.
- Keep concise Finding summaries separate from technical diagnostics and preserve typed fault codes. Delivery failure must not redefine an alert or deployment outcome.

## Notifications

Path: `internal/modules/notifications`.

### Responsibilities

- Own Telegram/custom-webhook transports, notification routes, payloads and templates, transport receipts, and per-recipient alert message links/actions.
- Recover configured and persisted alert destinations and deliver opening/resolution transitions.

### Non-responsibilities

- Deciding whether an incident is open/resolved, determining deployment success, or implementing another durable event bus.

### Dependencies and Interactions

- The composition root initializes Notifications after Event and before the worker starts. Notifications registers event and alert deliveries as external subscribers through Event's dispatcher.
- `notifications/eventnotify` owns the mapping from events to notification payloads and the standard templates. Configuration validation uses its reference payloads.
- Alert notifications consume `AlertOpened` and `AlertResolved` and use Alert Management kind constants to choose message formatting. Notifications uses Event's Outbox inspection to recover subscriptions and shared storage for `notification_messages`. `alerts/store.go` also reads Outbox tables directly; see known issues.

### Architectural Invariants

- External sends run outside publisher/subscriber DB transactions. Preserve at-least-once delivery and receipt-based replay protection; do not claim exactly-once transport delivery.
- Preserve physical recipient identity and stored message links across configuration alias changes. Notification transport state does not own alert state.

## Resources

Path: `internal/modules/resources`.

### Responsibilities

- Own persisted node/secret observations, service metadata enrichment, known-app/type discovery, web routes, resource relations, and inferred service dependency graphs.
- Own discovery and supported controller calls for external Secret Managers (`secretmanager.Domain.List`, `Create`, `Sync`). External providers/controllers own their reconciliation.

### Non-responsibilities

- Git desired-state reconciliation, Deployment/Attempt lifecycle, or alert decisions. The dependency graph does not prescribe deployment ordering.
- `deployments/deployer/resources` owns deployment-time config/secret reconciliation; it is distinct from this catalog module.

### Dependencies and Interactions

- Node/secret collectors use Swarm managers and the shared Docker event stream. Node facts are published through Event.
- `module.go` subscribes service metadata to `DeploySuccess` via `gitops/deployment.WithDesired` and the Deployments store. `service/subscriber.go` inspects Docker outside a transaction, then persists the catalog and publishes `ServiceCatalogUpdated` atomically with its receipt.
- API/assistant use resource repositories and `graph.Builder`; Assistant RAG consumes catalog updates. Secret Manager calls use the discovered cloud-secrets gRPC adapter.

### Architectural Invariants

- Catalog projections are eventually consistent, not authoritative deployment completion or continuous live health. Preserve source receipts/current-baseline checks during replay.
- Use Swarm managers for Docker operations. Keep external secret creation and synchronization separate; `Create` does not imply `Sync`.
- The GitOps adapter dependency and subscriber-owned SQL are known issues, not patterns for new consumers.

## Recommendations

Path: `internal/modules/recommendations`.

### Responsibilities

- Analyze effective stack definitions for operational/configuration recommendations and replace the stored recommendations for that stack.
- Own analyzer composition, recommendation models, and their persistence/query contract.

### Non-responsibilities

- Current recommendation analysis does not apply fixes or edit Git. A future fix workflow may propose changes and create Git PRs after approval; it must not take over deployment execution, incident lifecycle, or failure diagnostics.

### Dependencies and Interactions

- `module.go` subscribes to `DeploySuccess`/`DeployFailed` via `gitops/deployment.WithDesired`, reading sanitized desired state from Deployments.
- `analyzer.StackAnalyzer` works on Compose-backed models; the scheduled-service analyzer consults configuration. `Recommender` writes through `modelstore.Store`; API/assistant read it.

### Architectural Invariants

- Analysis produces advice without mutating desired or live state. Preserve `sqlite_subscriber.go` ordering guards so delayed events cannot overwrite newer results.
- Do not turn the existing GitOps adapter import into ownership of recommendations by GitOps; its placement requires clarification.

## Assistant

Path: `internal/modules/assistant`.

### Responsibilities

- Own chat/request lifecycle, conversation history, model clients, routing/capabilities, RAG retrieval/indexing, prompt-injection checks, and built-in tool orchestration.
- Own deterministic pending-operation target resolution and confirmation for supported mutations.

### Non-responsibilities

- Reimplementing GitOps, deployment execution, incident classification, recommendation analysis, or resource collection.
- Model text does not determine whether a Docker operation or deployment actually succeeded.

### Dependencies and Interactions

- `module.go` assembles the `Assistant` service and tools; `internal/modules/assistant/tools/types.go` defines narrow consumer interfaces for readers, sync triggering, Git, registry, and hosting capabilities.
- Tools query module repositories, trigger GitOps `Manual`, or use Swarm managers for scale/restart/inspection. These runtime operations are distinct from Git-driven deployments and do not edit Git.
- `ServiceCatalogUpdated` drives an external RAG subscriber using Resources and embeddings. Conversation history uses shared SQLite; active requests, pending operations, and the index also have process-local state.

### Architectural Invariants

- Preserve capability/tool allowlists and `graph_tool.go`'s rejection of model-initiated mutations. Mutations go through `pending_operation.go` confirmation and target revalidation.
- Tools use existing module interfaces/managers and authoritative results; do not bypass them with SDK calls, SQL, or model-invented outcomes.
- Deployment reads currently arrive through `gitops.Module.Deployments`; this wiring does not transfer lifecycle ownership to GitOps (see known issues).

## Supporting boundaries

These are existing packages/adapters, not additional business modules. The mappings
below describe their current role and the established constraints to preserve.

| Area | Responsibilities and dependencies | Outside its responsibility / invariants |
| --- | --- | --- |
| `cmd/swarm-deploy/main.go` | Composition root: shared SQLite, Docker client/Swarm managers, module initialization, worker/server lifecycle. | Wire existing components; do not place reconciliation or domain transitions here. Module containers expose dependencies; runtime components use their injected contracts. |
| `internal/swarm` | Docker SDK adapter: service/task, image, config, secret, node, network, plugin operations and shared Docker event stream (`interfaces.go`, `swarm.go`, `events.go`). Uses shared faults/tracing. | All new Docker Engine/Swarm operations go through these managers, per `go.md`. No GitOps or deployment orchestration. SDK data types may cross the boundary; duplicate DTOs/parallel clients are not required. |
| `internal/compose` | Compose models, parsing, validation, normalization, referenced-file loading and digests; injectable file reader (`file.go`). Uses shared helpers. | No Docker operations or final application ServiceSpec conversion. Repository values stay literal; do not read controller environment variables for interpolation (`interpolation.go`, `docs/reconciler.md`). GitOps supplies revision-pinned reads and preparation policy. |
| `internal/config`, `internal/configstore` | YAML types/loading/defaults/validation and accepted stack/network snapshots; ConfigStore coordinates reload and emits `ConfigurationChanged`. Config references hosting, alert, event and notification contract types. | Configuration does not execute synchronization/deployment. Preserve `StackProvider` and snapshot/readiness semantics; this is not a module-independent leaf package. Keep config docs/examples aligned with changes. |
| `internal/storage`, `migrations/sqlite` | Shared SQLite connection, embedded Goose schema, context-aware transactions and projection helpers (`db.go`, `projection.go`). | Domain repositories own domain queries; services/handlers must not implement SQL. Reuse `storage.Database`, goqu and bound parameters. No parallel transaction infrastructure or sharing a transaction context across concurrent goroutines. Nested `WithinTransaction` calls join the active transaction without savepoints or independent rollback. |
| `internal/registry` | `AuthManager` resolves Docker registry credentials; `ImageVersionResolver` resolves image digests and uses `swarm.ImageManager` for Docker Engine distribution inspection. Used by Deployments and Assistant. | No GitOps scheduling or deployment lifecycle. Keep credentials out of specs, events and logs. |
| `internal/githosting` | `ProviderManager` resolves repository URLs to provider adapters; `Provider` currently exposes latest-release reads for Assistant. | Does not own Git checkout or reconciliation; Git repository reads/pulls belong to `gitops/git`. Git write/PR capabilities are not implemented here today; ownership for future workflows requires a separate decision. |
| `internal/security` | User context, logging and Event user enrichment; web entrypoints implement authentication/authorization. | Not deployment policy or a separate identity service. Preserve authenticated user propagation through existing request/event contracts. |
| `internal/metrics`, `internal/shared` | Prometheus groups, tracing, build info, filesystem helpers, labels, typed faults, dotenv/image utilities and shared log parsing. Metrics uses Event types; `shared/logparser.Service` uses `swarm.ServiceManager`. | Not a place to relocate business ownership. Shared packages are not all dependency-free. Preserve `faults.Coded` (`Error`, `FaultCode`, `FaultSummary`), sanitization boundaries, and centrally defined tracing keys. |
| `internal/entrypoints/webserver`, `api` | Authenticated REST/SSE/UI serving, request validation, response mapping and query aggregation over module repositories/services and Swarm managers (`handlers/handler.go`). `api/api-server.yaml` is the ogen contract. | No direct SQL/SDK calls or deployment lifecycle implementation. Current handlers also perform standalone scale, node-label and secret operations through managers; not every mutation is a Deployment. Regenerate contracts with `make gen` when editing OpenAPI, per `openapi.md`. |
| `internal/entrypoints/webhookserver`, `internal/entrypoints/healthserver`, `internal/entrypoints/sd` | Authenticate/rate-limit webhook requests and enqueue GitOps work; serve health/metrics; initialize tracing. | Webhook acceptance is queue admission, not deployment completion. Keep transport/startup concerns separate from reconciliation. |
| `cmd/sd`, `internal/entrypoints/cli` | CLI config/Compose linting through existing config/Compose loaders. | No independent deployment engine or parsing rules. |
| `ui` | Vue views/domain components, API clients, stores, routing and reusable presentation components (`ui/src/api`, `ui/src/components`, `ui/src/stores`). | Backend owns durable lifecycle; UI tolerates projection lag. Per `ui.md`, domain components own API calls/status interpretation/actions; common components accept presentation props/slots. Reuse UI Kit and existing API contracts. |
| `internal/testutil`, `e2e`, `tools/compatibility` | Shared test database setup, Swarm end-to-end tests and Docker CLI comparison harness. | Test-only infrastructure is not a runtime dependency or permission to reintroduce Docker CLI into deployment execution. |

## Known issues and maintainer clarification

These observations do not authorize refactoring. Preserve behavior within task scope;
do not expand a violation or choose a new owner without explicit approval.

1. **Existing persistence-boundary violation/coupling:** `resources/service/subscriber.go`
   constructs receipt SQL and directly reads Deployments' `desired_snapshots`, contrary
   to `go.md`'s repository rule. `notifications/alerts/store.go` reads Event's
   `outbox_events` payload/schema for routing and cleanup, and uses raw cleanup SQL.
   **Clarification:** what repository contracts should expose these cross-module checks?
   Do not treat access to the shared database as ownership of another module's tables.
2. **Ambiguous adapter/query ownership:** Resources and Recommendations import
   `gitops/deployment.WithDesired`, although it only hydrates events from Deployments.
   Web/assistant deployment queries are wired through `gitops.Module.Deployments`.
   **Clarification:** are these intentional integration points or residual coupling?
   Until decided, they describe current wiring, not permission for further GitOps dependencies.
3. **Resolved: Event/Notifications assembly boundary.** See [Event](#event) and
   [Notifications](#notifications) for the supported ownership boundary.
4. **Residual Deployments wiring coupling:** `deployments/module.go` reads
   `cfg.Spec.GitOps.Secrets.Age` and `cfg.Spec.GitOps.Sync.Policy`.
   **Clarification:** is this configuration shape intentional or compatibility residue?
   It does not permit a GitOps module dependency or direct SDK operations.
5. **Resolved: transaction documentation mismatch.** Nested `WithinTransaction` calls
   join the active transaction without savepoints or independent rollback.
