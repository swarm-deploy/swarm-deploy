import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchDeployments } from "../api/overview";
import DeploymentsView from "./DeploymentsView.vue";

const store = vi.hoisted(() => ({ openDeploymentDetailsModal: vi.fn(), openCommitDetailsModal: vi.fn() }));
vi.mock("../stores/overview", () => ({ useOverviewStore: () => store }));
vi.mock("../api/overview", () => ({ fetchDeployments: vi.fn() }));

describe("Deployments page", () => {
  beforeEach(() => vi.clearAllMocks());
  it("opens details separately from commit and follows the cursor", async () => {
    const initial = { id: "d1", stack: "app", status: "succeeded", commit: "abcdef012345",
      started_at: "2026-10-10T10:00:00Z", comparison_basis: "none",
      summary: { added: 1, changed: 0, removed: 0, redacted: 0 },
      resources: { services: 1, configs: 0, secrets: 0 } };
    const compared = { ...initial, id: "d2", stack: "infra", comparison_basis: "successful_baseline",
      summary: { added: 0, changed: 2, removed: 0, redacted: 0 } };
    vi.mocked(fetchDeployments).mockResolvedValueOnce({ deployments: [initial, compared], next_cursor: "cursor" } as never)
      .mockResolvedValueOnce({ deployments: [] } as never);
    const wrapper = mount(DeploymentsView);
    await flushPromises();
    expect(fetchDeployments).toHaveBeenCalledWith({ limit: 20, cursor: undefined });
    expect(wrapper.text()).toContain("Initial snapshot · 1 services, 0 configs, 0 secrets");
    expect(wrapper.text()).toContain("2 fields · 1 services, 0 configs, 0 secrets");
    expect(wrapper.find('[aria-label="View snapshot for app"]').text()).toBe("View snapshot");
    expect(wrapper.find('[aria-label="View changes for infra"]').text()).toBe("View changes");
    await wrapper.find('[aria-label="View snapshot for app"]').trigger("click");
    expect(store.openDeploymentDetailsModal).toHaveBeenCalledWith("d1");
    await wrapper.find('[aria-label="View changes for infra"]').trigger("click");
    expect(store.openDeploymentDetailsModal).toHaveBeenCalledWith("d2");
    await wrapper.find('[aria-label="Open deployment details for app"]').trigger("click");
    expect(store.openDeploymentDetailsModal).toHaveBeenLastCalledWith("d1");
    await wrapper.find('[aria-label="Open Git commit abcdef012345"]').trigger("click");
    expect(store.openCommitDetailsModal).toHaveBeenCalledWith("abcdef012345");
    expect(store.openDeploymentDetailsModal).toHaveBeenCalledTimes(3);
    await wrapper.findAll(".deployment-pages button")[1].trigger("click");
    await flushPromises();
    expect(fetchDeployments).toHaveBeenCalledWith({ limit: 20, cursor: "cursor" });
    wrapper.unmount();
  });
});
