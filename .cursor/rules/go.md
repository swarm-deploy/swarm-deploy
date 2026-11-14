---
name: go
description: Rules for writing code in Go
globs: ["**/*.go"]
apply: by file patterns
---

## Docker API Boundary

- **All new Docker Engine / Docker Swarm API operations MUST go through `internal/swarm`.** Other packages must use the managers exposed by `internal/swarm`; they must not call Docker SDK client methods directly or introduce parallel Docker API clients/adapters.
- If an operation is missing, extend the appropriate `internal/swarm` manager interface and implementation, including mocks and tests where relevant.
- Docker SDK **data types** (for example, `dockerswarm.ServiceSpec`, `mount.Mount`, and Docker network options) may be used directly by other packages. Do not introduce wrapper DTOs that merely duplicate these complex SDK types.
- Existing direct Docker API calls outside `internal/swarm` do not require unrelated refactoring in every task, but must not be copied or expanded. Migrate them when working on the corresponding integration or in an explicitly scoped refactor.

# Comments
- Each interface method must have a comment.
- Each structure exported field must have a comment.

# Database and SQL
- Build SQL queries in Go using `github.com/doug-martin/goqu/v9` (goqu). Use the appropriate dialect, `Prepared(true)`, and bound arguments for dynamic values. Do not build queries by concatenating or formatting SQL strings.
- Follow the **Model Repository** pattern: keep domain models separate from persistence, and encapsulate database reads and writes in repositories. Follow existing module conventions (`model` for models, `modelstore` for repositories where applicable).
- Application services, controllers, and HTTP handlers must access persistence through repository methods rather than constructing or executing SQL directly.
- Reuse existing repository contracts and `internal/storage` transaction abstractions. Use `goqu.L` only for SQL expressions the builder cannot express, without interpolating dynamic values. Raw SQL migration files are exempt.

# Go Logging Rules

- **Primary Library**: Always use the `log/slog` standard library for all logging needs.
- **Format**: Ensure all logs are structured (JSON format where possible) using key-value pairs.
- **Error Handling**: When logging an error, use `slog.ErrorContext`.
- **Avoid**: Never log secrets, credentials, or personally identifiable information (PII).
- Always log with Go context. Use `slog.InfoContext` instead of `slog.Info`

# Go Testing
- For asserts use library `github.com/stretchr/testify/assert`. Example: `assert.Equal(t, 123, 123, "they should be equal")`
- For stoppable asserts use library `github.com/stretchr/testify/require`. Example: `require.Equal(t, 123, 123, "they should be equal")`
- Use table-driven pattern

## Mocking
- Do not write handwritten fakes, stubs, or mocks when the task requires mocking dependencies.
- For mocks always use `go.uber.org/mock/gomock` and generated mocks.
- Place generated mocks next to the package that owns the mocked interface.
- Do not introduce ad-hoc test helper types that imitate production interfaces unless explicitly requested.

# Configuration
- Runtime configuration is loaded from YAML; its types and validation live in `internal/config/` (see `Config.Spec` in `internal/config/config.go`).
- When changing configuration, update the relevant documentation and examples under `example/`.
- Load supported sensitive configuration values from mounted files (for example, `specw.File` for the age identity file); never log secret values.

# Events and Notifications
- Event types and payloads are defined in `internal/modules/event/events/`.
- Durable event delivery is implemented in `internal/modules/event/outbox/`.
- Notification configuration types live in `internal/config/`.
- Keep `docs/event-history.md` and `docs/notifications.md` consistent with relevant public contract changes.

## Structure initialization
- For DTO use default initialization.
- For service components use constructor functions like New{StructName} to initialize structs. In structure methods not repeat dependency validation.
- Do not use nil-guard for injected dependencies.
- All injected dependencies are mandatory unless explicitly marked optional.
- Never mask wiring errors with fallback behavior.

## Tracing

- To record an error in a span, use `tracing.FailSpan`

- Define application-owned OpenTelemetry span and span-event attribute keys centrally in `internal/shared/tracing/keys.go` using `attribute.Key`.
- Use the shared `tracing` keys when recording attributes; do not introduce inline string literals for application-owned attribute keys or duplicate existing keys locally.
- Preserve semantic value types (for example, record counts and durations as numeric attributes instead of strings).
