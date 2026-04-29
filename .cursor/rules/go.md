---
name: go
description: Rules for writing code in Go
globs: ["**/*.go"]
apply: by file patterns
---

# Comments
- Each interface method must have a comment.
- Each structure exported field must have a comment.

# Go Logging Rules

- **Primary Library**: Always use the `log/slog` standard library for all logging needs.
- **Format**: Ensure all logs are structured (JSON format where possible) using key-value pairs.
- **Error Handling**: When logging an error, use `slog.ErrorContext`.
- **Avoid**: Never log raw error strings without context or PII.
- Always log with Go context. Use `slog.InfoContext` instead of `slog.Info`

# Go Testing
- For asserts use library `github.com/stretchr/testify/assert`. Example: `assert.Equal(t, 123, 123, "they should be equal")`
- For stoppable asserts use library `github.com/stretchr/testify/require`. Example: `require.Equal(t, 123, 123, "they should be equal")`

# Configuration / Environment
- Environment variables are described in the `Config` structure
- To configure the gRPC, an additional GrpcConfig structure with the `PORT` and `USE_REFLECTION` variables is required
- Database config actually have `DSN` variable which secret and loaded from mounted file. Library automatically loaded file and set value into `Config` structure
- Secret values like API key or DSN must be loaded from mounted file.
- Every time you change configs update the documentation in the `example/**` folder
- When working with secrets (API token, password), always use files. Don't manually write file reads in your code; use the `specw.File` from the library https://github.com/ArtARTs36/specw

Example of a valid configuration file:

```go
type Config struct {
	DB         DBConfig         `envPrefix:"DB_"`

	Log        struct {
		Level slog.Level `env:"LEVEL"`
	} `envPrefix:"LOG_"`

	GRPC GRPCConfig `envPrefix:"GRPC_"`
}

type DBConfig struct {
	DSN string `env:"DSN,file,unset,notEmpty,required"`
}

type GRPCConfig struct {
	Port          int  `env:"PORT,required"`
	UseReflection bool `env:"USE_REFLECTION"`
}
```

# Event Dispatcher
- Project use Event Dispatching for notifications and save events to history
- Events declared in ./internal/event/events
- Notification subscribers config located in ./internal/config NotificationSpec
- Each event is described in docs/event-history.md

## Structure initialization
- For DTO use default initialization.
- For service components use constructor functions like New{StructName} to initialize structs. In structure methods not repeat dependency validation.
- Do not use `nil-guard`
