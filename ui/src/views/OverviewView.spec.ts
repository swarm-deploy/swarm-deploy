import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";
import OverviewView from "./OverviewView.vue";
import { fetchDeployments, fetchEvents } from "../api/overview";

const store = vi.hoisted(() => ({
  syncInfo: null, services: [], stacks: [], loading: false, loadingError: "",
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

  it("renders compact deployment actions with distinct status semantics", async () => {
    vi.mocked(fetchDeployments).mockResolvedValue({ deployments: ["running", "succeeded", "failed", "interrupted"].map((status, i) => ({
      id: String(i), stack: i === 0 ? "a-very-long-stack-name-that-needs-truncation" : "app", commit: "abc123", status: status as "running" | "succeeded" | "failed" | "interrupted",
      phase: "apply", apply_status: "running", verification_status: "pending", cleanup_status: "pending",
      actual_state_status: "unknown", comparison_basis: "successful_baseline", comparison_status: "known",
      started_at: "2026-10-10T10:00:00Z", summary: { added: 1, changed: 2, removed: 3, redacted: 0 },
      resources: { services: 0, configs: 0, secrets: 0 },
    })) });
    const wrapper = mount(OverviewView, { global: { stubs: { RouterLink: { template: "<a><slot /></a>" } } } });
    await flushPromises();

    expect(fetchDeployments).toHaveBeenCalledWith({ limit: 4 });
    expect(fetchEvents).not.toHaveBeenCalled();
    for (const status of ["running", "succeeded", "failed", "interrupted"]) {
      expect(wrapper.find(`[aria-label="Deployment ${status}"]`).exists()).toBe(true);
      expect(wrapper.text()).toContain(status);
    }
    expect(wrapper.find('[aria-label="Deployment running"]').classes()).toContain("overview-deployment-result--running");
    expect(wrapper.find('[aria-label="Deployment succeeded"]').classes()).toContain("overview-deployment-result--success");
    expect(wrapper.find('[aria-label="Deployment failed"]').classes()).toContain("overview-deployment-result--failed");
    const interrupted = wrapper.find('[aria-label="Deployment interrupted"]');
    expect(interrupted.classes()).toContain("overview-deployment-result--interrupted");
    expect(interrupted.classes()).not.toContain("overview-deployment-result--failed");
    expect(wrapper.findAll(".overview-deployment-stack")).toHaveLength(4);
    expect(wrapper.find(".overview-summary-secondary").text()).toBe("1 added · 2 changed · 3 removed");
    expect(wrapper.find(".overview-deployment-open .overview-summary-sha-badge").exists()).toBe(false);

    await wrapper.findAll(".overview-deployment-open")[0].trigger("click");
    expect(store.openDeploymentDetailsModal).toHaveBeenCalledWith("0");
    expect(store.openCommitDetailsModal).not.toHaveBeenCalled();

    await wrapper.findAll(".overview-summary-sha-badge")[0].trigger("click");
    expect(store.openCommitDetailsModal).toHaveBeenCalledWith("abc123");
    expect(store.openDeploymentDetailsModal).toHaveBeenCalledTimes(1);
    wrapper.unmount();
  });
});
