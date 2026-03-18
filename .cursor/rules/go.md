---
name: go
description: Rules for writing code in Go
globs: ["**/*.go"]
apply: by file patterns
---

# Go Logging Rules

- **Primary Library**: Always use the `log/slog` standard library for all logging needs.
- **Format**: Ensure all logs are structured (JSON format where possible) using key-value pairs.
- **Error Handling**: When logging an error, use `slog.Error` and include the stack trace details.
- **Avoid**: Never log raw error strings without context or PII.

# Environment
- Environment variables are described in the `Config` structure
- Config parse with library `https://github.com/caarlos0/env`
- To configure the gRPC, an additional GrpcConfig structure with the `PORT` and `USE_REFLECTION` variables is required
- Database config actually have `DSN` variable which secret and loaded from mounted file. Library automatically loaded file and set value into `Config` structure
- Secret values like API key or DSN must be loaded from mounted file.

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
