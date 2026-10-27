import { flushPromises, mount } from "@vue/test-utils";
import { createPinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createMemoryHistory, createRouter } from "vue-router";
import { fetchAlerts, fetchDeployments, fetchRecommendations, fetchStackResources, fetchStacks } from "../api/overview";
import { fetchServices } from "../api/services";
import type { Alert, DeploymentSummary, Recommendation, ServiceInfo, StackView as Stack } from "../api/types";
import { useOverviewStore } from "../stores/overview";
import StackView from "./StackView.vue";

vi.mock("../api/overview", () => ({ fetchStacks: vi.fn(), fetchDeployments: vi.fn(), fetchAlerts: vi.fn(), fetchRecommendations: vi.fn(), fetchStackResources: vi.fn() }));
vi.mock("../api/services", () => ({ fetchServices: vi.fn() }));

const stack: Stack = { name: "app", compose_file: "app.yaml", status: { synced: 2, out_of_synced: 1 }, last_commit: "abcdef012345", last_deploy_at: "2026-10-10T10:00:00Z" };
const service = (name: string, sync_status: ServiceInfo["sync_status"], owner = "app"): ServiceInfo => ({ name, stack: owner, sync_status, type: "application", type_title: "Application", image: "app:2", image_version: "2" });
const alert = (id: string, resourceType: Alert["resourceType"] = "stack", resourceId = "app", status: Alert["status"] = "open"): Alert => ({
  id, fingerprint: id, kind: "sync_failed", resourceType, resourceId, status, title: `Alert ${id}`, message: "Apply failed", occurrences: 1,
  openedAt: "2026-10-10T10:00:00Z", updatedAt: "2026-10-10T10:00:00Z", openEventId: "event", latestEventId: "event",
});
const deployment: DeploymentSummary = {
  id: "d1", stack: "app", commit: "abcdef", status: "failed", phase: "apply", latest_attempt_id: "attempt-d1", attempt_count: 1,
  apply_status: "failed", verification_status: "skipped", cleanup_status: "skipped",
  actual_state_status: "unknown", comparison_basis: "none", comparison_status: "unknown", started_at: "2026-10-10T10:00:00Z", diff_available: true,
  summary: { added: 0, changed: 0, removed: 0, redacted: 0 }, resources: { services: 0, configs: 0, secrets: 0, networks: 0, volumes: 0 },
};

async function render(path = "/stacks/app") {
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: "/stacks/:stack?", name: "stack-details", component: StackView },
    { path: "/overview", component: { template: "<div />" } },
    { path: "/services/:stack/:service", name: "service-details", component: { template: "<div />" } },
    { path: "/deployments", name: "deployments", component: { template: "<div />" } },
    { path: "/recommendations", name: "recommendations", component: { template: "<div />" } },
    { path: "/events", name: "events", component: { template: "<div />" } },
  ] });
  await router.push(path);
  const pinia = createPinia();
  const store = useOverviewStore(pinia);
  for (const action of ["openDeploymentDetailsModal", "openAlertDetailsModal", "openCommitDetailsModal", "openStackManifestModal"] as const) vi.spyOn(store, action).mockImplementation(vi.fn());
  const wrapper = mount(StackView, { global: { plugins: [pinia, router] } });
  return { wrapper, router, store };
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(fetchStacks).mockResolvedValue({ stacks: [stack], sync: {} });
  vi.mocked(fetchServices).mockResolvedValue({ services: [service("api", "Synced"), service("worker", "OutOfSync"), service("pending", "unknown"), service("other", "Synced", "infra")] });
  vi.mocked(fetchDeployments).mockResolvedValue({ deployments: [deployment] });
  vi.mocked(fetchAlerts).mockResolvedValue({ alerts: [alert("own"), alert("other", "stack", "infra"), alert("node", "node"), alert("repo", "repository"), alert("closed", "stack", "app", "resolved")] });
  vi.mocked(fetchRecommendations).mockResolvedValue({ recommendations: [] });
  vi.mocked(fetchStackResources).mockResolvedValue({ networks: [{ id: "net", name: "app_default", driver: "overlay", scope: "swarm", internal: false, attachable: false, ingress: false, managed: true, stack_name: "app" }, { id: "foreign", name: "foreign", driver: "overlay", scope: "swarm", internal: false, attachable: false, ingress: false, managed: false }], secrets: [{ id: "s1", name: "app_password" }], configs: [{ id: "c1", name: "app_config" }] });
});

describe("Stack Details", () => {
  it("renders API counters, service metadata, related alerts and networks without inventing health", async () => {
    const { wrapper, store } = await render();
    await flushPromises();
    expect(wrapper.get("h2").text()).toBe("app");
    expect(wrapper.get(".overview-stack-status-badge").text()).toBe("Out of sync 1");
    // API aggregate is authoritative, even if the service metadata snapshot differs.
    expect(wrapper.findAll(".stack-details-summary dd").map((item) => item.text())).toEqual(["3", "2", "1", "1", "0"]);
    expect(wrapper.findAll(".services-cell-sync-status .status").map((item) => item.text())).toEqual(["Synced", "unknown", "OutOfSync"]);
    expect(wrapper.text()).toContain("Application");
    expect(wrapper.text()).toContain("app_default");
    for (const text of ["foreign", "Alert node", "Alert repo", "Alert other", "Alert closed", "healthy", "Repository", "Branch"]) expect(wrapper.text()).not.toContain(text);
    expect(wrapper.find('input[type="search"]').exists()).toBe(false);
    await wrapper.get('[aria-label="Open alert: Alert own"]').trigger("click");
    expect(store.openAlertDetailsModal).toHaveBeenCalledWith(expect.objectContaining({ id: "own" }));
    await wrapper.get('[aria-label="Open commit abcdef"]').trigger("click");
    expect(store.openCommitDetailsModal).toHaveBeenCalledWith(stack.last_commit);
    await wrapper.get(".stack-details-header > button").trigger("click");
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
    expect(wrapper.get(".overview-stack-status-badge").text()).toBe(label);
    // A failed deployment remains failed when the aggregate says Synced.
    expect(wrapper.get('[aria-label="Open failed deployment details"]').text()).toContain("Failed");
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
    expect(wrapper.text()).not.toContain("Stack not found");
    await wrapper.get("button").trigger("click");
    await flushPromises();
    expect(wrapper.get("h2").text()).toBe("app");
    wrapper.unmount();
  });

  it.each(["/stacks/absent", "/stacks"])("handles unknown or missing stacks: %s", async (path) => {
    const { wrapper } = await render(path);
    await flushPromises();
    expect(wrapper.text()).toContain("Stack not found.");
    expect(wrapper.find(".stack-details-summary").exists()).toBe(false);
    wrapper.unmount();
  });

  it("handles empty data and missing deployment metadata", async () => {
    vi.mocked(fetchStacks).mockResolvedValue({ stacks: [{ name: "app", compose_file: "app.yaml", status: { synced: 0, out_of_synced: 0 } }], sync: {} });
    vi.mocked(fetchServices).mockResolvedValue({ services: [] });
    vi.mocked(fetchDeployments).mockResolvedValue({ deployments: [] });
    vi.mocked(fetchAlerts).mockResolvedValue({ alerts: [] });
    vi.mocked(fetchStackResources).mockResolvedValue({ networks: [], secrets: [], configs: [] });
    const { wrapper } = await render();
    await flushPromises();
    for (const text of ["No services captured yet.", "No deployments yet.", "n/a"]) expect(wrapper.text()).toContain(text);
    expect(wrapper.find('[aria-labelledby="stack-alerts-title"]').exists()).toBe(false);
    expect(wrapper.get('[data-summary="alerts"] dd').text()).toBe("0");
    expect(wrapper.findAll(".stack-resource summary").map((item) => item.text())).toEqual(["Networks 0", "Secrets 0", "Configs 0"]);
    wrapper.unmount();
  });

  it("keeps optional API errors distinct from empty and zero states", async () => {
    vi.mocked(fetchDeployments).mockRejectedValue(new Error("history offline"));
    vi.mocked(fetchAlerts).mockRejectedValue(new Error("alerts offline"));
    vi.mocked(fetchStackResources).mockRejectedValue(new Error("networks offline"));
    const { wrapper } = await render();
    await flushPromises();
    expect(wrapper.get('[data-summary="alerts"] dd').text()).toBe("n/a");
    for (const text of ["history offline", "alerts offline", "networks offline"]) expect(wrapper.text()).toContain(text);
    expect(wrapper.text()).not.toContain("No active alerts.");
    expect(wrapper.find(".services-stack-table").exists()).toBe(true);
    wrapper.unmount();
  });

  it("does not display a misleading zero service count when service loading fails", async () => {
    vi.mocked(fetchServices).mockRejectedValue(new Error("services offline"));
    const { wrapper } = await render();
    await flushPromises();
    expect(wrapper.get('[role="alert"]').text()).toContain("services offline");
    expect(wrapper.find(".stack-details-summary").exists()).toBe(false);
    wrapper.unmount();
  });

  it("opens deployments and services without breadcrumbs or a history link", async () => {
    const { wrapper, router, store } = await render();
    await flushPromises();
    expect(fetchDeployments).toHaveBeenCalledWith({ stack: "app", limit: 3 });
    expect(fetchAlerts).toHaveBeenCalledWith({ status: "open", stack: "app" });
    expect(fetchStackResources).toHaveBeenCalledWith("app");
    expect(wrapper.find(".stack-history-link").exists()).toBe(false);
    expect(wrapper.find('[aria-label="Breadcrumb"]').exists()).toBe(false);
    expect(wrapper.text()).not.toContain("View all");
    await wrapper.get('[aria-label="Open failed deployment details"]').trigger("click");
    expect(store.openDeploymentDetailsModal).toHaveBeenCalledWith("d1");
    await wrapper.get('[aria-label="Open details for api"]').trigger("keydown", { key: "Enter" });
    await flushPromises();
    expect(router.currentRoute.value.fullPath).toBe("/services/app/api");
    wrapper.unmount();
  });


  it("shows only the latest three attempts in a compact table and isolates commit actions", async () => {
    vi.mocked(fetchDeployments).mockResolvedValue({ deployments: [1, 4, 2, 3].map((day) => ({
      ...deployment, id: `d${day}`, commit: `commit${day}`, started_at: `2026-10-0${day}T10:00:00Z`,
    })) });
    const { wrapper, store } = await render();
    await flushPromises();
    expect(wrapper.findAll(".stack-deployments-table th").map((item) => item.text())).toEqual(["Status", "Commit", "Timestamp"]);
    const rows = wrapper.findAll(".stack-deployments-table tbody tr");
    expect(rows).toHaveLength(3);
    expect(rows.map((row) => row.find("button").attributes("aria-label"))).toEqual([
      "Open deployment commit commit4", "Open deployment commit commit3", "Open deployment commit commit2",
    ]);
    await rows[0].get("button").trigger("click");
    await rows[0].get("button").trigger("keydown", { key: "Enter" });
    await rows[0].get("button").trigger("keydown", { key: " " });
    expect(store.openCommitDetailsModal).toHaveBeenCalledWith("commit4");
    expect(store.openDeploymentDetailsModal).not.toHaveBeenCalled();
    await rows[0].trigger("click");
    await rows[1].trigger("keydown", { key: "Enter" });
    await rows[2].trigger("keydown", { key: " " });
    expect(vi.mocked(store.openDeploymentDetailsModal).mock.calls).toEqual([["d4"], ["d3"], ["d2"]]);
    wrapper.unmount();
  });

  it("uses the existing deployment status labels and handles missing commits", async () => {
    vi.mocked(fetchDeployments).mockResolvedValue({ deployments: ["succeeded", "running", "interrupted"].map((status) => ({
      ...deployment, id: status, status: status as DeploymentSummary["status"], commit: "",
    })) });
    const { wrapper } = await render();
    await flushPromises();
    const badges = wrapper.findAll(".stack-deployments-table .status");
    expect(badges.map((badge) => badge.text())).toEqual(["Succeeded", "Running", "Interrupted"]);
    expect(badges[0].classes()).toContain("success");
    expect(badges[1].classes()).toContain("deployment-status-running");
    expect(badges[2].classes()).toContain("unknown");
    expect(wrapper.findAll(".stack-deployments-table button")).toHaveLength(0);
    wrapper.unmount();
  });

  it("omits empty alerts without hiding fetch errors or the summary count", async () => {
    vi.mocked(fetchAlerts).mockResolvedValue({ alerts: [] });
    const { wrapper } = await render();
    await flushPromises();
    expect(wrapper.find('[aria-labelledby="stack-alerts-title"]').exists()).toBe(false);
    expect(wrapper.get('[data-summary="alerts"] dd').text()).toBe("0");
    expect(wrapper.find(".stack-details-panels").exists()).toBe(false);
    wrapper.unmount();
  });

  it("keeps resources collapsed, expands names, and distinguishes unavailable counts from zero", async () => {
    const { wrapper } = await render();
    await flushPromises();
    const details = wrapper.findAll(".stack-resource");
    expect(details.map((item) => item.get("summary").text())).toEqual(["Networks 1", "Secrets 1", "Configs 1"]);
    for (const item of details) expect((item.element as HTMLDetailsElement).open).toBe(false);
    // Native details handles Enter/Space; model its open state to verify the expanded content.
    (details[0].element as HTMLDetailsElement).open = true;
    await details[0].trigger("toggle");
    expect(details[0].get("li").text()).toContain("app_default");
    expect(details[0].get("li").text()).toContain("overlay · swarm");
    expect(wrapper.find('[aria-labelledby="stack-resources-title"] table').exists()).toBe(false);
    expect(details[1].get("li").text()).toBe("app_password");
    expect(details[2].get("li").text()).toBe("app_config");
    wrapper.unmount();
  });


  it("counts recommendations only for the current stack without limiting the request", async () => {
    const recommendation = (stack: string, service: string): Recommendation => ({
      severity: "medium", type: "service.image.latest", subject: { stack, service },
      source: { file: "app.yaml", digest: "sha256:abc", commit: "abcdef" },
      title: "Pin image version", recommendation: "Use an explicit version", created_at: "2026-10-10T10:00:00Z",
    });
    vi.mocked(fetchRecommendations).mockResolvedValue({ recommendations: [
      recommendation("app", "api"), recommendation("app", "worker"), recommendation("infra", "proxy"),
    ] });
    const { wrapper } = await render();
    await flushPromises();
    expect(fetchRecommendations).toHaveBeenCalledWith({ stack: "app" });
    expect(wrapper.get('[data-summary="recommendations"] dd').text()).toBe("2");
    expect(wrapper.get('[data-summary="recommendations"] svg').classes()).toContain("summary-icon--warning");
    expect(wrapper.get('[data-summary="alerts"] svg').classes()).toContain("summary-icon--error");
    expect(wrapper.get('[data-summary="out-of-sync"] svg').classes()).toContain("summary-icon--warning");
    expect(wrapper.get(".stack-details-main").findAll(":scope > section")).toHaveLength(2);
    wrapper.unmount();
  });

  it("uses muted icons for zero recommendations, alerts, and OutOfSync", async () => {
    vi.mocked(fetchAlerts).mockResolvedValue({ alerts: [] });
    vi.mocked(fetchStacks).mockResolvedValue({ stacks: [{ ...stack, status: { synced: 1, out_of_synced: 0 } }], sync: {} });
    vi.mocked(fetchServices).mockResolvedValue({ services: [service("api", "Synced")] });
    const { wrapper } = await render();
    await flushPromises();
    for (const key of ["recommendations", "alerts", "out-of-sync"]) {
      expect(wrapper.get(`[data-summary="${key}"] dd`).text()).toBe("0");
      expect(wrapper.get(`[data-summary="${key}"] svg`).classes()).toContain("summary-icon--muted");
    }
    expect(wrapper.get('[data-summary="services"] svg').classes()).toContain("summary-icon--muted");
    expect(wrapper.get('[data-summary="synced"] svg').classes()).toContain("summary-icon--success");
    wrapper.unmount();
  });

  it("does not show a false zero while recommendations load", async () => {
    let resolve!: (value: { recommendations: Recommendation[] }) => void;
    vi.mocked(fetchRecommendations).mockReturnValueOnce(new Promise((done) => { resolve = done; }));
    const { wrapper } = await render();
    await flushPromises();
    expect(wrapper.get('[role="status"]').text()).toBe("Loading stack...");
    expect(wrapper.find('[data-summary="recommendations"]').exists()).toBe(false);
    resolve({ recommendations: [] });
    await flushPromises();
    expect(wrapper.get('[data-summary="recommendations"] dd').text()).toBe("0");
    wrapper.unmount();
  });

  it("shows an unknown recommendation count on API failure while preserving the page", async () => {
    vi.mocked(fetchRecommendations).mockRejectedValue(new Error("recommendations offline"));
    const { wrapper } = await render();
    await flushPromises();
    expect(wrapper.get('[data-summary="recommendations"] dd').text()).toBe("n/a");
    expect(wrapper.get('[data-summary="recommendations"] svg').classes()).toContain("summary-icon--muted");
    expect(wrapper.get('[role="alert"]').text()).toContain("recommendations offline");
    expect(wrapper.find(".services-stack-table").exists()).toBe(true);
    wrapper.unmount();
  });


  it.each([
    ["alerts", "/events?tab=alerts&stack_name=app"],
    ["recommendations", "/recommendations?stack_name=app"],
  ])("links the %s indicator to the filtered list", async (kind, destination) => {
    const { wrapper, router } = await render();
    await flushPromises();
    const link = wrapper.get(`[data-summary="${kind}"] a`);
    expect(link.attributes("href")).toBe(destination);
    await link.trigger("click");
    await flushPromises();
    expect(router.currentRoute.value.fullPath).toBe(destination);
    wrapper.unmount();
  });

  it("shows unknown counts for every resource group on API failure", async () => {
    vi.mocked(fetchStackResources).mockRejectedValue(new Error("resources unavailable"));
    const { wrapper } = await render();
    await flushPromises();
    expect(wrapper.findAll(".stack-resource summary").map((item) => item.text())).toEqual(["Networks n/a", "Secrets n/a", "Configs n/a"]);
    expect(wrapper.find(".services-stack-table").exists()).toBe(true);
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
    expect(fetchDeployments).toHaveBeenLastCalledWith({ stack: "infra", limit: 3 });
    expect(fetchRecommendations).toHaveBeenLastCalledWith({ stack: "infra" });
    expect(fetchStackResources).toHaveBeenLastCalledWith("infra");
    wrapper.unmount();
  });
});
