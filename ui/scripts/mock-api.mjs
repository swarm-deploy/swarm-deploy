import { serve } from "@hono/node-server";
import { createMockServer } from "@scalar/mock-server";
import { fileURLToPath } from "node:url";

const hostname = "127.0.0.1";
const port = 4010;
const document = fileURLToPath(new URL("../../api/api-server.yaml", import.meta.url));

const app = await createMockServer({
  document,
  onRequest({ context }) {
    console.log(`[mock-api] ${context.req.method} ${context.req.path}`);
  },
});

serve(
  {
    fetch: app.fetch,
    hostname,
    port,
  },
  () => {
    console.log(`[mock-api] OpenAPI mock server is listening on http://${hostname}:${port}`);
  },
);
