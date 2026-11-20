import { flushPromises, mount } from "@vue/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { getAssistantUsage } from "../../api/assistant";
import { ApiError } from "../../api/client";
import type { AssistantUsageResponse } from "../../api/types";
import AssistantUsage from "./AssistantUsage.vue";

const { setOption } = vi.hoisted(() => ({ setOption: vi.fn() }));
vi.mock("echarts/core", () => ({
  use: vi.fn(),
  init: () => ({ setOption, resize: vi.fn(), dispose: vi.fn() }),
}));
vi.mock("echarts/charts", () => ({ BarChart: {} }));
vi.mock("echarts/components", () => ({ GridComponent: {}, TooltipComponent: {} }));
vi.mock("echarts/renderers", () => ({ SVGRenderer: {} }));
vi.mock("../../api/assistant", () => ({ getAssistantUsage: vi.fn() }));

const usage: AssistantUsageResponse = {
  buckets: [{
    start: "2026-11-16",
    input_tokens: 1_234_567,
    output_tokens: 100_000,
    total_tokens: 1_334_568,
  }],
};

const number = new Intl.NumberFormat();

function render() {
  return mount(AssistantUsage);
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(new Date("2026-11-20T12:00:00Z"));
  vi.mocked(getAssistantUsage).mockReset().mockResolvedValue(usage);
  setOption.mockClear();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("Assistant Usage", () => {
  it("shows weekly stacked input and output by default and preserves the reported total", async () => {
    const wrapper = render();
    await flushPromises();

    expect(getAssistantUsage).toHaveBeenCalledWith({
      from: "2026-09-28", to: "2026-11-21", interval: "weekly",
    }, expect.any(AbortSignal));
    expect(wrapper.get('[role="tab"][aria-selected="true"]').text()).toBe("Weekly");
    expect(wrapper.findAll("dd").map((item) => item.text())).toEqual(
      [1_334_568, 1_234_567, 100_000].map((value) => number.format(value)),
    );
    const option = setOption.mock.lastCall?.[0];
    expect(option.series).toMatchObject([
      { type: "bar", stack: "total", data: [1_234_567], itemStyle: { color: "var(--info)" } },
      { type: "bar", stack: "total", data: [100_000], itemStyle: { color: "var(--success)" } },
    ]);
    expect(option.yAxis.axisLabel.formatter(1_200_000)).toBe("1.2M");
    const tooltip = option.tooltip.formatter([{ dataIndex: 0 }]);
    expect(tooltip).toContain(`Total: ${number.format(1_334_568)}`);
    expect(tooltip).toContain(`Input tokens: ${number.format(1_234_567)}`);
    expect(tooltip).toContain(`Output tokens: ${number.format(100_000)}`);
    wrapper.unmount();
  });

  it("switches to 30 daily buckets and 12 calendar months and drops earlier responses", async () => {
    let resolveWeekly!: (value: AssistantUsageResponse) => void;
    vi.mocked(getAssistantUsage).mockReturnValueOnce(new Promise((resolve) => { resolveWeekly = resolve; }));
    const wrapper = render();
    expect(wrapper.get('[role="status"]').text()).toContain("Loading token usage");
    const weeklySignal = vi.mocked(getAssistantUsage).mock.calls[0][1];

    await wrapper.get("#usage-daily").trigger("click");
    await flushPromises();
    expect(weeklySignal?.aborted).toBe(true);
    expect(getAssistantUsage).toHaveBeenLastCalledWith({
      from: "2026-10-22", to: "2026-11-21", interval: "daily",
    }, expect.any(AbortSignal));
    expect(wrapper.get('[role="img"]').attributes("aria-label")).toBe("Assistant token usage by day");

    await wrapper.get("#usage-monthly").trigger("click");
    await flushPromises();
    resolveWeekly({ buckets: [] });
    await flushPromises();
    expect(getAssistantUsage).toHaveBeenLastCalledWith({
      from: "2025-12-01", to: "2026-11-21", interval: "monthly",
    }, expect.any(AbortSignal));
    expect(wrapper.get('[role="img"]').attributes("aria-label")).toBe("Assistant token usage by month");
    wrapper.unmount();
  });

  it.each([
    ["2026-10-22", "daily"],
    ["2026-09-01", "weekly"],
    ["2026-01-01", "monthly"],
  ])("applies a custom range from %s at %s granularity", async (from, interval) => {
    const wrapper = render();
    await wrapper.get("#usage-custom").trigger("click");
    await wrapper.get('label:first-child input').setValue(from);
    await wrapper.get('label:nth-child(2) input').setValue("2026-11-19");
    await wrapper.get("form").trigger("submit");
    await flushPromises();

    expect(getAssistantUsage).toHaveBeenLastCalledWith({
      from, to: "2026-11-20", interval,
    }, expect.any(AbortSignal));
    wrapper.unmount();
  });

  it("keeps the applied range when an inverted custom range is submitted", async () => {
    const wrapper = render();
    await wrapper.get("#usage-custom").trigger("click");
    await flushPromises();
    vi.mocked(getAssistantUsage).mockClear();
    await wrapper.get('label:first-child input').setValue("2026-11-20");
    await wrapper.get('label:nth-child(2) input').setValue("2026-11-19");
    await wrapper.get("form").trigger("submit");

    expect(wrapper.get('[role="alert"]').text()).toContain("Choose a start date");
    expect(getAssistantUsage).not.toHaveBeenCalled();
    expect(wrapper.find('[role="img"]').exists()).toBe(true);
    wrapper.unmount();
  });

  it("shows an error with retry and an empty response without fabricated totals", async () => {
    vi.mocked(getAssistantUsage).mockRejectedValueOnce(new ApiError("Upstream offline", 503));
    const wrapper = render();
    await flushPromises();
    expect(wrapper.get('[role="alert"]').text()).toContain("Upstream offline");
    expect(wrapper.find("dd").exists()).toBe(false);

    vi.mocked(getAssistantUsage).mockResolvedValueOnce({ buckets: [] });
    await wrapper.get('[role="alert"] button').trigger("click");
    await flushPromises();
    expect(wrapper.text()).toContain("No token usage in the selected period.");
    expect(wrapper.find("dd").exists()).toBe(false);
    wrapper.unmount();
  });

  it("explains when the server does not provide the usage API", async () => {
    vi.mocked(getAssistantUsage).mockRejectedValueOnce(new ApiError("HTTP 404", 404));
    const wrapper = render();
    await flushPromises();
    expect(wrapper.text()).toContain("Token usage history is unavailable on this server.");
    expect(wrapper.find("dd").exists()).toBe(false);
    wrapper.unmount();
  });
});
