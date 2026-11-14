import { mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createMemoryHistory, createRouter } from "vue-router";

import { useOverviewStore } from "../../stores/overview";
import RecentDeploymentsPanel from "./RecentDeploymentsPanel.vue";

const deployments = [
  {
    id: "deploy-succeeded",
    status: "succeeded",
    startedAt: "2026-10-10T10:00:00Z",
    commit: "abcdef012345",
    phase: "completed",
    summary: "3 resources changed",
  },
  {
    id: "deploy-failed",
    status: "failed",
    startedAt: "2026-10-09T10:00:00Z",
    commit: "fedcba987654",
    phase: "apply",
    summary: "Service image was not found",
  },
  {
    id: "deploy-running",
    status: "running",
    startedAt: "2026-10-08T10:00:00Z",
    phase: "verification",
    summary: "Verifying service state",
  },
  {
    id: "deploy-interrupted",
    status: "interrupted",
    startedAt: "2026-10-07T10:00:00Z",
    summary: "Image v2",
  },
  {
    id: "deploy-superseded",
    status: "superseded",
    startedAt: "2026-10-06T10:00:00Z",
    summary: "Replaced by a newer deployment",
  },
];

async function render(overrides: Partial<InstanceType<typeof RecentDeploymentsPanel>["$props"]> = {}) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: "/", component: { template: "<div />" } },
      { path: "/deployments", name: "deployments", component: { template: "<div />" } },
    ],
  });
  await router.push("/");
  const pinia = createPinia();
  setActivePinia(pinia);
  const store = useOverviewStore(pinia);
  vi.spyOn(store, "openDeploymentDetailsModal").mockResolvedValue(undefined);
  vi.spyOn(store, "openCommitDetailsModal").mockResolvedValue(undefined);
  const wrapper = mount(RecentDeploymentsPanel, {
    props: {
      id: "recent-deployments-title",
      title: "Latest Deployments",
      link: { name: "deployments", query: { stack: "app" } },
      deployments,
      ...overrides,
    },
    global: { plugins: [pinia, router] },
  });

  return { wrapper, store };
}

describe("RecentDeploymentsPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders representative deployment data and the filtered deployments link", async () => {
    const { wrapper } = await render();

    expect(wrapper.get("h3").text()).toBe("Latest Deployments");
    expect(wrapper.get("section").attributes("aria-labelledby")).toBe("recent-deployments-title");
    expect(wrapper.get("a").attributes("href")).toBe("/deployments?stack=app");
    const rows = wrapper.findAll(".stack-deployment");
    expect(rows).toHaveLength(5);
    expect(rows.map((row) => row.get(".stack-deployment-heading strong").text())).toEqual([
      "Deploy completed",
      "Deploy failed",
      "Deploy started",
      "Deploy interrupted",
      "Deploy superseded",
    ]);
    expect(rows.map((row) => row.classes().find((name) => name.startsWith("stack-deployment--")))).toEqual([
      "stack-deployment--success",
      "stack-deployment--failed",
      "stack-deployment--running",
      "stack-deployment--unknown",
      "stack-deployment--unknown",
    ]);
    expect(rows[0].get(".stack-commit-button").text()).toBe("abcdef");
    expect(rows[0].get(".stack-deployment-meta").text()).toContain("Phase completed");
    expect(rows[2].text()).toContain("Commit not available");
    expect(rows[3].find(".stack-deployment-meta strong").exists()).toBe(false);
    expect(rows.map((row) => row.get(".stack-deployment-summary").text())).toEqual(
      deployments.map((deployment) => deployment.summary),
    );
  });

  it("opens deployment details by pointer and keyboard while isolating the commit action", async () => {
    const { wrapper, store } = await render();
    const rows = wrapper.findAll(".stack-deployment");

    await rows[0].trigger("click");
    await rows[1].trigger("keydown", { key: "Enter" });
    await rows[2].trigger("keydown", { key: " " });
    expect(store.openDeploymentDetailsModal).toHaveBeenNthCalledWith(1, "deploy-succeeded");
    expect(store.openDeploymentDetailsModal).toHaveBeenNthCalledWith(2, "deploy-failed");
    expect(store.openDeploymentDetailsModal).toHaveBeenNthCalledWith(3, "deploy-running");

    await rows[0].get(".stack-commit-button").trigger("click");
    expect(store.openCommitDetailsModal).toHaveBeenCalledWith("abcdef012345");
    expect(store.openDeploymentDetailsModal).toHaveBeenCalledTimes(3);
  });

  it.each([
    [{ loading: true }, "status", "Loading deployments..."],
    [{ error: "history unavailable" }, "alert", "Failed to load deployments: history unavailable"],
    [{ deployments: [] }, undefined, "No deployments yet."],
  ])("renders the expected panel state for %j", async (overrides, role, message) => {
    const { wrapper } = await render(overrides);

    expect(wrapper.find(".stack-deployment-list").exists()).toBe(false);
    const state = wrapper.get(".stack-panel-state");
    expect(state.text()).toBe(message);
    expect(state.attributes("role")).toBe(role);
  });
});
