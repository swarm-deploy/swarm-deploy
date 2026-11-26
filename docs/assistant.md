# AI Assistant

The assistant helps with environment debugging using:

- service metadata from the SQLite-backed service catalog (`service.store`) as RAG context
- local tools for runtime actions and history inspection

Assistant is available only when `assistant.enabled: true`.

## Language

`system.language` selects English (`en`, the default) or Russian (`ru`) for the assistant UI, new replies, and execution progress. The assistant uses the installation language even when the question or earlier messages are in another language. Changing the installation language applies to the next reply in an existing chat. Saved messages and their activity remain in their original form. The installation language takes precedence over language instructions in `assistant.systemPrompt`.

Both `yes` / `no` and `да` / `нет` work for operation confirmations. Resource names, identifiers, commands, and quoted tool output keep their original spelling.

## Request routing

For a regular chat turn the configured model classifies the user's goal using the current message and up to six recent messages without calling tools. The assistant handles swarm-deploy usage and practical infrastructure tasks, including deployment, configuration, observability, operations, and troubleshooting. Supported tasks use the `platform`, `services`, `cluster`, `deployments`, `diagnostics`, or `lookups` profile. Exact greetings and acknowledgements use a local fast path. An invalid router response fails the request.

The `general` and `out_of_scope` routes return a brief description of the assistant's specialization. The `needs_clarification` route asks which infrastructure task the user wants to solve. These turns end after routing, before retrieval, answer generation, or tool execution. For mixed requests, the router selects capabilities and passes the supported part to service retrieval and the response model. The original request text is stored in the conversation.

`platform` covers stable infrastructure guidance as well as swarm-deploy itself. Questions about the actual installation can request the configuration capability. Capabilities select operational tools independently of the response profile. Only `service_context` requests service catalog retrieval. Pure platform guidance reaches the response model with conversation history and the security-report tool.

## Terminal service actions

Successful single calls to `service_restart_trigger` and `service_replicas_set` are terminal assistant actions. After the existing tool-result injection guard passes, the assistant formats their typed result directly instead of issuing a second model completion. Tool errors, malformed results, non-terminal tools, and iterations containing multiple calls continue through the regular model tool loop.

The assistant chat span records request-size diagnostics without recording prompt or user content: `swarm-deploy.assistant.request.system_prompt_chars`, `swarm-deploy.assistant.request.history_chars`, `swarm-deploy.assistant.request.context_chars`, `swarm-deploy.assistant.request.user_message_chars`, `swarm-deploy.assistant.request.tools_chars`, `swarm-deploy.assistant.request.message_count`, and `swarm-deploy.assistant.request.tool_count`. It also records `swarm-deploy.assistant.route` and, for a completed terminal action, `swarm-deploy.assistant.terminal_tool`. MCP tool execution spans use the OpenTelemetry GenAI attribute `gen_ai.operation.name=execute_tool`. Log tool results are excluded from span attributes; only their encoded character count is recorded.

## API

- `POST /api/v1/assistant/chat`
- `GET /api/assistant/suggestions`
- `GET /api/v1/assistant/chats`
- `GET /api/v1/assistant/chats/{conversation_id}`
- `GET /api/v1/assistant/usage?from=2026-11-01&to=2026-12-01&interval=weekly`

The chat endpoint supports start and poll with the same route. Chat history endpoints return persisted conversations ordered by latest activity and the full message history for a selected chat.

While a run is in progress, responses may include an `activity` array with safe execution progress such as request routing, context lookup, response generation, and MCP tool execution. The UI uses short long-poll requests while a run is active so these updates appear promptly. Activity intentionally excludes model chain-of-thought, tool arguments, and tool results. Completed assistant turns persist this activity metadata alongside the message, so reopening a chat restores the execution trace. Activity is not included in subsequent model context.

### Empty-chat suggestions

The suggestions endpoint returns a `suggestions` array with `id`, `title`, and `prompt` for each item. The drawer displays the localized title and sends the full prompt. The endpoint lists the most recently changed deployed service and the newest alert when available, followed by cluster state and today's deployments. If either dynamic source fails, the other suggestions are still returned with HTTP 200.

Alert suggestions use the alert kind and selected metadata, including the alert ID, status, and creation time in UTC. They omit the original alert title and message and ask the assistant to verify the cause against deployment and cluster data.

### Start request

```json
{
  "conversation_id": "optional",
  "message": "Why deploy failed for api stack?",
  "wait_timeout_ms": 12000
}
```

### Poll request

```json
{
  "conversation_id": "conversation-id-from-start",
  "request_id": "request-id-from-start",
  "wait_timeout_ms": 12000
}
```

### Response

```json
{
  "status": "in_progress|completed|failed|rejected|disabled",
  "request_id": "request-id",
  "conversation_id": "conversation-id",
  "answer": "optional",
  "tool_calls": [],
  "activity": ["Routing request", "Generating response"],
  "error_message": "optional",
  "poll_after_ms": 1000
}
```

## Chat history

User/assistant turns are persisted in the `assistant_chats` and `assistant_turns` tables of `<dataDir>/swarm-deploy.sqlite`. A user question is saved before the model is called, and the reply is appended when it is ready; a chat therefore appears in history as soon as its first question is accepted. If the question cannot be saved, the request fails without calling the model. If the reply cannot be saved, the request fails with a save error and the reply is not added to model context. An operation becomes eligible for confirmation once its confirmation prompt is saved. Questions rejected by prompt injection protection before generation are not saved. A question whose reply failed stays in the chat and in subsequent model context, and the UI marks it as having no reply. Assistant turns may also contain an optional `activity` array with the safe execution trace shown by the UI. The chat list reads metadata from SQLite without loading full message histories. The UI derives the title from the first user message and can reopen older chats.

A chat accepts one request at a time: a new message while a reply is being generated fails with an error. While a reply is running, the chat history endpoint returns `active_request` with its `request_id` and activity, so the UI continues polling after a page reload. Reopening a chat after a connection failure reloads its history and resumes any running reply. Active requests are kept in process memory, so a reply that was running during a restart is lost and its question stays unanswered. When an older chat is continued after the in-memory context cache expires or the process restarts, recent turns are restored from the persisted SQLite history.

## Token usage

The Usage screen groups each chat's accumulated token totals by its UTC creation date. It includes chats created before the Usage feature was introduced. Further activity in an existing chat increases the totals in its original bucket.

The usage endpoint takes required UTC dates in `YYYY-MM-DD` format and an `interval` of `daily`, `weekly`, or `monthly`. The range includes `from` and excludes `to`. Weeks start on Monday and months on the first. Each bucket counts chats created within the requested range.

The response contains ascending `buckets` with `start`, `input_tokens`, `output_tokens`, and provider-reported `total_tokens`. Empty intervals between populated buckets are returned as zeros. A range without chats with token totals returns an empty array.

## Built-in tools

- `swarm_deploy_config_get` - reads the running global configuration. Omit `section` for an overview.
- `history_event_list` - returns recent events from event history
- `alert_list` - returns Alert Management incidents ordered by latest update
  - `status`: `open` (default), `resolved`, or `all`
  - optional filters: `kind`, `resource_type`, and `resource_id`
  - `limit`: default `20`, maximum `100`
  - each compact summary contains only selection/correlation fields: `id`, `status`, `kind`, `resource_type`, `resource_id`, `title`, `updated_at`, and optional `deployment_id`/`error_code`
- `alert_get` - returns one complete safe alert representation by required `id`, including message, failure metadata, deployment correlation, occurrences, lifecycle timestamps, and resolution details
- `deployment_list` - returns lightweight desired-state deployments in newest-first order without Compose snapshots or semantic change arrays
  - optional `stack` and `status` (`running`, `succeeded`, `failed`, `interrupted`, or `superseded`) filters
  - `limit`: default `20`, maximum `100`
  - optional `cursor`; the response contains `next_cursor` when another filtered page exists
- `deployment_get` - returns one desired-state deployment by required `id`, including immutable redacted changes and nested execution Attempts with safe diagnostics and lifecycle stages
- `deploy_sync_trigger` - triggers manual sync (same as `POST /api/v1/sync`)
- `swarm_node_list` - returns current Docker Swarm nodes snapshot
- `docker_network_list` - returns current Docker networks snapshot (`name`, `scope`, `driver`, `internal`, `attachable`, `ingress`, `labels`)
- `docker_plugin_list` - returns current Docker plugins snapshot (`id`, `name`, `description`, `enabled`, `plugin_reference`, `capabilities`)
- `docker_secret_list` - returns current Docker secrets snapshot (`id`, `name`, `created_at`, `updated_at`, `driver`, `labels`)
- `service_logs_get` - returns bounded recent logs for a stack service
- `service_task_list` - returns all Docker Swarm task states for a stack service, newest first
  - requires `stack_name` and `service_name`; `limit` defaults to `20` and is capped at `100`; use `next_offset` as `offset` to access older tasks
  - each task contains `id`, `node`, `state`, `error`, `created_at`, and `updated_at`; terminal states such as `failed`, `rejected`, and `shutdown` are retained
  - use the returned task `id` with `service_task_logs_get`
- `service_health_get` - returns a deterministic health snapshot of one stack service and is the entry point for service diagnostics
  - requires `stack_name` and `service_name`
  - `status` is `healthy`, `degraded`, `failing`, or `unknown`; it is derived from the most severe flag and is `unknown` whenever live Swarm state could not be read
  - `flags` contain `code`, `severity` (`critical`, `warning`, `info`), `phase` (`transient` or `active`), optional `reason`, `since`, tool-computed `since_ago`, `expected_ready_by`, `evidence`, and `refs` (`task_id`, `deployment_id`, `alert_id`) for follow-up tools
  - flag codes: `no_running_tasks`, `replicas_below_desired`, `no_eligible_nodes`, `scaled_to_zero`, `restart_loop`, `update_rollback_completed`, `update_paused`, `update_in_progress`, `last_deploy_failed`, `deploy_in_progress`, `convergence_unverified`, `drift_detected`, `open_alert`, and `service_not_found`
  - task deficit reasons reuse `faults` codes when Docker reported a typed cause (`no_suitable_node`, `image_not_found`, `image_pull_denied`, `mount_failed`, `image_extraction_failed`) and otherwise use `node_unavailable`, `healthcheck_failing`, `crash_loop`, `task_failed`, `starting`, `no_tasks`, or `unknown`
  - relevance follows the current service revision instead of a time window: only open alerts, only the latest deployment containing the service (plus the last succeeded one when the latest failed or was interrupted), and only failures of tasks created from the current task template; the one recency check is `restart_loop`, which requires a failure within the last 10 minutes
  - stack-level alerts are included when their `deployment_id` belongs to a deployment containing the service (`linked: true`), or when they have no deployment, such as preparation failures (`linked: false`)
  - a startup grace period of `max(UpdateConfig.Monitor, Healthcheck.StartPeriod, 30s)` starts at the latest service update; within it, flags without hard failure evidence have `phase: transient` and at most `warning` severity
  - running tasks on nodes that are not `ready` are not counted as running; `sources` reports `ok`, `unavailable`, or `skipped` for `swarm`, `nodes`, `alerts`, and `deployments`, and an unavailable optional source does not fail the call
  - text fields are bounded and remain untrusted data
- `service_task_logs_get` - returns bounded logs for a Docker task, with optional shared JSON/logfmt parsing, normalized level filters, and time bounds
- `service_replicas_set` - updates desired replicas count for a specific stack service
- `service_restart_trigger` - restarts service by scaling replicas to `0` and restoring previous count
- `service_self_heal_enable` - proposes enabling automatic drift repair for one GitOps service after user confirmation
- `service_self_heal_disable` - proposes disabling automatic drift repair for one GitOps service after user confirmation
- `service_image_pin` - proposes pinning a GitOps service image to the digest used by all running Swarm tasks after user confirmation
- `service_webroute_ping` - checks web routes for a specific service from `service.store` and returns HTTP results for each route
- `tls_certificate_check` - checks a live TLS endpoint and validates its server certificate against the system trust store
- `dependency_graph_get` - returns service dependency graph with nodes, endpoints, and direct dependencies inferred from service environment variables
- `registry_image_version_get` - resolves актуальный тег и digest Docker-образа в registry (Docker Hub и совместимые)
  - registry is selected automatically by tool logic
- `git_repository_diagnostics` - reads local GitOps repository metrics, last confirmed remote availability, and the active repository alert; see the contract below
- `git_commit_list` - returns latest git commits from repository history (`limit` optional, default 10)
- `self_metrics_list` - returns structured snapshot of internal Prometheus metrics (`swarm_deploy_*`)

`history_event_list` is the user-facing event timeline and is not the source of truth for every deployment failure. `alert_list`/`alert_get` expose operational incident lifecycle, while `deployment_list`/`deployment_get` expose desired states and their persisted execution Attempts. A `succeeded` deployment means its latest Attempt completed its applicable pipeline, including convergence verification for services that require it. It is not a continuous health guarantee: an unchanged no-op does not recheck task health, externally managed replicated services can use specification-only verification, and services may fail after deployment completion. See [native deployment verification](native-deployment-compatibility.md#convergence-failures-and-retries) and [external replica ownership](drift.md#replica-ownership-during-deployments).

Alerts correlate to the desired state through `deployment_id`; lifecycle events additionally carry `attempt_id`. The preferred minimal lookup is `alert_list` -> `deployment_get` -> final answer because the compact list result already includes `deployment_id`. Use `alert_get` only when its additional incident details are needed. Follow the correlation only when `deployment_id` is non-empty. A preparation failure can create an alert without creating a Deployment or Attempt; the assistant reports that condition instead of inventing one.

The default route capabilities expose deployments and alerts to `deployments` and `diagnostics`; `cluster` receives Alert Management plus cluster inspection. Service-specific deployment-failure questions are routed with service context/runtime plus deployment and alert capabilities.

All four tools are read-only and query the existing repositories directly. They do not expose Outbox payloads, desired Compose YAML, fingerprint maps, or raw database payloads. Deployment details reuse the persisted bounded, masked diagnostic and redacted semantic changes. Alert messages, diagnostics, commit metadata, and diff text are treated as untrusted data, not assistant instructions.

Example use-case:
- Question: `Я использую актуальную версию этого сервиса?`
- Expected assistant flow: detect current service image from `service.store` -> call `registry_image_version_get` for current image and for upstream latest image -> compare and answer with concrete tag/digest difference.

Tool access is controlled by `assistant.tools`:

- empty list (`[]`) permits all built-in tools, subject to the selected route
- a non-empty list sets the global allow-list. Selected capabilities determine which of those operational tools are available for the request
- the built-in prompt-injection report tool is available regardless of `assistant.tools`
- the effective allow-list is checked both when tools are sent to the model and immediately before execution

Each assistant run permits at most three tool execution rounds. After the third round, the model receives one tool-free instruction to produce its final answer from the evidence already collected. Any further tool request is not executed, and the run terminates gracefully instead of restarting the tool loop.

## TLS certificate checks

Ask “When does the TLS certificate for registry.example.com expire?” or “Проверь TLS на 10.0.0.15:8443 с именем api.example.com”. The `tls` capability is available for focused lookups and diagnostics. General questions about TLS can be answered without a live check. Add `tls_certificate_check` to `assistant.tools` if you use an explicit tool allowlist.

| Argument | Meaning |
| --- | --- |
| `host` | Required DNS hostname or IPv4/IPv6 address without a scheme, path, or port. Use an ASCII DNS name for internationalized domains. |
| `port` | Optional TCP port from 1 to 65535. Defaults to 443. |
| `server_name` | Optional certificate identity. A DNS name is also sent as SNI. Defaults to `host`. With an IP identity the check uses the IP SAN and sends no SNI. |

```json
{"host":"registry.example.com"}
```

```json
{"host":"10.0.0.15","port":8443,"server_name":"api.example.com"}
```

The check runs from the swarm-deploy process and accepts any reachable public or internal endpoint, including loopback. It attempts one direct TLS handshake without sending HTTP or other application data. TCP connection and handshake each have a five-second timeout and honor request cancellation. The client offers TLS 1.2 or later using Go's supported suites. The result reports the negotiated version and cipher suite for that handshake.

| Result | Meaning |
| --- | --- |
| `connection` | Effective target, TCP connection status, handshake status if attempted, and the version and cipher suite on success. |
| `certificate` | Leaf subject, issuer, DNS and IP SANs, UTC validity dates, signed remaining seconds, self-signature status, and hex SHA-256 fingerprint. Available certificate details survive handshake or validation failures. |
| `chain` | Certificates presented by the peer and the result of path and signature verification against the process's system trust store at the time of the check. Path verification is independent of the requested hostname and TLS Server Authentication usage. |
| `validation` | Leaf validity period, hostname or IP SAN match, TLS Server Authentication suitability when known, and overall certificate status of `valid`, `invalid`, or `not_checked`. Unavailable individual results are omitted. Certificate status describes X.509 validation. Check the connection section separately for handshake success. |
| `warnings` / `errors` | Diagnostic codes with a stage and technical detail. Network failures may also include a reason of `timeout` or `canceled`. |

`connection_failed` and `tls_handshake_failed` identify different network stages. Certificate findings include `certificate_expired`, `certificate_not_yet_valid`, `hostname_mismatch`, `unknown_authority`, and `server_auth_incompatible`. The tool reports `certificate_expiring_soon` for a currently valid leaf expiring within 30 days. It reports `incomplete_chain` when verified paths prove that the peer omitted an intermediate. If the available certificates cannot establish why trust verification failed, the result retains `unknown_authority` and the verifier's detail. A root trust anchor may be omitted from the peer's certificate list.

The check covers one endpoint per call. It does not perform OCSP/CRL checks, load PEM files, fetch issuer certificates, scan networks, schedule monitoring, renew certificates, or create Alerts or Recommendations.

## Questions about running configuration

Ask “What is enabled in my swarm-deploy?”, “How are notifications configured?” or “How much time does an init job have?”. The assistant uses the `configuration` capability and calls `swarm_deploy_config_get` with `{}` for the overview or with a single `section`.

The supported sections are `gitops`, `deployments`, `event`, `system`, `notifications`, `web`, `healthServer`, `assistant`, and `hostings`. For example, `{"section":"deployments"}` gives the poll interval and default timeout per init job. A job can define its own timeout in Compose. Other arguments and unsupported section names are rejected. If you use an explicit `assistant.tools` allowlist, add `swarm_deploy_config_get` to enable the tool.

The `system` section reports the effective `language`. In the `assistant` section, `toolAccess.allowedTools` lists recognized entries from the configured global allowlist. The actual tools available for a request follow the capability and security rules above.

GitOps settings describe the global policy. In webhook mode, local reconciliation still runs on a timer and periodic Git polling is disabled. For a question about self-heal on a particular stack, the assistant can check the global policy and available runtime evidence. The stack or service override requires separate evidence.

Notification settings describe configured event channels and the route selected for each alert kind, including the fallback and the action on resolution. Telegram attempts include the initial attempt for each delivery action. Delivery success is a separate runtime fact. The assistant's in-memory context TTL only governs the cache. SQLite chat history is retained independently.

Only explicitly selected fields from startup configuration are sent to the model. Tool logs and spans contain execution metadata, and the OpenAI completion trace omits the raw arguments and structured result of this tool. The assistant's answer can include the selected settings and is saved as normal chat history.

## Configuration

```yaml
system:
  language: en
assistant:
  enabled: true
  tools: []
  systemPrompt: ""
  model:
    name: gpt-4o-mini
    openai:
      baseUrl: https://api.openai.com/v1
      apiTokenPath: /run/secrets/openai_api_token
      temperature: "0.2"
      maxTokens: "800"
```

Notes:

- `assistant.model.openai.apiTokenPath` must point to a non-empty token file when assistant is enabled
- relative `apiTokenPath` is resolved relative to config file directory
- `temperature` must be in range `[0,2]`
- `maxTokens` must be greater than `0`

## Prompt Injection Protection

Assistant requests are protected by:

- regex deny-list checks on user input before model execution
- immutable safety instructions inside system prompt

When protection is triggered, response status is `rejected`.

### Change self-heal for a service

Ask the assistant to enable or disable self-heal for a managed service. It checks the Compose service label
in the write base, then the stack policy, then the global policy. If the effective policy already matches the
request, the assistant reports its source and ends the operation. Otherwise it asks for confirmation to set
`org.swarm-deploy.service.sync.policy.selfHeal` under the service's Compose `deploy.labels` to the string
`"true"` or `"false"`. For a disable request, if self-heal is still enabled when the assistant rechecks the
write base but its source has changed, the assistant asks for confirmation again with the updated source.

A confirmed change is published on a proposal branch. The policy takes effect when the change reaches the
tracked branch and GitOps syncs it. Disabling self-heal affects automatic drift repair for the selected
service. Drift detection, normal deployments, Pause Sync, and replica management continue as configured.
If `assistant.tools` is set, allow each action by its own tool name.

The writer uses `gitops.git.repository` and `gitops.git.branch` by default. Set `gitops.git.push.repository`,
`gitops.git.push.branch`, or `gitops.git.push.auth` to override those settings. Write auth inherits sync
credentials only when using the sync repository. For GitHub, set `gitops.git.push.apiTokenPath` to a mounted
token file with permission to open pull requests in the write repository. The assistant returns the branch
and, when available, the pull request link.

### Pin the running service image in Git

Ask the assistant to pin the image currently running for a managed service. It reads
`services.<service>.image` from the write-base Compose file and calls `ServiceManager.ListTasks` for the exact
stack/service selected through the normal service resolver. Only tasks whose current state is `running` are
evidence; completed historical tasks are ignored, while both old and new running tasks during a rollout are
included. The operation never resolves a tag through a registry.

Every running task must expose a valid `sha256` digest in its task image reference. All running digests must be
equal, and the normalized repository and tag must match the Compose value. The confirmation shows the exact
service, running-task count, current Git image, and proposed image. The edit preserves the Compose image spelling
and appends `@sha256:...` through `shared/yamledit`; no other Compose field is changed.

The assistant does not ask for confirmation when Git already contains the same pin, running tasks are absent,
a digest is missing, a rollout has multiple running digests, or Git contains another repository, tag, or digest.
Before publishing it rereads both Compose and running tasks for the previously selected service. Changed evidence
invalidates the confirmation and is evaluated again. A confirmed change uses the same proposal branch, push, and
GitHub pull-request integration as self-heal proposals. Add `service_image_pin` to `assistant.tools` when an
explicit allowlist is configured. Docker access in this workflow is read-only.

### Git repository diagnostics

`git_repository_diagnostics` is a read-only starting point for repository health and GitOps access investigations.
It accepts no arguments and reads the configured, already-cloned GitOps checkout. It does not fetch, pull,
create another checkout, or return file contents or Git credentials.

The response contains `head`, nullable `branch` (detached HEAD), `shallow`, `commits`, `gitSizeBytes`,
`workingTreeSizeBytes`, `trackedFiles`, `workingTreeDirty`, `untrackedFiles`, `untrackedFilesTruncated`,
`availability`, `alertId`, and `errors`.

- `commits` counts unique reachable commits at the returned `head`, stopping at shallow boundaries.
- Sizes sum file lengths on disk, including packfiles for Git storage and ignored/untracked files for the working
  tree. The root `.git` entry is excluded from the working tree. Symlinks are counted without following targets;
  filesystem allocation blocks and hard-link deduplication are not measured.
- `trackedFiles` counts distinct index paths, including staged additions. Status uses go-git's staged/unstaged
  comparison and ignore rules. Untracked files alone never set `workingTreeDirty`.
- `untrackedFiles` returns up to 100 sorted, repository-relative paths. `untrackedFilesTruncated=true` means
  additional paths exist; an empty list means none were found.
- `availability` reads the last confirmed remote observation persisted by `gitpull.Monitor` in the existing
  GitOps runtime JSON. Successful pulls (including no-change results) establish `true`; classified remote access
  failures establish `false`. Local/ambiguous failures, invalid local auth configuration and cancellation do not
  overwrite the last confirmed observation. Missing state or a different repository identity yields `null`.
  Historical `GitPullFailure` and poll/sync summaries are not treated as proof of remote access.
- `alertId` reads the active `git_repository_unavailable` incident for the same credential-free repository identity
  through the existing Alert Management store. Follow it with `alert_get` when availability is false. A false
  availability without an alert still indicates a problem: network alerts require three consecutive failures,
  and the asynchronous alert projection can lag. Availability and alert reads are not an atomic snapshot.
- Failed metrics return `null` with a corresponding entry in `errors`: `read_failed`, `deadline_exceeded`,
  `canceled`, `scan_limit_exceeded`, or `head_unavailable` for a commit count without HEAD. A null value without
  an error means expected absence/unknown state. The atomic status operation supplies dirty/untracked/truncation
  together; if it fails, all three fields are null with error entries. Other metrics remain available.

Each operation has a two-second context deadline. Traversals have a 100,000-entry/commit budget; status also
bounds filesystem operations and content reads (64 MiB). These are cooperative limits between library/filesystem
operations, not hard preemption of a single filesystem call or go-git object/index decoding. Budget failures
never return a misleading exact count. The tool introduces no diagnostic cache.

Long scans use independent go-git handles over the same checkout, without holding the reconciler's repository
lock. Commit counting is pinned to the captured HEAD; branch, index, status and file sizes are best-effort samples
and may reflect different moments during a concurrent pull. The tool never writes checkout state. Existing HEAD
resolution assumes the GitOps checkout has already been cloned.
