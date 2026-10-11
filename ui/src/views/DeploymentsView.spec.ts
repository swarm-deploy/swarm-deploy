import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchDeployments } from "../api/overview";
import DeploymentsView from "./DeploymentsView.vue";

const store = vi.hoisted(() => ({ openDeploymentDetailsModal: vi.fn(), openCommitDetailsModal: vi.fn() }));
vi.mock("../stores/overview", () => ({ useOverviewStore: () => store }));
vi.mock("../api/overview", () => ({ fetchDeployments: vi.fn() }));

describe("Deployments page", () => {
  beforeEach(() => vi.clearAllMocks());
  it("renders the compact columns in descending order and keeps commit independent from the row", async () => {
    const older = { id: "d1", stack: "app", status: "succeeded", commit: "abcdef012345",
      started_at: "2026-10-10T10:00:00Z", comparison_basis: "none",
      diff_available: true,
      summary: { added: 0, changed: 0, removed: 0, redacted: 0 },
      resources: { services: 1, configs: 0, secrets: 0, networks: 0, volumes: 0 } };
    const newer = { ...older, id: "d2", stack: "infra", status: "interrupted",
      started_at: "2026-10-11T10:00:00Z", comparison_basis: "successful_baseline" };
    vi.mocked(fetchDeployments).mockResolvedValueOnce({ deployments: [older, newer], next_cursor: "cursor" } as never)
      .mockResolvedValueOnce({ deployments: [] } as never);
    const wrapper = mount(DeploymentsView);
    await flushPromises();

    expect(fetchDeployments).toHaveBeenCalledWith({ limit: 20, cursor: undefined });
    expect(wrapper.findAll("thead th").map((header) => header.text())).toEqual(["Stack", "Status", "Started", "Commit"]);
    expect(wrapper.text()).not.toContain("Desired changes");
    expect(wrapper.text()).not.toContain("No configuration changes");
    expect(wrapper.findAll("tbody tr").map((row) => row.find(".deployment-stack").text())).toEqual(["infra", "app"]);

    const rows = wrapper.findAll("tbody tr");
    await rows[0].trigger("click");
    await rows[1].trigger("keydown", { key: "Enter" });
    await rows[0].trigger("keydown", { key: " " });
    expect(store.openDeploymentDetailsModal.mock.calls).toEqual([["d2"], ["d1"], ["d2"]]);

    const commitLink = wrapper.find('[aria-label="Open Git commit abcdef012345"]');
    expect(commitLink.element.tagName).toBe("A");
    await commitLink.trigger("click");
    await commitLink.trigger("keydown", { key: " " });
    expect(store.openCommitDetailsModal).toHaveBeenNthCalledWith(1, "abcdef012345");
    expect(store.openCommitDetailsModal).toHaveBeenNthCalledWith(2, "abcdef012345");
    expect(store.openDeploymentDetailsModal).toHaveBeenCalledTimes(3);

    await wrapper.findAll(".deployment-pages button")[1].trigger("click");
    await flushPromises();
    expect(fetchDeployments).toHaveBeenCalledWith({ limit: 20, cursor: "cursor" });
    wrapper.unmount();
  });

  it("uses semantic status badges and a neutral fallback", async () => {
    const base = { id: "d", stack: "app", commit: "", started_at: "2026-10-10T10:00:00Z",
      comparison_basis: "successful_baseline", diff_available: true,
      summary: { added: 0, changed: 0, removed: 0, redacted: 0 },
      resources: { services: 0, configs: 0, secrets: 0, networks: 0, volumes: 0 } };
    const statuses = ["succeeded", "failed", "running", "interrupted", "queued"];
    vi.mocked(fetchDeployments).mockResolvedValue({
      deployments: statuses.map((status, index) => ({ ...base, id: `${index}`, status })),
    } as never);

    const wrapper = mount(DeploymentsView);
    await flushPromises();

    const badges = wrapper.findAll(".deployment-status-badge");
    expect(badges.map((badge) => badge.text())).toEqual(["Succeeded", "Failed", "Running", "Interrupted", "Queued"]);
    expect(badges[0].classes()).toContain("success");
    expect(badges[1].classes()).toContain("failed");
    expect(badges[2].classes()).toContain("deployment-status-badge--running");
    expect(badges[3].classes()).toContain("unknown");
    expect(badges[4].classes()).toContain("unknown");
    expect(wrapper.findAll(".deployment-status-badge").every((badge) => badge.element.tagName === "SPAN")).toBe(true);
  });
});
