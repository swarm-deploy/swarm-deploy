## Project Context
- This project implements Continuous Deployment for Docker Swarm

## Projects Rules
- Before starting any coding task, load and follow all files in `.cursor/rules/*.md`
- Treat frontmatter as policy:
- `apply: always` means rule is always active.
- `apply: by file patterns` + `globs` means apply only to matching files.
- `alwaysApply: true` means apply regardless of globs.
- In the first progress update, briefly state which `.cursor` rules were loaded.

## Project structure
- `./ui` - Frontend
- `./internal` - Backend on Golang
- - `./internal/entrypoints/health` - Entrypoint for metrics and healthchecks
- - `./internal/entrypoints/webserver` - Entrypoint for UI and API Server
- - `./internal/entrypoints/webhookserver` - Entrypoint for Webhook Server, receive webhooks from another systems, like GitHub, GitLab, etc.
- `./api/api-server.yaml` - OpenAPI contracts for API Server of `webserver`
