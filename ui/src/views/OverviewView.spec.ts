import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createMemoryHistory, createRouter } from "vue-router";
import type { StackView } from "../api/types";
import OverviewView from "./OverviewView.vue";
import { fetchDeployments, fetchEvents } from "../api/overview";

const store = vi.hoisted(() => ({
  syncInfo: null, services: [], stacks: [] as StackView[], loading: false, loadingError: "",
  loadOverview: vi.fn().mockResolvedValue(undefined),
  openCommitDetailsModal: vi.fn(), openDeploymentDetailsModal: vi.fn(), openStackManifestModal: vi.fn(), openAlertDetailsModal: vi.fn(),
}));
vi.mock("../stores/overview", () => ({ useOverviewStore: () => store }));
vi.mock("../api/overview", () => ({
  fetchDeployments: vi.fn(),
  fetchEvents: vi.fn(),
  fetchAlerts: vi.fn().mockResolvedValue({ alerts: [] }),
  fetchRecommendations: vi.fn().mockResolvedValue({ recommendations: [] }),
}));

describe("Latest deployments", () => {
  beforeEach(() => vi.clearAllMocks());

  it("restores the compact pre-PR rows while using deployment records", async () => {
    vi.mocked(fetchDeployments).mockResolvedValue({ deployments: ["running", "succeeded", "failed", "interrupted"].map((status, i) => ({
      id: String(i), stack: i === 0 ? "a-very-long-stack-name-that-needs-truncation" : "app", commit: "abc123", status: status as "running" | "succeeded" | "failed" | "interrupted",
		latest_attempt_id: `attempt-${i}`, attempt_count: 1,
      phase: "apply", apply_status: "running", verification_status: "pending", cleanup_status: "pending",
      actual_state_status: "unknown", comparison_basis: i === 0 ? "none" : "successful_baseline", comparison_status: i === 0 ? "unknown" : "known",
      started_at: "2026-10-10T10:00:00Z", diff_available: i !== 3,
      summary: i === 2 ? { added: 0, changed: 0, removed: 0, redacted: 0 } : { added: 1, changed: 2, removed: 3, redacted: 0 },
      resources: { services: 0, configs: 0, secrets: 0, networks: 0, volumes: 0 },
    })) });
    const wrapper = mount(OverviewView, { global: { stubs: { RouterLink: { template: "<a><slot /></a>" } } } });
    await flushPromises();

    expect(fetchDeployments).toHaveBeenCalledWith({ limit: 4 });
    expect(fetchEvents).not.toHaveBeenCalled();
    for (const status of ["running", "succeeded", "failed", "interrupted"]) {
      expect(wrapper.find(`[aria-label^="Deployment ${status}."]`).exists()).toBe(true);
    }
    expect(wrapper.find('[aria-label^="Deployment running."]').classes()).toContain("overview-deployment-result--running");
    expect(wrapper.find('[aria-label^="Deployment succeeded."]').classes()).toContain("overview-deployment-result--success");
    expect(wrapper.find('[aria-label^="Deployment failed."]').classes()).toContain("overview-deployment-result--failed");
    const interrupted = wrapper.find('[aria-label^="Deployment interrupted."]');
    expect(interrupted.classes()).toContain("overview-deployment-result--interrupted");
    expect(interrupted.classes()).not.toContain("overview-deployment-result--failed");
    expect(wrapper.findAll(".overview-deployment-stack")).toHaveLength(4);
    expect(wrapper.findAll(".overview-deployment-row")).toHaveLength(4);
    expect(wrapper.findAll(".overview-deployment-row")[0].attributes("title")).toBeUndefined();
    expect(wrapper.findAll(".overview-deployment-details")[0].attributes("title")).toBe("Initial snapshot");
    expect(wrapper.findAll(".overview-deployment-details")[2].attributes("title")).toBe("No configuration changes");
    expect(wrapper.findAll(".overview-deployment-details")[3].attributes("title")).toBe("Changes unavailable");
    expect(wrapper.find(".overview-deployment-status").exists()).toBe(false);
    expect(wrapper.find(".overview-summary-secondary").exists()).toBe(false);

    await wrapper.findAll(".overview-deployment-details")[0].trigger("click");
    expect(store.openDeploymentDetailsModal).toHaveBeenCalledWith("0");
    expect(store.openCommitDetailsModal).not.toHaveBeenCalled();

    await wrapper.findAll(".overview-summary-sha-badge")[0].trigger("click");
    expect(store.openCommitDetailsModal).toHaveBeenCalledWith("abc123");
    expect(store.openDeploymentDetailsModal).toHaveBeenCalledTimes(1);
    wrapper.unmount();
  });
});

describe("Overview stack navigation", () => {
  it("uses a full-card native link and keeps Compare and Commit independent", async () => {
    store.stacks = [{ name: "app", compose_file: "app.yaml", status: { synced: 1, out_of_synced: 0 }, last_commit: "abcdef123" }];
    vi.mocked(fetchDeployments).mockResolvedValue({ deployments: [] });
    const router = createRouter({ history: createMemoryHistory(), routes: [
      { path: "/overview", component: OverviewView },
      { path: "/stacks/:stack", name: "stack-details", component: { template: "<div />" } },
      { path: "/deployments", component: { template: "<div />" } },
      { path: "/events", component: { template: "<div />" } },
      { path: "/recommendations", component: { template: "<div />" } },
    ] });
    await router.push("/overview");
    const wrapper = mount(OverviewView, { global: { plugins: [router] } });
    await flushPromises();
    const card = wrapper.get(".overview-stack-card");
    const link = card.get("a.stack-card-link");
    expect(link.attributes("href")).toBe("/stacks/app");
    expect(link.attributes("tabindex")).not.toBe("-1");
    await card.get(".overview-stack-card-action").trigger("click");
    await card.get(".overview-stack-commit-badge").trigger("click");
    await flushPromises();
    expect(store.openStackManifestModal).toHaveBeenCalledWith("app");
    expect(store.openCommitDetailsModal).toHaveBeenCalledWith("abcdef123");
    expect(router.currentRoute.value.path).toBe("/overview");
    await link.trigger("click");
    await flushPromises();
    expect(router.currentRoute.value.path).toBe("/stacks/app");
    wrapper.unmount();
    store.stacks = [];
  });
});
