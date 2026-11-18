# Swarm Deploy test environment

The test environment builds swarm-deploy from the current checkout, deploys two
test stacks through GitOps, and exports traces to Jaeger. Use a dedicated local
Swarm manager because the controller has access to the Docker socket.

## Start

1. Install Docker Engine with Compose v2, Git, and Make. On a dedicated local Docker
   Engine, initialize Swarm with `docker swarm init` if needed. The Makefile uses
   `/var/run/docker.sock`.
2. Save an OpenAI API key in `testenv/.openai-api-token`. For another OpenAI-compatible
   provider, set the API URL, chat model, and embedding model in
   [swarm-deploy.yaml](swarm-deploy.yaml).
3. Run `make -C testenv up` from the repository root. Run `make -C testenv build`
   to build the local images separately.

| Endpoint | Address |
| --- | --- |
| Swarm Deploy UI and assistant | [http://127.0.0.1:8080](http://127.0.0.1:8080) |
| Health check | [http://127.0.0.1:8082/healthz](http://127.0.0.1:8082/healthz) |
| Jaeger UI | [http://127.0.0.1:16686](http://127.0.0.1:16686) |
| Jaeger MCP (Streamable HTTP) | `http://127.0.0.1:16686/api/ai/mcp/` |

Wait for `testenv-whoami` and `testenv-valkey` to appear in the Stacks page.
Ask the built-in assistant which services are running in `testenv-whoami`.
To use Jaeger from a local Codex agent, add this to its MCP configuration:

```toml
[mcp_servers.jaeger]
url = "http://127.0.0.1:16686/api/ai/mcp/"
```

Search for the `swarm-deploy` service in Jaeger after a sync or a UI request.
Jaeger retains up to 10,000 traces in memory until the container restarts.

## Change the test stacks

Edit the manifests under [gitops](gitops), then run `make -C testenv seed-git`.
This commits the changes to the generated local Git repository that swarm-deploy
polls every five seconds. Running `up` also seeds the repository.

## Stop

Run `make -C testenv down` to stop the Compose services. Then run
`docker --host unix:///var/run/docker.sock stack rm testenv-whoami testenv-valkey`
to remove the test stacks. The Compose volume keeps swarm-deploy's SQLite state
and Git checkout across restarts.
