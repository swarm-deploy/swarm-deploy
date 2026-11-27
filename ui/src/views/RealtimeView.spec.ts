import { flushPromises, mount } from "@vue/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import RealtimeView from "./RealtimeView.vue";

const route = vi.hoisted(() => ({ query: {} as Record<string, string> }));
vi.mock("vue-router", () => ({ useRoute: () => route }));

const apiMocks = vi.hoisted(() => ({
  fetchSwarmServices: vi.fn(),
  fetchSwarmServiceTasks: vi.fn(),
  fetchSwarmTask: vi.fn(),
  scaleSwarmService: vi.fn(),
  restartSwarmService: vi.fn(),
}));
const pauseMocks = vi.hoisted(() => ({
  getServiceSyncPause: vi.fn(),
  setServiceSyncPause: vi.fn(),
}));

vi.mock("../api/services", () => apiMocks);
vi.mock("../api/syncPauses", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/syncPauses")>()),
  ...pauseMocks,
}));
vi.mock("../components/services/TaskLogsModal.vue", () => ({ default: { template: "<div />" } }));

const services = [
  { id: "one", name: "api", full_name: "infra_api", stack: "infra", image: "example/api:v1", mode: "replicated", configured_replicas: 4, replicas: { running: 2, desired: 2 }, updated_at: "2026-10-28T20:00:00Z" },
  { id: "two", name: "worker", full_name: "infra_worker", stack: "infra", image: "example/worker:v1", mode: "replicated", configured_replicas: 2, replicas: { running: 1, desired: 2 }, updated_at: "2026-10-28T20:00:00Z" },
  { id: "three", name: "reports", full_name: "ops_reports", stack: "ops", image: "example/reports:v1", mode: "replicated", configured_replicas: 2, replicas: { running: 0, desired: 0 }, updated_at: "2026-10-28T20:00:00Z" },
  { id: "four", name: "proxy", full_name: "proxy", stack: "", image: "example/proxy:v1", mode: "replicated", configured_replicas: 2, replicas: { running: 3, desired: 2 }, updated_at: "2026-10-28T20:00:00Z" },
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
  const wrapper = mount(RealtimeView, { global: { stubs: { Teleport: true, RouterLink: true } } });
  await flushPromises();
  return wrapper;
}

describe("RealtimeView", () => {
  it("opens a service-scoped link on the exact service and expands its tasks", async () => {
    route.query = { stack: "infra", service: "api" };
    const wrapper = await mountView();
    expect(wrapper.findAll(".realtime-service-row")).toHaveLength(1);
    expect(wrapper.get(".realtime-service-row").text()).toContain("infra_api");
    expect(wrapper.get(".realtime-stack-filter summary").text()).toContain("infra");
    expect(wrapper.get(".realtime-service-filter summary").text()).toContain("api");
    expect(wrapper.find(".meta").exists()).toBe(false);
    expect(wrapper.get('[aria-label="Collapse api"]').attributes("aria-expanded")).toBe("true");
    expect(apiMocks.fetchSwarmServiceTasks).toHaveBeenCalledExactlyOnceWith("one");
    wrapper.unmount();
  });

  beforeEach(() => {
    window.localStorage.clear();
    route.query = {};
    apiMocks.fetchSwarmServices.mockResolvedValue({ services });
    apiMocks.fetchSwarmServiceTasks.mockResolvedValue({ tasks });
    apiMocks.scaleSwarmService.mockResolvedValue(undefined);
    apiMocks.fetchSwarmTask.mockImplementation(async (id: string) => taskDetails(id));
    pauseMocks.getServiceSyncPause.mockResolvedValue({ effective_paused: false });
    pauseMocks.setServiceSyncPause.mockResolvedValue({
      effective_paused: true,
      effective_source: "service",
      service_pause: { scope: "service", stack: "infra", service: "api", expires_at: null },
    });
  });

  afterEach(() => {
    vi.useRealTimers();
    window.localStorage.clear();
    vi.clearAllMocks();
  });

  it("renders a compact refresh split control with the default interval", async () => {
    const wrapper = await mountView();

    expect(wrapper.get(".realtime-refresh-button").text()).toBe("Refresh");
    expect(wrapper.get(".realtime-refresh-dropdown > summary").text()).toBe("5s");
    expect(wrapper.find(".realtime-auto-refresh").exists()).toBe(false);
    expect(wrapper.find(".realtime-refresh-status").exists()).toBe(false);
    expect(wrapper.get(".realtime-live-indicator").text()).toBe("Live");
    expect(wrapper.findAll(".realtime-refresh-option").map((option) => option.text())).toEqual([
      "Off", "5s", "10s", "30s", "1m", "5m", "15m", "30m", "1h",
    ]);
    wrapper.unmount();
  });

  it("disables polling when Off is selected while keeping manual refresh available", async () => {
    vi.useFakeTimers();
    const wrapper = await mountView();

    await wrapper.findAll(".realtime-refresh-option")[0].trigger("click");
    expect(wrapper.get(".realtime-refresh-dropdown > summary").text()).toBe("Off");
    expect(wrapper.get(".realtime-live-indicator").text()).toBe("Paused");
    expect(window.localStorage.getItem("realtime.refreshInterval")).toBe("0");

    await vi.advanceTimersByTimeAsync(60_000);
    expect(apiMocks.fetchSwarmServices).toHaveBeenCalledTimes(1);

    await wrapper.get(".realtime-refresh-button").trigger("click");
    await flushPromises();
    expect(apiMocks.fetchSwarmServices).toHaveBeenCalledTimes(2);
    wrapper.unmount();
  });

  it("reschedules polling immediately when the interval changes", async () => {
    vi.useFakeTimers();
    const wrapper = await mountView();

    await wrapper.findAll(".realtime-refresh-option")[2].trigger("click");
    await vi.advanceTimersByTimeAsync(5_000);
    expect(apiMocks.fetchSwarmServices).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(5_000);
    expect(apiMocks.fetchSwarmServices).toHaveBeenCalledTimes(2);

    await wrapper.findAll(".realtime-refresh-option")[3].trigger("click");
    await vi.advanceTimersByTimeAsync(10_000);
    expect(apiMocks.fetchSwarmServices).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(20_000);
    expect(apiMocks.fetchSwarmServices).toHaveBeenCalledTimes(3);
    wrapper.unmount();
  });

  it("does not start an overlapping refresh while a request is pending", async () => {
    vi.useFakeTimers();
    const wrapper = await mountView();
    let resolveRefresh!: (value: { services: typeof services }) => void;
    apiMocks.fetchSwarmServices.mockImplementationOnce(() => new Promise((resolve) => {
      resolveRefresh = resolve;
    }));

    await vi.advanceTimersByTimeAsync(5_000);
    expect(apiMocks.fetchSwarmServices).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(5_000);
    expect(apiMocks.fetchSwarmServices).toHaveBeenCalledTimes(2);

    resolveRefresh({ services });
    await flushPromises();
    wrapper.unmount();
  });

  it("restores a valid persisted interval and falls back to five seconds for invalid values", async () => {
    window.localStorage.setItem("realtime.refreshInterval", "60000");
    const persisted = await mountView();
    expect(persisted.get(".realtime-refresh-dropdown > summary").text()).toBe("1m");
    persisted.unmount();

    window.localStorage.setItem("realtime.refreshInterval", "12345");
    const fallback = await mountView();
    expect(fallback.get(".realtime-refresh-dropdown > summary").text()).toBe("5s");
    fallback.unmount();
  });

  it("shows replica counts as the status for every live service", async () => {
    const wrapper = await mountView();

    expect(wrapper.findAll(".realtime-service-row")).toHaveLength(4);
    expect(wrapper.text()).toContain("Standalone");
    const badges = wrapper.findAll(".realtime-state-cell .app-status-badge");
    expect(badges.map((badge) => badge.text())).toEqual([
      "2 / 2 replicas", "1 / 2 replicas", "0 / 0 replicas", "3 / 2 replicas",
    ]);
    expect(badges.every((badge) => badge.classes().includes("app-status-badge--filled"))).toBe(true);
    expect(badges[0].classes()).toContain("app-status-badge--success");
    expect(badges[1].classes()).toContain("app-status-badge--warning");
    expect(badges[2].classes()).toContain("app-status-badge--success");
    expect(badges[3].classes()).toContain("app-status-badge--warning");
    expect(wrapper.findAll(".realtime-services-table > thead th").map((header) => header.text())).toEqual([
      "Service",
      "Stack",
      "State",
      "Updated",
      "Actions",
    ]);
    expect(wrapper.text()).not.toContain("example/api:v1");
    expect(apiMocks.fetchSwarmServices).toHaveBeenCalledTimes(1);
    wrapper.unmount();
  });

  it("filters locally by search, multiple stacks, and state without refetching", async () => {
    const wrapper = await mountView();

    const filters = wrapper.get(".realtime-filters");
    const stateFilter = filters.get(".realtime-state-filter");
    expect(filters.findAll(".realtime-filter-icon")).toHaveLength(4);
    expect(filters.get(".realtime-stack-filter summary").text()).toContain("All stacks");
    expect(filters.get(".realtime-service-filter summary").text()).toContain("All services");
    expect(stateFilter.get("summary").text()).toContain("All states");
    expect(filters.find(".realtime-result-count").exists()).toBe(false);
    expect(stateFilter.get(".app-select-icon").attributes("aria-hidden")).toBe("true");

    await wrapper.get<HTMLInputElement>('.realtime-search input').setValue("worker");
    expect(wrapper.findAll(".realtime-service-row")).toHaveLength(1);
    expect(wrapper.text()).toContain("infra_worker");

    await wrapper.get<HTMLInputElement>('.realtime-search input').setValue("");
    const serviceOptions = wrapper.findAll<HTMLInputElement>('.realtime-service-filter .app-select-option input');
    await serviceOptions.find((option) => option.element.value === "worker")!.setValue(true);
    expect(wrapper.findAll(".realtime-service-row")).toHaveLength(1);
    expect(wrapper.text()).toContain("infra_worker");

    await wrapper.get(".realtime-service-filter .app-select-header button").trigger("click");
    const stackOptions = wrapper.findAll<HTMLInputElement>('.realtime-stack-filter .app-select-option input');
    await stackOptions.find((option) => option.element.value === "")!.setValue(true);
    expect(wrapper.findAll(".realtime-service-row")).toHaveLength(1);
    expect(wrapper.text()).toContain("proxy");

    await stackOptions.find((option) => option.element.value === "ops")!.setValue(true);
    expect(wrapper.findAll(".realtime-service-row")).toHaveLength(2);
    expect(wrapper.text()).toContain("proxy");
    expect(wrapper.text()).toContain("reports");

    await wrapper.get(".realtime-stack-filter .app-select-header button").trigger("click");
    await stateFilter.findAll("button.app-select-option").find((option) => option.text() === "Scaled down")!.trigger("click");
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
    const wrapper = mount(RealtimeView, { global: { stubs: { Teleport: true, RouterLink: true } } });
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
    const wrapper = mount(RealtimeView, { global: { stubs: { Teleport: true, RouterLink: true } } });
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


describe("Realtime service scaling", () => {
  beforeEach(() => {
    window.localStorage.clear();
    apiMocks.fetchSwarmServices.mockResolvedValue({ services });
    apiMocks.fetchSwarmServiceTasks.mockResolvedValue({ tasks });
    apiMocks.fetchSwarmTask.mockImplementation(async (id: string) => taskDetails(id));
    apiMocks.scaleSwarmService.mockResolvedValue(undefined);
    pauseMocks.getServiceSyncPause.mockResolvedValue({ effective_paused: false });
    pauseMocks.setServiceSyncPause.mockResolvedValue({
      effective_paused: true,
      effective_source: "service",
      service_pause: { scope: "service", stack: "infra", service: "api", expires_at: null },
    });
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
  });

  it("shows Scale only for replicated services, including standalone services", async () => {
    apiMocks.fetchSwarmServices.mockResolvedValueOnce({ services: [
      ...services,
      ...["global", "replicated-job", "global-job"].map((mode) => ({ ...services[0], id: mode, full_name: mode, mode })),
    ] });
    const wrapper = await mountView();
    expect(wrapper.findAll(".realtime-actions-button")).toHaveLength(4);
    expect(wrapper.findAll(".realtime-state-cell button")).toHaveLength(0);
    await wrapper.get('[aria-label="Actions for proxy"]').trigger("click");
    expect(wrapper.get('[aria-label="Scale proxy"]').text()).toContain("Scale replicas");
    for (const row of wrapper.findAll(".realtime-service-row")) {
      if (["global", "replicated-job", "global-job"].some((mode) => row.text().includes(mode))) {
        expect(row.find(".realtime-scale-button").exists()).toBe(false);
      }
    }
    wrapper.unmount();
  });

  it("opens with the configured count instead of task counts without expanding the row", async () => {
    const wrapper = await mountView();
    await wrapper.get('[aria-label="Actions for api"]').trigger("click");
    await wrapper.get('[aria-label="Scale api"]').trigger("click");
    expect(apiMocks.fetchSwarmServiceTasks).not.toHaveBeenCalled();
    expect(wrapper.find(".realtime-task-row").exists()).toBe(false);
    const modal = wrapper.get('[role="dialog"]');
    expect(modal.text()).toContain("api");
    expect(modal.text()).toContain("infra");
    expect(modal.findAll(".realtime-scale-section-label").map((item) => item.text())).toEqual(["Service details", "Scale"]);
    expect(modal.find(".realtime-scale-divider").exists()).toBe(true);
    expect(modal.findAll(".realtime-scale-summary dt").map((item) => item.text())).toEqual(["Service", "Stack", "Current scale"]);
    expect(modal.findAll(".realtime-scale-summary dd").map((item) => item.text())).toEqual(["api", "infra", "4"]);
    expect(modal.text()).not.toContain("Enter a non-negative whole number.");
    expect(wrapper.get<HTMLInputElement>("#realtime-scale-replicas").element.value).toBe("4");
    expect(modal.get('button[type="submit"]').attributes("disabled")).toBeDefined();
    await modal.get('[aria-label="Close scaling modal"]').trigger("click");
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false);
    wrapper.unmount();
  });

  it("shows the GitOps scaling warning only in the Pause Sync tooltip", async () => {
    const wrapper = await mountView();
    await wrapper.get('[aria-label="Actions for api"]').trigger("click");
    await wrapper.get('[aria-label="Scale api"]').trigger("click");
    await flushPromises();
    const modal = wrapper.get('[role="dialog"]');
    const row = modal.get(".realtime-scale-pause-row");
    const help = row.get('[aria-label="About Pause Sync"]');
    const tooltip = '[role="tooltip"]';
    const warning = "Manual scaling may be overwritten by the next GitOps synchronization.";

    expect(row.get("label").text()).toBe("Pause sync");
    expect(modal.text()).not.toContain(warning);
    const trigger = row.get(".realtime-scale-pause-help");
    await trigger.trigger("pointerenter", { pointerType: "mouse" });
    expect(modal.get(tooltip).text()).toBe(warning);
    expect(help.attributes("aria-describedby")).toBe(modal.get(tooltip).attributes("id"));
    await trigger.trigger("pointerleave", { pointerType: "mouse" });
    expect(modal.find(tooltip).exists()).toBe(false);

    await help.trigger("click");
    expect(modal.get(tooltip).text()).toBe(warning);
    expect(row.get<HTMLInputElement>('input[type="checkbox"]').element.checked).toBe(false);
    await modal.get("#realtime-scale-replicas").trigger("pointerdown");
    expect(modal.find(tooltip).exists()).toBe(false);

    await help.trigger("click");
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
    await wrapper.vm.$nextTick();
    expect(modal.find(tooltip).exists()).toBe(false);
    expect(wrapper.find('[role="dialog"]').exists()).toBe(true);
    wrapper.unmount();
  });

  it("displays Standalone for services outside stacks", async () => {
    const wrapper = await mountView();
    await wrapper.get('[aria-label="Actions for proxy"]').trigger("click");
    await wrapper.get('[aria-label="Scale proxy"]').trigger("click");
    expect(wrapper.get('[role="dialog"]').text()).toContain("Standalone");
    wrapper.unmount();
  });

  it.each(["", "-1", "1.5", "invalid", "9007199254740992"])("rejects invalid replicas: %s", async (value) => {
    const wrapper = await mountView();
    await wrapper.get('[aria-label="Actions for api"]').trigger("click");
    await wrapper.get('[aria-label="Scale api"]').trigger("click");
    await wrapper.get("#realtime-scale-replicas").setValue(value);
    expect(wrapper.get('button[type="submit"]').attributes("disabled")).toBeDefined();
    await wrapper.get("form").trigger("submit");
    expect(apiMocks.scaleSwarmService).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it("supports increment/decrement and scales to zero while preserving filters, tasks and details", async () => {
    const wrapper = await mountView();
    await wrapper.get('input[type="search"]').setValue("infra_api");
    await wrapper.get(".realtime-service-row").trigger("click");
    await flushPromises();
    await wrapper.findAll(".realtime-task-select")[0].trigger("click");
    await flushPromises();
    await wrapper.get('[aria-label="Actions for api"]').trigger("click");
    await wrapper.get('[aria-label="Scale api"]').trigger("click");
    await wrapper.get('[aria-label="Increase replicas"]').trigger("click");
    expect(wrapper.get<HTMLInputElement>("#realtime-scale-replicas").element.value).toBe("5");
    await wrapper.get('[aria-label="Decrease replicas"]').trigger("click");
    expect(wrapper.get('button[type="submit"]').attributes("disabled")).toBeDefined();
    await wrapper.get("#realtime-scale-replicas").setValue("0");
    expect(wrapper.get('[aria-label="Decrease replicas"]').attributes("disabled")).toBeDefined();
    apiMocks.fetchSwarmServices.mockResolvedValueOnce({ services: services.map((service) => (
      service.id === "one" ? { ...service, configured_replicas: 0, replicas: { running: 2, desired: 0 } } : service
    )) });
    await wrapper.get("form").trigger("submit");
    await flushPromises();
    expect(apiMocks.scaleSwarmService).toHaveBeenCalledExactlyOnceWith("one", 0);
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false);
    expect(apiMocks.fetchSwarmServices).toHaveBeenCalledTimes(2);
    expect(apiMocks.fetchSwarmServiceTasks).toHaveBeenCalledTimes(2);
    expect(wrapper.get<HTMLInputElement>('input[type="search"]').element.value).toBe("infra_api");
    expect(wrapper.findAll(".realtime-service-row")).toHaveLength(1);
    expect(wrapper.findAll(".realtime-task-row")).toHaveLength(2);
    expect(wrapper.get(".realtime-state-cell").text()).toBe("2 / 0 replicas");
    expect(wrapper.get(".realtime-task-panel").text()).toContain("task-current");
    wrapper.unmount();
  });

  it("keeps errors in the modal and prevents repeated submissions while loading", async () => {
    let reject!: (reason: Error) => void;
    apiMocks.scaleSwarmService.mockImplementationOnce(() => new Promise((_resolve, fail) => { reject = fail; }));
    const wrapper = await mountView();
    await wrapper.get('[aria-label="Actions for api"]').trigger("click");
    await wrapper.get('[aria-label="Scale api"]').trigger("click");
    await wrapper.get("#realtime-scale-replicas").setValue("5");
    await wrapper.get("form").trigger("submit");
    expect(wrapper.get('button[type="submit"]').text()).toBe("Scaling...");
    expect(wrapper.get('button[type="submit"]').attributes("disabled")).toBeDefined();
    expect(wrapper.get("#realtime-scale-replicas").attributes("disabled")).toBeDefined();
    await wrapper.get("form").trigger("submit");
    expect(apiMocks.scaleSwarmService).toHaveBeenCalledTimes(1);
    reject(new Error("service changed; refresh and retry"));
    await flushPromises();
    expect(wrapper.get('[role="dialog"] [role="alert"]').text()).toContain("service changed");
    expect(wrapper.get('button[type="submit"]').attributes("disabled")).toBeUndefined();
    expect(apiMocks.fetchSwarmServices).toHaveBeenCalledTimes(1);
    wrapper.unmount();
  });

  it("persists Pause Sync before scaling", async () => {
    const order: string[] = [];
    pauseMocks.setServiceSyncPause.mockImplementation(async () => {
      order.push("pause");
      return {
        effective_paused: true,
        effective_source: "service",
        service_pause: { scope: "service", stack: "infra", service: "api", expires_at: null },
      };
    });
    apiMocks.scaleSwarmService.mockImplementation(async () => { order.push("scale"); });
    const wrapper = await mountView();
    await wrapper.get('[aria-label="Actions for api"]').trigger("click");
    await wrapper.get('[aria-label="Scale api"]').trigger("click");
    await flushPromises();
    await wrapper.get('.realtime-scale-pause-toggle input[type="checkbox"]').setValue(true);
    await wrapper.get("#realtime-scale-replicas").setValue("5");
    await wrapper.get("form").trigger("submit");
    await flushPromises();

    expect(order).toEqual(["pause", "scale"]);
    expect(apiMocks.scaleSwarmService).toHaveBeenCalledWith("one", 5);
    wrapper.unmount();
  });

  it("ignores a stale Pause Sync lookup after switching services", async () => {
    let resolveAPI!: (value: { effective_paused: boolean; effective_source: string }) => void;
    let resolveWorker!: (value: { effective_paused: boolean; effective_source: string }) => void;
    pauseMocks.getServiceSyncPause.mockImplementation((_stack: string, service: string) => new Promise((resolve) => {
      if (service === "api") resolveAPI = resolve;
      else resolveWorker = resolve;
    }));
    const wrapper = await mountView();

    await wrapper.get('[aria-label="Actions for api"]').trigger("click");
    await wrapper.get('[aria-label="Scale api"]').trigger("click");
    await wrapper.get('[aria-label="Close scaling modal"]').trigger("click");
    await wrapper.get('[aria-label="Actions for worker"]').trigger("click");
    await wrapper.get('[aria-label="Scale worker"]').trigger("click");
    resolveWorker({ effective_paused: true, effective_source: "stack" });
    await flushPromises();
    expect(wrapper.get('[role="dialog"]').text()).toContain("worker");
    expect(wrapper.get('[role="dialog"]').text()).toContain("Sync paused by: Stack");

    resolveAPI({ effective_paused: true, effective_source: "service" });
    await flushPromises();
    expect(wrapper.get('[role="dialog"]').text()).toContain("Sync paused by: Stack");
    expect(wrapper.get('[role="dialog"]').text()).not.toContain("Sync paused by: Service");
    wrapper.unmount();
  });

  it("keeps a successful pause when scaling fails and retries only scaling", async () => {
    apiMocks.scaleSwarmService.mockRejectedValueOnce(new Error("manager unavailable")).mockResolvedValueOnce(undefined);
    const wrapper = await mountView();
    await wrapper.get('[aria-label="Actions for api"]').trigger("click");
    await wrapper.get('[aria-label="Scale api"]').trigger("click");
    await flushPromises();
    await wrapper.get('.realtime-scale-pause-toggle input[type="checkbox"]').setValue(true);
    await wrapper.get("#realtime-scale-replicas").setValue("5");
    await wrapper.get("form").trigger("submit");
    await flushPromises();

    expect(wrapper.get('[role="dialog"] [role="alert"]').text()).toContain("Sync is paused, but scaling failed");
    expect(wrapper.get('[role="dialog"]').text()).toContain("Retry will only retry scaling");
    expect(pauseMocks.setServiceSyncPause).toHaveBeenCalledTimes(1);

    await wrapper.get("form").trigger("submit");
    await flushPromises();
    expect(pauseMocks.setServiceSyncPause).toHaveBeenCalledTimes(1);
    expect(apiMocks.scaleSwarmService).toHaveBeenCalledTimes(2);
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false);
    wrapper.unmount();
  });

  it("ignores old service and task polls after a successful mutation and continues polling", async () => {
    vi.useFakeTimers();
    const wrapper = await mountView();
    await wrapper.findAll(".realtime-service-row")[0].trigger("click");
    await flushPromises();
    let resolveOldServices!: (value: { services: typeof services }) => void;
    let resolveOldTasks!: (value: { tasks: typeof tasks }) => void;
    apiMocks.fetchSwarmServiceTasks.mockImplementationOnce(() => new Promise((resolve) => { resolveOldTasks = resolve; }));
    await vi.advanceTimersByTimeAsync(5_000);
    await flushPromises();
    // An old task poll remains in flight when scaling succeeds.
    await wrapper.get('[aria-label="Actions for api"]').trigger("click");
    await wrapper.get('[aria-label="Scale api"]').trigger("click");
    await wrapper.get("#realtime-scale-replicas").setValue("5");
    apiMocks.fetchSwarmServices.mockResolvedValueOnce({ services: services.map((service) => ({ ...service, configured_replicas: 5 })) });
    await wrapper.get("form").trigger("submit");
    await flushPromises();
    resolveOldTasks({ tasks: [] });
    await flushPromises();
    expect(wrapper.findAll(".realtime-task-row")).toHaveLength(2);

    apiMocks.fetchSwarmServices.mockImplementationOnce(() => new Promise((resolve) => { resolveOldServices = resolve; }));
    await vi.advanceTimersByTimeAsync(5_000);
    await wrapper.get('[aria-label="Actions for api"]').trigger("click");
    await wrapper.get('[aria-label="Scale api"]').trigger("click");
    await wrapper.get("#realtime-scale-replicas").setValue("6");
    const latest = services.map((service) => ({ ...service, configured_replicas: 6, replicas: { running: 2, desired: 6 } }));
    apiMocks.fetchSwarmServices.mockResolvedValueOnce({ services: latest });
    await wrapper.get("form").trigger("submit");
    await flushPromises();
    resolveOldServices({ services });
    await flushPromises();
    expect(wrapper.get(".realtime-state-cell").text()).toBe("2 / 6 replicas");
    await wrapper.get('[aria-label="Actions for api"]').trigger("click");
    await wrapper.get('[aria-label="Scale api"]').trigger("click");
    expect(wrapper.get<HTMLInputElement>("#realtime-scale-replicas").element.value).toBe("6");
    const calls = apiMocks.fetchSwarmServices.mock.calls.length;
    await vi.advanceTimersByTimeAsync(5_000);
    await flushPromises();
    expect(apiMocks.fetchSwarmServices).toHaveBeenCalledTimes(calls + 1);
    wrapper.unmount();
  });
});

describe("Realtime service restart", () => {
  beforeEach(() => {
    window.localStorage.clear();
    window.localStorage.setItem("realtime.refreshInterval", "0");
    apiMocks.fetchSwarmServices.mockResolvedValue({ services });
    apiMocks.fetchSwarmServiceTasks.mockResolvedValue({ tasks });
    apiMocks.restartSwarmService.mockResolvedValue(undefined);
  });
  afterEach(() => vi.clearAllMocks());

  it("closes Actions on a second click after focus returns to its trigger", async () => {
    const wrapper = mount(RealtimeView, { attachTo: document.body, global: { stubs: { RouterLink: true } } });
    try {
      await flushPromises();
      const trigger = wrapper.get<HTMLButtonElement>('[aria-label="Actions for api"]').element;
      trigger.focus();
      trigger.click();
      await flushPromises();
      expect(trigger.getAttribute("aria-expanded")).toBe("true");
      expect(document.activeElement?.textContent).toContain("Restart service");

      // Pointer down moves focus to the trigger before the click fires.
      trigger.focus();
      await flushPromises();
      trigger.click();
      await flushPromises();

      expect(trigger.getAttribute("aria-expanded")).toBe("false");
      expect(document.querySelector(".realtime-actions-menu")).toBeNull();
      expect(document.activeElement).toBe(trigger);
    } finally {
      wrapper.unmount();
    }
  });

  it.each([
    ["Restart service", "realtime-restart-title"],
    ["Scale replicas", "realtime-scale-title"],
  ])("opens %s when a tap clears focus before click", async (action, dialogTitle) => {
    const wrapper = mount(RealtimeView, { attachTo: document.body, global: { stubs: { RouterLink: true } } });
    try {
      await flushPromises();
      await wrapper.get('[aria-label="Actions for proxy"]').trigger("click");
      const menu = document.querySelector(".realtime-actions-menu")!;
      const option = [...menu.querySelectorAll("button")].find((button) => button.textContent?.trim() === action)!;
      expect(document.activeElement).toBe(menu.querySelector("button"));

      document.activeElement!.dispatchEvent(new FocusEvent("focusout", { bubbles: true, relatedTarget: null }));
      await flushPromises();
      expect(option.isConnected).toBe(true);
      option.click();
      await flushPromises();

      expect(wrapper.get('[role="dialog"]').attributes("aria-labelledby")).toBe(dialogTitle);
      expect(document.querySelector(".realtime-actions-menu")).toBeNull();
    } finally {
      wrapper.unmount();
    }
  });

  it("closes Actions when tapping an outside control that stops click propagation", async () => {
    const wrapper = mount(RealtimeView, { attachTo: document.body, global: { stubs: { RouterLink: true } } });
    try {
      await flushPromises();
      const trigger = wrapper.get('[aria-label="Actions for api"]');
      await trigger.trigger("click");
      expect(trigger.attributes("aria-expanded")).toBe("true");

      document.activeElement!.dispatchEvent(new FocusEvent("focusout", { bubbles: true, relatedTarget: null }));
      await flushPromises();
      await wrapper.get('[aria-label="Expand api"]').trigger("click");

      expect(trigger.attributes("aria-expanded")).toBe("false");
      expect(apiMocks.fetchSwarmServiceTasks).toHaveBeenCalledWith("one");
    } finally {
      wrapper.unmount();
    }
  });

  it("offers restart only for replicated services with nonzero desired and configured counts", async () => {
    apiMocks.fetchSwarmServices.mockResolvedValue({ services: [
      ...services,
      { ...services[0], id: "zero", name: "zero", full_name: "zero", configured_replicas: 0 },
      ...["global", "replicated-job", "global-job"].map((mode) => ({ ...services[0], id: mode, name: mode, full_name: mode, mode })),
    ] });
    const wrapper = await mountView();
    expect(wrapper.findAll(".realtime-actions-button")).toHaveLength(5);
    for (const name of ["api", "proxy", "reports", "zero"]) {
      await wrapper.get(`[aria-label="Actions for ${name}"]`).trigger("click");
      expect(wrapper.get(".realtime-actions-menu").text().includes("Restart service")).toBe(["api", "proxy"].includes(name));
      expect(wrapper.get(".realtime-actions-menu").text()).toContain("Scale replicas");
    }
    expect(apiMocks.fetchSwarmServiceTasks).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it.each([null, "service changed while restarting; refresh and retry", "unable to restart service"])("refreshes after completion and prevents duplicate requests: %s", async (failure) => {
    let finish!: () => void;
    apiMocks.restartSwarmService.mockImplementationOnce(() => new Promise<void>((resolve, reject) => {
      finish = () => failure ? reject(new Error(failure)) : resolve();
    }));
    const wrapper = await mountView();
    await wrapper.get('[aria-label="Actions for api"]').trigger("click");
    await wrapper.get(".realtime-actions-menu button").trigger("click");
    expect(wrapper.get('[role="dialog"]').text()).toContain("All replicas will temporarily stop");
    expect(apiMocks.restartSwarmService).not.toHaveBeenCalled();
    const submit = wrapper.findAll('[role="dialog"] button').find((button) => button.text() === "Restart")!;
    await submit.trigger("click");
    await submit.trigger("click");
    expect(apiMocks.restartSwarmService).toHaveBeenCalledExactlyOnceWith("one");
    expect(wrapper.get('[aria-label="Close modal"]').attributes("disabled")).toBeDefined();
    apiMocks.fetchSwarmServices.mockResolvedValue({ services: services.map((service) => service.id === "one" ? { ...service, configured_replicas: failure ? 0 : 4, replicas: { running: 0, desired: failure ? 0 : 4 } } : service) });
    finish();
    await flushPromises();
    expect(apiMocks.fetchSwarmServices).toHaveBeenCalledTimes(2);
    const badge = wrapper.get(".realtime-state-cell .app-status-badge");
    expect(badge.text()).toBe(failure ? "0 / 0 replicas" : "0 / 4 replicas");
    expect(badge.classes()).toContain(failure ? "app-status-badge--success" : "app-status-badge--error");
    if (failure) {
      expect(wrapper.get('[role="alert"]').text()).toContain(failure);
      expect(wrapper.get('[role="alert"]').text()).toContain("Replica state may have changed");
      expect(wrapper.get('[role="dialog"]').text()).not.toContain("Restarting...");
    } else expect(wrapper.find('[role="dialog"]').exists()).toBe(false);
    expect(apiMocks.restartSwarmService).toHaveBeenCalledTimes(1);
    wrapper.unmount();
  });
});
