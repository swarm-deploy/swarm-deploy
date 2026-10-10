import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";
import OverviewView from "./OverviewView.vue";
import { fetchDeployments, fetchEvents } from "../api/overview";

const store = vi.hoisted(() => ({
  syncInfo: null, services: [], stacks: [], loading: false, loadingError: "",
  loadOverview: vi.fn().mockResolvedValue(undefined),
  openCommitDetailsModal: vi.fn(), openStackManifestModal: vi.fn(), openAlertDetailsModal: vi.fn(),
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
  it("reads attempts, displays all outcomes and never substitutes event history", async () => {
    vi.mocked(fetchDeployments).mockResolvedValue({ deployments: ["running", "succeeded", "failed", "interrupted"].map((status, i) => ({
      id: String(i), stack: "app", commit: "abc123", status: status as "running" | "succeeded" | "failed" | "interrupted",
      phase: "apply", apply_status: "running", verification_status: "pending", cleanup_status: "pending",
      actual_state_status: "unknown", comparison_basis: "successful_baseline", comparison_status: "known",
      started_at: "2026-10-10T10:00:00Z", summary: { added: 0, changed: 0, removed: 0, redacted: 0 },
      resources: { services: 0, configs: 0, secrets: 0 },
    })) });
    const wrapper = mount(OverviewView, { global: { stubs: { RouterLink: { template: "<a><slot /></a>" } } } });
    await flushPromises();
    expect(fetchDeployments).toHaveBeenCalledWith({ limit: 4 });
    expect(fetchEvents).not.toHaveBeenCalled();
    for (const status of ["running", "succeeded", "failed", "interrupted"]) {
      expect(wrapper.find(`[aria-label="Deployment ${status}"]`).exists()).toBe(true);
    }
    expect(wrapper.findAll(".overview-deployment-stack")).toHaveLength(4);
    wrapper.unmount();
  });
});
