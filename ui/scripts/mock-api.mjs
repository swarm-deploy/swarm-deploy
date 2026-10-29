import { serve } from "@hono/node-server";
import { createMockServer } from "@scalar/mock-server";
import { Hono } from "hono";
import { streamSSE } from "hono/streaming";
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

const swarmServices = [
  {
    id: "svc-web-api-01", name: "web-api", full_name: "platform_web-api", stack: "platform",
    image: "registry.example.test/platform/web-api:v2.14.3", mode: "replicated",
    replicas: { running: 3, desired: 3 }, updated_at: "2026-10-28T20:54:18Z",
  },
  {
    id: "svc-worker-02", name: "events-worker", full_name: "platform_events-worker", stack: "platform",
    image: "registry.example.test/platform/events-worker:v4.8.0", mode: "replicated",
    replicas: { running: 1, desired: 3 }, updated_at: "2026-10-28T20:53:42Z",
  },
  {
    id: "svc-reports-03", name: "reports", full_name: "backoffice_reports", stack: "backoffice",
    image: "registry.example.test/backoffice/reports:2026.10.28", mode: "replicated",
    replicas: { running: 0, desired: 0 }, updated_at: "2026-10-28T18:12:09Z",
  },
  {
    id: "svc-agent-04", name: "node-metrics-agent", full_name: "monitoring_node-metrics-agent", stack: "monitoring",
    image: "registry.example.test/observability/node-metrics-agent:v1.22.1@sha256:6ee34dfdf8ac7248df53b94ed531f276b947211d20af27cc93bf97d9554a312f",
    mode: "global", replicas: { running: 4, desired: 4 }, updated_at: "2026-10-28T20:51:33Z",
  },
  {
    id: "svc-standalone-05", name: "debug-proxy", full_name: "debug-proxy", stack: "",
    image: "docker.io/library/nginx:1.27-alpine", mode: "replicated",
    replicas: { running: 1, desired: 1 }, updated_at: "2026-10-28T19:37:50Z",
  },
  {
    id: "svc-long-06", name: "customer-notification-delivery-reconciliation-worker", full_name: "messaging_customer-notification-delivery-reconciliation-worker", stack: "messaging",
    image: "registry.example.test/very-long-organization/customer-notification-delivery-reconciliation-worker:release-2026-10-28.1842",
    mode: "replicated", replicas: { running: 2, desired: 2 }, updated_at: "2026-10-28T20:50:04Z",
  },
];

const tasksByService = {
  "svc-web-api-01": [
    { id: "task-web-01-running-current", slot: 1, node: "node-manager-01", node_name: "manager-1", desired_state: "running", current_state: "running", container_id: "container-web-01", created_at: "2026-10-28T20:43:10Z", updated_at: "2026-10-28T20:43:17Z" },
    { id: "task-web-02-running-current", slot: 2, node: "node-worker-01", node_name: "worker-1", desired_state: "running", current_state: "running", container_id: "container-web-02", created_at: "2026-10-28T20:43:12Z", updated_at: "2026-10-28T20:43:19Z" },
    { id: "task-web-03-preparing-current", slot: 3, node: "node-worker-02", node_name: "worker-2", desired_state: "running", current_state: "preparing", container_id: "", created_at: "2026-10-28T20:53:12Z", updated_at: "2026-10-28T20:54:01Z" },
    { id: "task-web-03-failed-history", slot: 3, node: "node-worker-02", node_name: "worker-2", desired_state: "shutdown", current_state: "failed", container_id: "container-web-03-old", exit_code: 137, error: "task: non-zero exit (137): container was terminated", created_at: "2026-10-28T20:41:02Z", updated_at: "2026-10-28T20:52:48Z" },
  ],
  "svc-worker-02": [
    { id: "task-worker-01-running", slot: 1, node: "node-worker-01", node_name: "worker-1", desired_state: "running", current_state: "running", container_id: "container-worker-01", created_at: "2026-10-28T18:01:00Z", updated_at: "2026-10-28T20:52:00Z" },
    { id: "task-worker-02-failed", slot: 2, node: "node-worker-02", node_name: "worker-2", desired_state: "running", current_state: "failed", container_id: "container-worker-02", exit_code: 1, error: "connection refused while opening synthetic queue endpoint", created_at: "2026-10-28T20:40:00Z", updated_at: "2026-10-28T20:52:20Z" },
    { id: "task-worker-03-pending", slot: 3, node: "", desired_state: "running", current_state: "pending", container_id: "", created_at: "2026-10-28T20:52:30Z", updated_at: "2026-10-28T20:52:30Z" },
  ],
};

for (const service of swarmServices) {
  tasksByService[service.full_name] = tasksByService[service.id] || [];
}

function encoded(value) {
  return Buffer.from(value, "utf8").toString("base64");
}

function taskDetails(task) {
  const failed = task.current_state === "failed";
  return {
    id: task.id,
    service_id: task.id.includes("worker") ? "svc-worker-02" : "svc-web-api-01",
    node_id: task.node,
    slot: task.slot,
    desired_state: task.desired_state,
    current_state: task.current_state,
    ...(task.error ? { error: task.error } : {}),
    created_at: task.created_at,
    updated_at: task.updated_at,
    container_id: task.container_id,
    ...(task.exit_code !== undefined ? { exit_code: task.exit_code } : {}),
    image: task.id.includes("worker")
      ? "registry.example.test/platform/events-worker:v4.8.0"
      : "registry.example.test/platform/web-api:v2.14.3",
    environment: [
      { name: "APP_ENV", value: "production", encoded: false },
      { name: "LOG_LEVEL", value: "info", encoded: false },
      { name: "PORT", value: "8080", encoded: false },
      { name: "FEATURE_FLAGS", value: "audit=true=verbose", encoded: false },
      { name: "EMPTY_VALUE", value: "", encoded: false },
      { name: "WELCOME_MESSAGE", value: "Первая строка\nВторая строка", encoded: false },
      { name: "SERVICE_BASE_URL", value: "https://api.platform.example.test/v2/organizations/synthetic-organization/environments/production/services/realtime-task-details?include=health,deployments,alerts", encoded: false },
      { name: "CACHE_DIRECTORY", value: "/var/lib/swarm-deploy/runtime/cache/platform/web-api/releases/2026-10-28/assets/generated/metadata", encoded: false },
      { name: "DB_DSN", value: encoded("postgres://realtime_user:synthetic-password@db.example.test:5432/app?sslmode=require"), encoded: true },
      { name: "API_TOKEN", value: encoded("synthetic-realtime-token-not-valid"), encoded: true },
      { name: "UTF8_SECRET", value: encoded("Секрет=значение"), encoded: true },
      { name: "WEBHOOK_SIGNING_KEY", value: encoded("synthetic-webhook-signing-key-not-valid"), encoded: true },
      { name: "LONG_SETTING", value: "region=eu-west-1;features=events,alerts,reports;description=deterministic mock value for responsive layout verification", encoded: false },
      { name: "REQUEST_TIMEOUT", value: "30s", encoded: false },
    ],
    reservations: { nano_cpus: 250000000, memory_bytes: 134217728 },
    limits: { nano_cpus: 1000000000, memory_bytes: 536870912 },
    ...(failed ? { exit_code: task.exit_code ?? 1 } : {}),
  };
}

const allTasks = Object.values(tasksByService).flat();
const uniqueTasks = new Map(allTasks.map((task) => [task.id, task]));

app.get("/api/v1/swarm/services", (context) => context.json({ services: swarmServices }));

app.get("/api/v1/swarm/services/:service/tasks", (context) => {
  const tasks = tasksByService[context.req.param("service")] || [];
  return context.json({ tasks });
});

app.get("/api/v1/swarm/tasks/:task", (context) => {
  const task = uniqueTasks.get(context.req.param("task"));
  if (!task) return context.json({ error_message: "Task not found" }, 404);
  context.header("Cache-Control", "no-store");
  return context.json(taskDetails(task));
});

app.get("/api/tasks/:task/logs", (context) => streamSSE(context, async (stream) => {
  const parse = context.req.query("parse") === "true";
  if (parse) {
    await stream.writeSSE({ event: "counts", data: JSON.stringify({ counts: { INFO: 3, WARN: 1, ERROR: 1 } }) });
  }
  const entries = [
    { timestamp: "2026-10-28T20:54:10.123456Z", stream: "stdout", level: "INFO", message: "service request completed", attributes: { request_id: "mock-request-1", status: 200 }, raw_message: "level=info status=200 request_id=mock-request-1 msg=\"service request completed\"" },
    { timestamp: "2026-10-28T20:54:11.004Z", stream: "stderr", level: "WARN", message: "synthetic retry scheduled", attributes: { attempt: 2 }, raw_message: "level=warn attempt=2 msg=\"synthetic retry scheduled\"" },
    { timestamp: "2026-10-28T20:54:12.010Z", stream: "stdout", level: "INFO", message: "worker heartbeat", attributes: { node: "worker-1" }, raw_message: "worker heartbeat node=worker-1" },
  ];
  for (const entry of entries) {
    const payload = parse ? entry : { timestamp: entry.timestamp, stream: entry.stream, message: entry.raw_message };
    await stream.writeSSE({ event: "log", data: JSON.stringify(payload) });
  }
  await stream.writeSSE({ event: "eof", data: "{}" });
}));

app.get("/api/v1/deployments/filters", (context) => {
  console.log(`[mock-api] GET ${context.req.path} (deployment filter fixtures)`);
  return context.json({
    stacks: ["web", "worker", "infra"],
    services: [
      { stack_name: "web", service_name: "api" },
      { stack_name: "web", service_name: "frontend" },
      { stack_name: "worker", service_name: "queue" },
      { stack_name: "infra", service_name: "proxy" },
    ],
  });
});

app.get("/api/v1/deployments", (context) => {
  const stacks = context.req.queries("stack")?.map((stack) => stack.trim()).filter(Boolean) ?? [];
  const services = context.req.queries("service_name")?.map((service) => service.trim()).filter(Boolean) ?? [];
  const stackServices = { web: ["api", "frontend"], worker: ["queue"], infra: ["proxy"] };
  const candidateStacks = stacks.length > 0 ? stacks : services.length > 0 ? Object.keys(stackServices) : ["web"];
  const selectedStacks = candidateStacks.filter((stack) => (
    services.length === 0 || services.some((service) => stackServices[stack]?.includes(service))
  ));
  const limit = Number.parseInt(context.req.query("limit") || "4", 10);
  const deployments = selectedStacks.flatMap(recentDeployments)
    .slice(0, Number.isFinite(limit) ? Math.max(0, limit) : 4);
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
