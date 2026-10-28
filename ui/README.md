# Swarm Deploy UI

## Development with the mock API

Install the UI dependencies and start Vite together with the OpenAPI mock server:

```shell
npm install
npm run dev:mock
```

Run these commands from `ui/`. Alternatively, run `make ui-mock` from the repository root.
Open the Vite URL printed in the terminal (normally <http://localhost:5173>).

The mock server reads [`api/api-server.yaml`](../api/api-server.yaml), resolves its referenced
specifications, validates requests, and creates responses from the response schemas. In mock mode
Vite proxies all `/api` requests to Scalar Mock Server at `http://127.0.0.1:4010`, so the UI code
uses the same relative URLs as it does with the real backend.

The default overview dataset contains 10 stacks and 16 related services. It includes healthy and
out-of-sync workloads, deployment metadata, Git revisions, routes, images, and synchronization
errors so the main overview states can be tested without editing the mock first.

To run only the mock API:

```shell
npm run mock:api
```

The generated mock is stateless. Mutation endpoints return the response declared by OpenAPI, but
they do not change data returned by later requests.
