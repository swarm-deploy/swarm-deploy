import { flushPromises, mount } from "@vue/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import RealtimeView from "./RealtimeView.vue";

const apiMocks = vi.hoisted(() => ({
  fetchSwarmServices: vi.fn(),
  fetchSwarmServiceTasks: vi.fn(),
  fetchSwarmTask: vi.fn(),
}));

vi.mock("../api/services", () => apiMocks);
vi.mock("../components/services/TaskLogsModal.vue", () => ({ default: { template: "<div />" } }));

const services = [
  { id: "one", name: "api", full_name: "infra_api", stack: "infra", image: "example/api:v1", mode: "replicated", replicas: { running: 2, desired: 2 }, updated_at: "2026-10-28T20:00:00Z" },
  { id: "two", name: "worker", full_name: "infra_worker", stack: "infra", image: "example/worker:v1", mode: "replicated", replicas: { running: 1, desired: 2 }, updated_at: "2026-10-28T20:00:00Z" },
  { id: "three", name: "reports", full_name: "ops_reports", stack: "ops", image: "example/reports:v1", mode: "replicated", replicas: { running: 0, desired: 0 }, updated_at: "2026-10-28T20:00:00Z" },
  { id: "four", name: "proxy", full_name: "proxy", stack: "", image: "example/proxy:v1", mode: "replicated", replicas: { running: 3, desired: 2 }, updated_at: "2026-10-28T20:00:00Z" },
];

const taskError = "connection refused while opening docker socket on worker-1";
const tasks = [
  { id: "task-current", slot: 1, node: "node-1", node_name: "worker-1", desired_state: "running", current_state: "running", container_id: "container-1", updated_at: "2026-10-28T20:01:00Z" },
  { id: "task-history", slot: 1, node: "node-1", node_name: "worker-1", desired_state: "shutdown", current_state: "failed", container_id: "container-old", exit_code: 1, error: taskError, updated_at: "2026-10-28T19:55:00Z" },
];

function taskDetails(id: string) {
  return {
    id,
    service_id: "one",
    node_id: "node-1",
    slot: 1,
    desired_state: "running",
    current_state: "running",
    created_at: "2026-10-28T20:00:00Z",
    updated_at: "2026-10-28T20:01:00Z",
    container_id: "container-1",
    image: "example/api:v1",
    environment: [],
  };
}

async function mountView() {
  const wrapper = mount(RealtimeView);
  await flushPromises();
  return wrapper;
}

describe("RealtimeView", () => {
  beforeEach(() => {
    apiMocks.fetchSwarmServices.mockResolvedValue({ services });
    apiMocks.fetchSwarmServiceTasks.mockResolvedValue({ tasks });
    apiMocks.fetchSwarmTask.mockImplementation(async (id: string) => taskDetails(id));
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
  });

  it("renders all live services, standalone rows, replica counts, and runtime states", async () => {
    const wrapper = await mountView();

    expect(wrapper.findAll(".realtime-service-row")).toHaveLength(4);
    expect(wrapper.text()).toContain("Standalone");
    expect(wrapper.text()).toContain("2/2");
    expect(wrapper.text()).toContain("Running");
    expect(wrapper.text()).toContain("Degraded");
    expect(wrapper.text()).toContain("Scaled down");
    expect(wrapper.text()).toContain("Unknown");
    expect(wrapper.get(".realtime-replicas-running--degraded").text()).toBe("1");
    expect(wrapper.findAll(".realtime-services-table > thead th").map((header) => header.text())).toEqual([
      "Service",
      "Stack",
      "Replicas",
      "State",
      "Updated",
    ]);
    expect(wrapper.text()).not.toContain("example/api:v1");
    expect(apiMocks.fetchSwarmServices).toHaveBeenCalledTimes(1);
    wrapper.unmount();
  });

  it("filters locally by search, multiple stacks, and state without refetching", async () => {
    const wrapper = await mountView();

    const filters = wrapper.get(".realtime-filters");
    expect(filters.findAll(".realtime-filter-icon")).toHaveLength(3);
    expect(filters.get(".realtime-stack-filter summary").text()).toContain("All stacks");
    expect(filters.get("select").element.value).toBe("all");
    expect(filters.find(".realtime-result-count").exists()).toBe(false);
    expect(filters.find(".realtime-filter-field > span").exists()).toBe(false);

    await wrapper.get<HTMLInputElement>('.realtime-search input').setValue("worker");
    expect(wrapper.findAll(".realtime-service-row")).toHaveLength(1);
    expect(wrapper.text()).toContain("infra_worker");

    await wrapper.get<HTMLInputElement>('.realtime-search input').setValue("");
    const stackOptions = wrapper.findAll<HTMLInputElement>('.realtime-stack-filter-option input');
    await stackOptions.find((option) => option.element.value === "")!.setValue(true);
    expect(wrapper.findAll(".realtime-service-row")).toHaveLength(1);
    expect(wrapper.text()).toContain("proxy");

    await stackOptions.find((option) => option.element.value === "ops")!.setValue(true);
    expect(wrapper.findAll(".realtime-service-row")).toHaveLength(2);
    expect(wrapper.text()).toContain("proxy");
    expect(wrapper.text()).toContain("reports");

    await wrapper.get(".realtime-stack-filter-clear").trigger("click");
    await wrapper.get("select").setValue("scaled-down");
    expect(wrapper.findAll(".realtime-service-row")).toHaveLength(1);
    expect(wrapper.text()).toContain("reports");
    expect(apiMocks.fetchSwarmServices).toHaveBeenCalledTimes(1);
    wrapper.unmount();
  });

  it("closes the stack filter when clicking outside it", async () => {
    const wrapper = await mountView();
    const stackFilter = wrapper.get<HTMLDetailsElement>(".realtime-stack-filter");
    stackFilter.element.open = true;

    document.body.dispatchEvent(new MouseEvent("click", { bubbles: true }));

    expect(stackFilter.element.open).toBe(false);
    wrapper.unmount();
  });

  it("fetches tasks only when expanded and keeps historical tasks with the same slot distinct", async () => {
    const wrapper = await mountView();
    expect(apiMocks.fetchSwarmServiceTasks).not.toHaveBeenCalled();

    await wrapper.findAll(".realtime-expand-button")[0].trigger("click");
    await flushPromises();

    expect(apiMocks.fetchSwarmServiceTasks).toHaveBeenCalledExactlyOnceWith("one");
    expect(wrapper.findAll(".realtime-services-table > tbody > .realtime-task-row")).toHaveLength(2);
    expect(wrapper.findAll(".realtime-task-select code").map((item) => item.text())).toEqual(["task-current", "task-history"]);
    expect(wrapper.find(".realtime-tasks-heading").exists()).toBe(false);
    expect(wrapper.find(".realtime-tasks-container").exists()).toBe(false);
    expect(wrapper.find(".realtime-tasks-table").exists()).toBe(false);
    expect(wrapper.find(".realtime-task-row .realtime-expand-button").exists()).toBe(false);
    expect(wrapper.findAll(".realtime-task-action-button")).toHaveLength(4);
    expect(wrapper.findAll(".realtime-task-action-button").every((button) => button.text() === "")).toBe(true);
    expect(wrapper.findAll(".realtime-task-action-button")[0].attributes("aria-label")).toContain("Open details for task");
    expect(wrapper.findAll(".realtime-task-action-button")[1].attributes("aria-label")).toContain("Open logs for task");
    expect(wrapper.get(".realtime-task-error-cell--failed > span").attributes("title")).toBe(taskError);
    wrapper.unmount();
  });

  it("renders relative service and task timestamps with exact timestamp tooltips", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-10-28T20:03:00Z"));
    const wrapper = await mountView();

    expect(wrapper.findAll(".realtime-service-row time")[0].text()).toBe("3m ago");
    expect(wrapper.findAll(".realtime-service-row time")[0].attributes("title")).toBeTruthy();

    await wrapper.findAll(".realtime-expand-button")[0].trigger("click");
    await flushPromises();
    expect(wrapper.findAll(".realtime-task-grid time").map((item) => item.text())).toEqual(["2m ago", "8m ago"]);
    expect(wrapper.findAll(".realtime-task-grid time").every((item) => Boolean(item.attributes("title")))).toBe(true);
    wrapper.unmount();
  });

  it("copies the full task error from the compact task row", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(window, "isSecureContext", { configurable: true, value: true });
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    const wrapper = await mountView();

    await wrapper.findAll(".realtime-expand-button")[0].trigger("click");
    await flushPromises();
    await wrapper.get(".realtime-task-error-copy").trigger("click");

    expect(writeText).toHaveBeenCalledExactlyOnceWith(taskError);
    expect(wrapper.get(".realtime-task-error-copy").attributes("aria-label")).toBe("Task error copied");
    wrapper.unmount();
  });

  it("toggles tasks when the service row or its chevron is clicked", async () => {
    const wrapper = await mountView();

    await wrapper.findAll(".realtime-service-row")[0].trigger("click");
    await flushPromises();
    expect(wrapper.findAll(".realtime-task-row")).toHaveLength(2);
    expect(apiMocks.fetchSwarmServiceTasks).toHaveBeenCalledExactlyOnceWith("one");

    await wrapper.findAll(".realtime-expand-button")[0].trigger("click");
    expect(wrapper.findAll(".realtime-task-row")).toHaveLength(0);
    wrapper.unmount();
  });

  it("loads task details and ignores a stale response when switching tasks", async () => {
    let resolveFirst!: (value: ReturnType<typeof taskDetails>) => void;
    apiMocks.fetchSwarmTask
      .mockImplementationOnce(() => new Promise((resolve) => { resolveFirst = resolve; }))
      .mockResolvedValueOnce(taskDetails("task-history"));
    const wrapper = await mountView();
    await wrapper.findAll(".realtime-expand-button")[0].trigger("click");
    await flushPromises();

    const taskButtons = wrapper.findAll(".realtime-task-select");
    await taskButtons[0].trigger("click");
    await taskButtons[1].trigger("click");
    await flushPromises();
    expect(wrapper.get(".realtime-task-panel").text()).toContain("task-history");

    resolveFirst(taskDetails("task-current"));
    await flushPromises();
    expect(wrapper.get(".realtime-task-panel").text()).toContain("task-history");
    expect(wrapper.get(".realtime-task-panel").text()).not.toContain("task-current");
    wrapper.unmount();
  });

  it("refreshes every five seconds, preserves expanded rows, and retains data on temporary errors", async () => {
    vi.useFakeTimers();
    const wrapper = mount(RealtimeView);
    await flushPromises();
    await wrapper.findAll(".realtime-expand-button")[0].trigger("click");
    await flushPromises();

    apiMocks.fetchSwarmServices.mockRejectedValueOnce(new Error("temporary outage"));
    await vi.advanceTimersByTimeAsync(5_000);
    await flushPromises();

    expect(apiMocks.fetchSwarmServices).toHaveBeenCalledTimes(2);
    expect(wrapper.findAll(".realtime-service-row")).toHaveLength(4);
    expect(wrapper.text()).toContain("Live data may be stale: temporary outage");
    expect(wrapper.findAll(".realtime-task-row")).toHaveLength(2);
    wrapper.unmount();

    await vi.advanceTimersByTimeAsync(5_000);
    expect(apiMocks.fetchSwarmServices).toHaveBeenCalledTimes(2);
  });

  it("pauses polling while hidden and refreshes when the tab becomes visible", async () => {
    vi.useFakeTimers();
    Object.defineProperty(document, "hidden", { configurable: true, value: false });
    const wrapper = mount(RealtimeView);
    await flushPromises();
    expect(apiMocks.fetchSwarmServices).toHaveBeenCalledTimes(1);

    Object.defineProperty(document, "hidden", { configurable: true, value: true });
    document.dispatchEvent(new Event("visibilitychange"));
    await vi.advanceTimersByTimeAsync(10_000);
    expect(apiMocks.fetchSwarmServices).toHaveBeenCalledTimes(1);

    Object.defineProperty(document, "hidden", { configurable: true, value: false });
    document.dispatchEvent(new Event("visibilitychange"));
    await flushPromises();
    expect(apiMocks.fetchSwarmServices.mock.calls.length).toBeGreaterThan(1);
    wrapper.unmount();
  });
});
