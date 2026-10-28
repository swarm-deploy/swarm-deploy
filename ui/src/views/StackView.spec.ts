import { flushPromises, mount } from "@vue/test-utils";
import { createPinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createMemoryHistory, createRouter } from "vue-router";

import { fetchAlerts, fetchDeployments, fetchStackResources, fetchStacks } from "../api/overview";
import { fetchServices } from "../api/services";
import type { Alert, DeploymentSummary, ServiceInfo, StackView as Stack } from "../api/types";
import { useOverviewStore } from "../stores/overview";
import StackView from "./StackView.vue";

vi.mock("../api/overview", () => ({
  fetchStacks: vi.fn(),
  fetchDeployments: vi.fn(),
  fetchAlerts: vi.fn(),
  fetchStackResources: vi.fn(),
  fetchStackDeploymentActivity: vi.fn(),
}));
vi.mock("../api/services", () => ({ fetchServices: vi.fn() }));

const stack: Stack = {
  name: "app",
  compose_file: "app.yaml",
  status: { synced: 2, out_of_synced: 1 },
  last_commit: "abcdef012345",
  last_deploy_at: "2026-10-10T10:00:00Z",
};
const service = (name: string, sync_status: ServiceInfo["sync_status"], owner = "app"): ServiceInfo => ({
  name,
  stack: owner,
  sync_status,
  type: "application",
  type_title: "Application",
  image: `registry.example/${name}:2`,
  image_version: "2",
});
const alert = (id: string, resourceType: Alert["resourceType"] = "stack", resourceId = "app", status: Alert["status"] = "open"): Alert => ({
  id,
  fingerprint: id,
  kind: "sync_failed",
  resourceType,
  resourceId,
  status,
  title: `Alert ${id}`,
  message: "Apply failed",
  occurrences: 1,
  openedAt: "2026-10-10T10:00:00Z",
  updatedAt: "2026-10-10T10:00:00Z",
  openEventId: "event",
  latestEventId: "event",
});
const deployment: DeploymentSummary = {
  id: "d1",
  stack: "app",
  commit: "abcdef",
  status: "failed",
  phase: "apply",
  latest_attempt_id: "attempt-d1",
  attempt_count: 1,
  apply_status: "failed",
  verification_status: "skipped",
  cleanup_status: "skipped",
  actual_state_status: "unknown",
  comparison_basis: "none",
  comparison_status: "unknown",
  started_at: "2026-10-10T10:00:00Z",
  diff_available: true,
  summary: { added: 0, changed: 0, removed: 0, redacted: 0 },
  resources: { services: 0, configs: 0, secrets: 0, networks: 0, volumes: 0 },
};

async function render(path = "/stacks/app") {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: "/stacks/:stack?", name: "stack-details", component: StackView },
      { path: "/services", name: "services", component: { template: "<div />" } },
      { path: "/services/:stack/:service", name: "service-details", component: { template: "<div />" } },
      { path: "/deployments", name: "deployments", component: { template: "<div />" } },
    ],
  });
  await router.push(path);
  const pinia = createPinia();
  const store = useOverviewStore(pinia);
  for (const action of ["openDeploymentDetailsModal", "openAlertDetailsModal", "openCommitDetailsModal", "openStackManifestModal"] as const) {
    vi.spyOn(store, action).mockImplementation(vi.fn());
  }
  const wrapper = mount(StackView, {
    global: {
      plugins: [pinia, router],
      stubs: {
        DeploymentActivityChart: {
          props: ["stack"],
          template: '<div class="deployment-activity-stub" :data-stack="stack" />',
        },
      },
    },
  });
  return { wrapper, router, store };
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(fetchStacks).mockResolvedValue({ stacks: [stack], sync: {} });
  vi.mocked(fetchServices).mockResolvedValue({
    services: [service("api", "Synced"), service("worker", "OutOfSync"), service("pending", "unknown"), service("other", "Synced", "infra")],
  });
  vi.mocked(fetchDeployments).mockResolvedValue({ deployments: [deployment] });
  vi.mocked(fetchAlerts).mockResolvedValue({
    alerts: [alert("own"), alert("other", "stack", "infra"), alert("node", "node"), alert("closed", "stack", "app", "resolved")],
  });
  vi.mocked(fetchStackResources).mockResolvedValue({
    networks: [
      { id: "net", name: "app_default", driver: "overlay", scope: "swarm", internal: false, attachable: false, ingress: false, managed: true, stack_name: "app" },
      { id: "foreign", name: "foreign", driver: "overlay", scope: "swarm", internal: false, attachable: false, ingress: false, managed: false },
    ],
    secrets: [{ id: "s1", name: "app_password" }],
    configs: [{ id: "c1", name: "app_config" }],
  });
});

describe("Stack Details", () => {
  it("renders the mockup layout with real API data and keeps primary actions", async () => {
    const { wrapper, store } = await render();
    await flushPromises();

    expect(wrapper.get("h2").text()).toBe("app");
    expect(wrapper.get(".stack-state").text()).toContain("Out of sync 1");
    expect(wrapper.find('[aria-label="Breadcrumb"]').exists()).toBe(false);
    expect(wrapper.findAll(".stack-metrics dd").map((item) => item.text())).toEqual(["3", "2", "1", "1", "1", "1"]);
    expect(wrapper.findAll(".services-stack-table th").map((item) => item.text())).toEqual(["Name", "Version", "Type", "Status"]);
    expect(wrapper.find(".services-stack-table .deployment-icon").exists()).toBe(false);
    expect(wrapper.get(".services-cell-image").text()).toBe("2");
    expect(wrapper.get(".services-cell-image").attributes("title")).toBe("registry.example/api:2");
    expect(wrapper.findAll(".services-cell-sync-status .status").map((item) => item.text())).toEqual(["Synced", "unknown", "OutOfSync"]);
    expect(wrapper.get(".stack-services-panel .service-panel-heading").text()).toBe("Services");
    expect(wrapper.get(".deployment-activity-stub").attributes("data-stack")).toBe("app");
    expect(wrapper.get(".stack-deployments-panel .overview-summary-panel-header-link").attributes("href")).toBe("/deployments?stack=app");
    expect(wrapper.text()).not.toContain("foreign");

    await wrapper.get('[aria-label="Open alert: Alert own"]').trigger("click");
    await wrapper.get('[aria-label="Open commit abcdef"]').trigger("click");
    await wrapper.get(".stack-compare-button").trigger("click");
    expect(store.openAlertDetailsModal).toHaveBeenCalledWith(expect.objectContaining({ id: "own" }));
    expect(store.openCommitDetailsModal).toHaveBeenCalledWith(stack.last_commit);
    expect(store.openStackManifestModal).toHaveBeenCalledWith("app");
    wrapper.unmount();
  });

  it.each([
    [{ synced: 0, out_of_synced: 0 }, "Unknown"],
    [{ synced: 3, out_of_synced: 0 }, "Synced"],
    [{ synced: 3, out_of_synced: 2 }, "Out of sync 2"],
  ])("preserves aggregate synchronization for %j", async (status, label) => {
    vi.mocked(fetchStacks).mockResolvedValue({ stacks: [{ ...stack, status }], sync: {} });
    const { wrapper } = await render();
    await flushPromises();
    expect(wrapper.get(".stack-state").text()).toContain(label);
    expect(wrapper.get('[aria-label="Open failed deployment details"]').text()).toContain("Deploy failed");
    wrapper.unmount();
  });

  it("shows loading, then a retriable API error", async () => {
    let reject!: (reason: Error) => void;
    vi.mocked(fetchStacks).mockReturnValueOnce(new Promise((_, fail) => { reject = fail; }));
    const { wrapper } = await render();
    expect(wrapper.text()).toContain("Loading stack...");
    reject(new Error("offline"));
    await flushPromises();
    expect(wrapper.get('[role="alert"]').text()).toContain("offline");
    await wrapper.get("button").trigger("click");
    await flushPromises();
    expect(wrapper.get("h2").text()).toBe("app");
    wrapper.unmount();
  });

  it.each(["/stacks/absent", "/stacks"])("handles unknown or missing stacks: %s", async (path) => {
    const { wrapper } = await render(path);
    await flushPromises();
    expect(wrapper.text()).toContain("Stack not found.");
    expect(wrapper.find(".stack-metrics").exists()).toBe(false);
    wrapper.unmount();
  });

  it("renders honest empty and unavailable states", async () => {
    vi.mocked(fetchServices).mockResolvedValue({ services: [] });
    vi.mocked(fetchDeployments).mockResolvedValue({ deployments: [] });
    vi.mocked(fetchAlerts).mockRejectedValue(new Error("alerts offline"));
    vi.mocked(fetchStackResources).mockRejectedValue(new Error("resources offline"));
    const { wrapper } = await render();
    await flushPromises();
    expect(wrapper.text()).toContain("No services captured yet.");
    expect(wrapper.text()).toContain("No deployments yet.");
    expect(wrapper.text()).toContain("alerts offline");
    for (const key of ["networks", "secrets", "configs"]) {
      expect(wrapper.get(`[data-summary="${key}"] dd`).text()).toBe("n/a");
    }
    wrapper.unmount();
  });

  it("shows all services without a local search and opens a service with the keyboard", async () => {
    const { wrapper, router } = await render();
    await flushPromises();
    expect(wrapper.find('input[type="search"]').exists()).toBe(false);
    expect(wrapper.findAll(".services-stack-table tbody tr")).toHaveLength(3);
    await wrapper.get('[aria-label="Open details for worker"]').trigger("keydown", { key: "Enter" });
    await flushPromises();
    expect(router.currentRoute.value.fullPath).toBe("/services/app/worker");
    wrapper.unmount();
  });

  it("shows the latest four deployments as a timeline and isolates commit actions", async () => {
    vi.mocked(fetchDeployments).mockResolvedValue({
      deployments: [1, 5, 2, 4, 3].map((day) => ({
        ...deployment,
        id: `d${day}`,
        commit: `commit${day}`,
        started_at: `2026-10-0${day}T10:00:00Z`,
      })),
    });
    const { wrapper, store } = await render();
    await flushPromises();
    expect(fetchDeployments).toHaveBeenCalledWith({ stack: "app", limit: 4 });
    const rows = wrapper.findAll(".stack-deployment");
    expect(rows).toHaveLength(4);
    expect(rows.map((row) => row.get("button").attributes("aria-label"))).toEqual([
      "Open deployment commit commit5",
      "Open deployment commit commit4",
      "Open deployment commit commit3",
      "Open deployment commit commit2",
    ]);
    await rows[0].get("button").trigger("click");
    expect(store.openCommitDetailsModal).toHaveBeenCalledWith("commit5");
    expect(store.openDeploymentDetailsModal).not.toHaveBeenCalled();
    await rows[0].trigger("click");
    await rows[1].trigger("keydown", { key: "Enter" });
    await rows[2].trigger("keydown", { key: " " });
    expect(vi.mocked(store.openDeploymentDetailsModal).mock.calls).toEqual([["d5"], ["d4"], ["d3"]]);
    wrapper.unmount();
  });

  it("renders deployment states and summaries without requiring commits", async () => {
    vi.mocked(fetchDeployments).mockResolvedValue({ deployments: [
      { ...deployment, id: "ok", status: "succeeded", phase: "completed", commit: "", resources: { ...deployment.resources, services: 3 } },
      { ...deployment, id: "run", status: "running", commit: "", reason: "Applying services" },
      { ...deployment, id: "stop", status: "interrupted", commit: "" },
    ] });
    const { wrapper } = await render();
    await flushPromises();
    expect(wrapper.findAll(".stack-deployment-heading strong").map((item) => item.text())).toEqual([
      "Deploy completed", "Deploy started", "Deploy interrupted",
    ]);
    expect(wrapper.findAll(".stack-deployment .stack-commit-button")).toHaveLength(0);
    expect(wrapper.text()).toContain("3 services processed");
    wrapper.unmount();
  });

  it("hides the alerts panel when there are no active alerts", async () => {
    vi.mocked(fetchAlerts).mockResolvedValue({ alerts: [] });
    const { wrapper } = await render();
    await flushPromises();
    expect(wrapper.find('[aria-labelledby="stack-alerts-title"]').exists()).toBe(false);
    wrapper.unmount();
  });

  it("reloads when route parameters change and ignores an obsolete response", async () => {
    let resolve!: (value: Awaited<ReturnType<typeof fetchStacks>>) => void;
    vi.mocked(fetchStacks).mockReturnValueOnce(new Promise((done) => { resolve = done; }));
    const { wrapper, router } = await render();
    vi.mocked(fetchStacks).mockResolvedValue({ stacks: [{ ...stack, name: "infra" }], sync: {} });
    await router.push("/stacks/infra");
    await flushPromises();
    expect(wrapper.get("h2").text()).toBe("infra");
    resolve({ stacks: [stack], sync: {} });
    await flushPromises();
    expect(wrapper.get("h2").text()).toBe("infra");
    expect(fetchDeployments).toHaveBeenLastCalledWith({ stack: "infra", limit: 4 });
    expect(fetchStackResources).toHaveBeenLastCalledWith("infra");
    wrapper.unmount();
  });
});
