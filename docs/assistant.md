# AI Assistant

The assistant helps with environment debugging using:

- service metadata from `.swarm-deploy/services.json` (`service.store`) as RAG context
- local tools for runtime actions and history inspection

Assistant is available only when `assistant.enabled: true`.

## API

- `POST /api/v1/assistant/chat`

The endpoint supports start and poll with the same route.

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
  "error_message": "optional",
  "poll_after_ms": 1000
}
```

## Built-in tools

- `history_event_list` - returns recent events from event history
- `deploy_sync_trigger` - triggers manual sync (same as `POST /api/v1/sync`)
- `swarm_node_list` - returns current Docker Swarm nodes snapshot
- `docker_network_list` - returns current Docker networks snapshot (`name`, `scope`, `driver`, `internal`, `attachable`, `ingress`, `labels`)
- `service_webroute_ping` - checks web routes for a specific service from `service.store` and returns HTTP results for each route
- `registry_image_version_get` - resolves актуальный тег и digest Docker-образа в registry (Docker Hub и совместимые)
  - registry is selected automatically by tool logic
- `git_commit_list` - returns latest git commits from repository history (`limit` optional, default 10)

Example use-case:
- Question: `Я использую актуальную версию этого сервиса?`
- Expected assistant flow: detect current service image from `service.store` -> call `registry_image_version_get` for current image and for upstream latest image -> compare and answer with concrete tag/digest difference.

Tool access is controlled by `assistant.tools`:

- empty list (`[]`) means all built-in tools are available
- non-empty list works as an allow-list

## Configuration

```yaml
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
