import { afterEach, expect, it, vi } from "vitest";
import { fetchAlerts, fetchDeployments, fetchServiceRealtime, fetchStackDeploymentActivity, fetchStackResources } from "./overview";

afterEach(() => vi.unstubAllGlobals());
it("encodes stack resource paths and passes the alert stack filter", async () => {
  const fetch = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });
  vi.stubGlobal("fetch", fetch);
  await fetchStackResources("app/name");
  expect(fetch).toHaveBeenLastCalledWith("/api/v1/stacks/app%2Fname/resources", undefined);
  await fetchAlerts({ status: "open", stack: "app/name", limit: 3 });
  expect(fetch).toHaveBeenLastCalledWith("/api/v1/alerts?stack=app%2Fname&status=open&limit=3", undefined);
  await fetchStackDeploymentActivity("app/name", "daily");
  expect(fetch).toHaveBeenLastCalledWith(
    "/api/v1/stacks/app%2Fname/deployments/activity?interval=daily",
    undefined,
  );
});

it("uses the runtime task endpoint with the full Docker service name", async () => {
  const fetch = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({ tasks: [] }) });
  vi.stubGlobal("fetch", fetch);

  await fetchServiceRealtime("payments_api");

  expect(fetch).toHaveBeenLastCalledWith("/api/v1/swarm/services/payments_api/tasks", undefined);
});

it("encodes repeated deployment filters", async () => {
  const fetch = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({ deployments: [] }) });
  vi.stubGlobal("fetch", fetch);

  await fetchDeployments({ stack: ["app", "infra"], serviceName: ["api", "worker"], limit: 20 });

  expect(fetch).toHaveBeenLastCalledWith(
    "/api/v1/deployments?stack=app&stack=infra&service_name=api&service_name=worker&limit=20",
    undefined,
  );
});
