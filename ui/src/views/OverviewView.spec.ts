import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createMemoryHistory, createRouter } from "vue-router";
import { ApiError } from "../api/client";
import type { StackView, SyncInfo } from "../api/types";
import OverviewView from "./OverviewView.vue";
import { fetchDeployments, fetchEvent, fetchEvents } from "../api/overview";

const store = vi.hoisted(() => ({
  syncInfo: null as SyncInfo | null, services: [], stacks: [] as StackView[], loading: false, loadingError: "",
  loadOverview: vi.fn().mockResolvedValue(undefined),
  syncPending: false, triggerManualSync: vi.fn().mockResolvedValue(undefined),
  openCommitDetailsModal: vi.fn(), openDeploymentDetailsModal: vi.fn(), openStackManifestModal: vi.fn(), openAlertDetailsModal: vi.fn(),
}));
vi.mock("../stores/overview", () => ({ useOverviewStore: () => store }));
vi.mock("../api/overview", () => ({
  fetchDeployments: vi.fn(),
  fetchEvent: vi.fn(),
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

describe("GitOps status bar", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    store.syncInfo = null;
    store.stacks = [];
    store.loadingError = "";
    store.syncPending = false;
    vi.mocked(fetchDeployments).mockResolvedValue({ deployments: [] });
  });

  it("renders present sections, opens webhook and commit details, and owns Sync now", async () => {
    store.syncInfo = {
      last_poll: { at: "2026-11-04T13:57:43Z", result: "no_change", error: "" },
      last_webhook: {
        event_id: "c0e8a2f1-1234-4567-890a-123456789abc",
        at: "2026-11-04T13:42:07Z",
        queued: true,
      },
      last_revision: "6b220e1234567890abcdef1234567890abcdef12",
    };
    vi.mocked(fetchEvent).mockResolvedValue({
      id: "c0e8a2f1-1234-4567-890a-123456789abc",
      type: "WebhookReceived",
      severity: "info",
      category: "sync",
      created_at: "2026-11-04T13:42:07Z",
      message: "Webhook received",
      details: { queued: "true" },
    });
    const wrapper = mount(OverviewView, { global: { stubs: { RouterLink: { template: "<a><slot /></a>" } } } });
    await flushPromises();

    expect(wrapper.text()).toContain("Last poll:");
    expect(wrapper.text()).toContain("Last webhook:");
    expect(wrapper.text()).toContain("Last revision:");
    expect(wrapper.findAll(".overview-sync-separator")).toHaveLength(2);
    expect(wrapper.get(".overview-sync-indicator--success").text()).toBe("✓");
    expect(wrapper.findAll(".overview-sync-label")).toHaveLength(3);
    expect(wrapper.find(".overview-sync-panel strong").exists()).toBe(false);
    expect(wrapper.get(".overview-sync-button").element.parentElement).toBe(wrapper.get(".overview-sync-row").element);
    expect(wrapper.get(".overview-sync-button").element.parentElement).not.toBe(wrapper.get(".overview-sync-panel").element);

    await wrapper.get(".overview-webhook-details").trigger("click");
    expect(fetchEvent).toHaveBeenCalledWith("c0e8a2f1-1234-4567-890a-123456789abc");
    await flushPromises();
    expect(wrapper.get('[aria-label="Event metadata"]').text()).toContain("WebhookReceived");
    expect(wrapper.get(".overview-event-details").text()).toContain("queued");
    expect(wrapper.get(".overview-event-details").text()).toContain("true");

    await wrapper.get(".overview-sync-commit-badge").trigger("click");
    expect(store.openCommitDetailsModal).toHaveBeenCalledWith("6b220e1234567890abcdef1234567890abcdef12");
    expect(wrapper.get(".overview-sync-commit-badge").find("svg").exists()).toBe(false);

    await wrapper.get(".overview-sync-button").trigger("click");
    expect(store.triggerManualSync).toHaveBeenCalledOnce();
    wrapper.unmount();
  });

  it("shows empty present objects and hides absent sections with their separators", async () => {
    store.syncInfo = { last_webhook: {} };
    const wrapper = mount(OverviewView, { global: { stubs: { RouterLink: { template: "<a><slot /></a>" } } } });
    await flushPromises();

    expect(wrapper.text()).not.toContain("Last poll:");
    expect(wrapper.get(".overview-sync-item").text()).toContain("Last webhook:");
    expect(wrapper.get(".overview-sync-item").text()).toContain("—");
    expect(wrapper.text()).not.toContain("Last revision:");
    expect(wrapper.find(".overview-sync-separator").exists()).toBe(false);
    wrapper.unmount();
  });

  it("explains a webhook event removed by retention", async () => {
    store.syncInfo = {
      last_webhook: {
        event_id: "c0e8a2f1-1234-4567-890a-123456789abc",
        at: "2026-11-04T13:42:07Z",
        queued: false,
      },
    };
    vi.mocked(fetchEvent).mockRejectedValue(new ApiError("event not found", 404));
    const wrapper = mount(OverviewView, { global: { stubs: { RouterLink: { template: "<a><slot /></a>" } } } });
    await flushPromises();

    expect(wrapper.get(".overview-sync-indicator--warning").attributes("title")).toBe("Received but not queued");
    await wrapper.get(".overview-webhook-details").trigger("click");
    await flushPromises();
    expect(wrapper.text()).toContain("removed by event history retention");
    wrapper.unmount();
  });
});
