import { serve } from "@hono/node-server";
import { createMockServer } from "@scalar/mock-server";
import { Hono } from "hono";
import { fileURLToPath } from "node:url";

const hostname = "127.0.0.1";
const port = Number.parseInt(process.env.MOCK_API_PORT || "4010", 10);
const document = fileURLToPath(new URL("../../api/api-server.yaml", import.meta.url));

const openApiApp = await createMockServer({
  document,
  onRequest({ context }) {
    console.log(`[mock-api] ${context.req.method} ${context.req.path}`);
  },
});

const app = new Hono();

function deployment({ id, stack, commit, status, phase, startedAt, finishedAt, reason, services }) {
  const succeeded = status === "succeeded";
  const failed = status === "failed";
  const running = status === "running";

  return {
    id,
    stack,
    commit,
    status,
    phase,
    ...(reason ? { reason } : {}),
    apply_status: failed && phase === "apply" ? "failed" : succeeded || phase !== "apply" ? "succeeded" : "running",
    verification_status: failed && phase === "verification" ? "failed" : succeeded ? "succeeded" : running ? "pending" : "skipped",
    cleanup_status: succeeded ? "succeeded" : "skipped",
    actual_state_status: succeeded ? "observed" : "unknown",
    comparison_basis: succeeded ? "successful_baseline" : "last_attempt",
    comparison_status: succeeded ? "known" : "unknown",
    started_at: startedAt,
    ...(finishedAt ? { finished_at: finishedAt } : {}),
    summary: { added: 0, changed: services, removed: 0, redacted: 0 },
    resources: { services, configs: 1, secrets: 2, networks: 1, volumes: 0 },
    diff_available: true,
    latest_attempt_id: `${id}-attempt-1`,
    attempt_count: 1,
  };
}

function recentDeployments(stack) {
  return [
    deployment({
      id: `${stack}-deploy-104`, stack, commit: "893f4c2a1b7e", status: "succeeded", phase: "completed",
      startedAt: "2026-10-28T19:52:00Z", finishedAt: "2026-10-28T19:54:18Z", services: 3,
    }),
    deployment({
      id: `${stack}-deploy-103`, stack, commit: "893f4c2a1b7e", status: "running", phase: "apply",
      startedAt: "2026-10-28T19:49:00Z", services: 3,
    }),
    deployment({
      id: `${stack}-deploy-102`, stack, commit: "7a1be9d47c12", status: "succeeded", phase: "completed",
      startedAt: "2026-10-25T10:12:00Z", finishedAt: "2026-10-25T10:13:41Z", services: 1,
    }),
    deployment({
      id: `${stack}-deploy-101`, stack, commit: "5c3e2a72f8d4", status: "failed", phase: "verification",
      startedAt: "2026-10-25T09:44:00Z", finishedAt: "2026-10-25T09:45:03Z",
      reason: "Service web-api failed to start", services: 1,
    }),
  ];
}

function deploymentActivity(interval) {
  const weekly = interval !== "daily";
  const count = weekly ? 8 : 30;
  const start = new Date(weekly ? "2026-09-07T00:00:00Z" : "2026-09-29T00:00:00Z");
  const step = (weekly ? 7 : 1) * 24 * 60 * 60 * 1000;
  return Array.from({ length: count }, (_, index) => ({
    start: new Date(start.getTime() + index * step).toISOString(),
    succeeded: index % (weekly ? 2 : 5) === 0 ? (weekly ? 3 + (index % 3) : 1) : 0,
    failed: index === count - 1 || index === Math.max(0, count - 4) ? 1 : 0,
    running: index === count - 1 ? 1 : 0,
    interrupted: index === Math.max(0, count - 3) ? 1 : 0,
    superseded: index === Math.max(0, count - 6) ? 1 : 0,
  }));
}

app.get("/api/v1/deployments", (context) => {
  const stack = context.req.query("stack")?.trim() || "web";
  const limit = Number.parseInt(context.req.query("limit") || "4", 10);
  const deployments = recentDeployments(stack).slice(0, Number.isFinite(limit) ? Math.max(0, limit) : 4);
  console.log(`[mock-api] GET ${context.req.path} (recent deployment fixtures)`);
  return context.json({ deployments });
});

app.get("/api/v1/stacks/:stack/deployments/activity", (context) => {
  const interval = context.req.query("interval") === "daily" ? "daily" : "weekly";
  console.log(`[mock-api] GET ${context.req.path} (${interval} activity fixtures)`);
  return context.json({ buckets: deploymentActivity(interval) });
});

app.route("/", openApiApp);

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
