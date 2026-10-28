import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { fetchStackDeploymentActivity } from "../../api/overview";
import type { DeploymentActivityInterval, DeploymentActivityResponse } from "../../api/types";
import DeploymentActivityChart from "./DeploymentActivityChart.vue";

const chartMocks = vi.hoisted(() => ({
  setOption: vi.fn(),
  resize: vi.fn(),
  clear: vi.fn(),
  dispose: vi.fn(),
  init: vi.fn(),
  use: vi.fn(),
}));

vi.mock("echarts/charts", () => ({ BarChart: {} }));
vi.mock("echarts/components", () => ({ GridComponent: {}, TooltipComponent: {} }));
vi.mock("echarts/renderers", () => ({ SVGRenderer: {} }));
vi.mock("echarts/core", () => ({
  use: chartMocks.use,
  init: chartMocks.init,
}));
vi.mock("../../api/overview", () => ({ fetchStackDeploymentActivity: vi.fn() }));

function activity(interval: DeploymentActivityInterval, empty = false): DeploymentActivityResponse {
  const count = interval === "daily" ? 30 : 8;
  const start = interval === "daily" ? new Date("2026-09-29T00:00:00Z") : new Date("2026-09-07T00:00:00Z");
  return {
    buckets: Array.from({ length: count }, (_, index) => ({
      start: new Date(start.getTime() + index * (interval === "daily" ? 86400000 : 7 * 86400000)).toISOString(),
      succeeded: !empty && index === count - 1 ? 4 : 0,
      failed: !empty && index === count - 1 ? 2 : 0,
      running: !empty && index === count - 1 ? 1 : 0,
      interrupted: !empty && index === count - 1 ? 1 : 0,
      superseded: !empty && index === count - 1 ? 1 : 0,
    })),
  };
}

async function render() {
  const wrapper = mount(DeploymentActivityChart, { props: { stack: "app" } });
  await flushPromises();
  return wrapper;
}

beforeEach(() => {
  vi.clearAllMocks();
  chartMocks.init.mockReturnValue({
    setOption: chartMocks.setOption,
    resize: chartMocks.resize,
    clear: chartMocks.clear,
    dispose: chartMocks.dispose,
  });
  vi.mocked(fetchStackDeploymentActivity).mockImplementation(async (_stack, selected) => activity(selected ?? "weekly"));
  vi.stubGlobal("ResizeObserver", class {
    observe() {}
    disconnect() {}
  });
});

describe("DeploymentActivityChart", () => {
  it("renders weekly activity, status colors and derived summary", async () => {
    const wrapper = await render();

    expect(fetchStackDeploymentActivity).toHaveBeenCalledWith("app", "weekly");
    expect(wrapper.get(".overview-summary-panel-header .overview-panel-title").text()).toBe("Deployment Activity");
    expect(wrapper.find(".overview-summary-panel-icon .deployment-icon").exists()).toBe(true);
    expect(wrapper.get('[aria-label="Deployment activity interval"] [aria-pressed="true"]').text()).toBe("Weekly");
    expect(wrapper.findAll(".deployment-activity-legend li").map((item) => item.text())).toEqual([
      "Succeeded", "Failed", "Running", "Interrupted", "Superseded",
    ]);
    expect(wrapper.findAll(".deployment-activity-summary dd").map((item) => item.text())).toEqual([
      "9", "1.1", "4", "2", "1", "1", "1",
    ]);
    const option = chartMocks.setOption.mock.calls.at(-1)?.[0] as {
      series: Array<{ name: string; data: number[]; itemStyle: { color: string } }>;
    };
    expect(option.series.map((series) => [series.name, series.itemStyle.color])).toEqual([
      ["Succeeded", "#3fb950"],
      ["Failed", "#f85149"],
      ["Running", "#d29922"],
      ["Interrupted", "#8b949e"],
      ["Superseded", "#58a6ff"],
    ]);
    expect(option.series.map((series) => series.data.at(-1))).toEqual([4, 2, 1, 1, 1]);
    wrapper.unmount();
  });

  it("switches intervals and preserves the selection without refetching when collapsed", async () => {
    const wrapper = await render();
    await wrapper.get('.deployment-activity-interval button[aria-pressed="false"]').trigger("click");
    await flushPromises();
    expect(fetchStackDeploymentActivity).toHaveBeenLastCalledWith("app", "daily");

    await wrapper.get(".deployment-activity-toggle").trigger("click");
    expect(wrapper.get(".deployment-activity-toggle").attributes("aria-expanded")).toBe("false");
    expect(wrapper.get(".deployment-activity-compact").text()).toBe("9 deployments in 30 days");
    await wrapper.get(".deployment-activity-toggle").trigger("click");
    expect(wrapper.get('[aria-label="Deployment activity interval"] [aria-pressed="true"]').text()).toBe("Daily");
    expect(fetchStackDeploymentActivity).toHaveBeenCalledTimes(2);
    wrapper.unmount();
  });

  it("supports collapsing and expanding while the request is loading", async () => {
    let resolve!: (response: DeploymentActivityResponse) => void;
    vi.mocked(fetchStackDeploymentActivity).mockReturnValueOnce(new Promise((done) => { resolve = done; }));
    const wrapper = mount(DeploymentActivityChart, { props: { stack: "app" } });
    expect(wrapper.get('[role="status"]').text()).toContain("Loading deployment activity");
    await wrapper.get(".deployment-activity-toggle").trigger("click");
    expect(wrapper.get(".deployment-activity-compact").text()).toBe("Loading activity…");
    await wrapper.get(".deployment-activity-toggle").trigger("click");
    resolve(activity("weekly"));
    await flushPromises();
    expect(wrapper.find('[role="status"]').exists()).toBe(false);
    expect(fetchStackDeploymentActivity).toHaveBeenCalledTimes(1);
    wrapper.unmount();
  });

  it("renders retriable errors and an honest empty state", async () => {
    vi.mocked(fetchStackDeploymentActivity).mockRejectedValueOnce(new Error("offline"));
    const wrapper = await render();
    expect(wrapper.get('[role="alert"]').text()).toContain("offline");
    vi.mocked(fetchStackDeploymentActivity).mockResolvedValueOnce(activity("weekly", true));
    await wrapper.get('[role="alert"] button').trigger("click");
    await flushPromises();
    expect(wrapper.text()).toContain("No deployments in the selected period.");
    expect(wrapper.find(".deployment-activity-summary").exists()).toBe(false);
    wrapper.unmount();
  });
});
