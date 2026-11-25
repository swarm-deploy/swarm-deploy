import { flushPromises, mount } from "@vue/test-utils";
import { createMemoryHistory, createRouter } from "vue-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { fetchEventFilters, fetchEvents } from "../api/overview";
import type { EventHistoryItem } from "../api/types";
import EventDetailsModal from "../components/overview/EventDetailsModal.vue";
import { i18n } from "../i18n";
import EventsView from "./EventsView.vue";

vi.mock("../api/overview", () => ({
  fetchEventFilters: vi.fn(),
  fetchEvents: vi.fn(),
}));

const filterOptions = {
  stacks: ["archive", "payments"],
  services: [
    { stack_name: "archive", service_name: "api" },
    { stack_name: "payments", service_name: "api" },
    { stack_name: "payments", service_name: "worker" },
  ],
};

const events: EventHistoryItem[] = [
  {
    id: "older-info",
    type: "SyncManualStarted",
    severity: "info",
    category: "sync",
    created_at: "2026-10-06T16:00:00Z",
    message: "Manual sync started",
  },
  {
    id: "newer-info",
    type: "DeploySuccess",
    severity: "info",
    category: "sync",
    created_at: "2026-10-06T18:00:00Z",
    message: "Newer info",
  },
  {
    id: "warning",
    type: "ServiceMissed",
    severity: "warn",
    category: "swarm",
    created_at: "2026-10-06T17:00:00Z",
    message: "Warning",
  },
  {
    id: "error",
    type: "SendNotificationFailed",
    severity: "error",
    category: "sync",
    created_at: "2026-10-06T15:00:00Z",
    message: "Error",
    details: {
      stack_name: "payments",
      service_name: "api",
    },
  },
];

async function mountView(path = "/events") {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: "/events", component: EventsView }],
  });
  await router.push(path);
  await router.isReady();

  const wrapper = mount(EventsView, {
    global: { plugins: [router] },
  });
  await flushPromises();

  return { wrapper, router };
}

function renderedMessages(wrapper: ReturnType<typeof mount>): string[] {
  return wrapper.findAll("tbody tr:not(.events-details-row) td:nth-child(3)").map((cell) => cell.text());
}

function filter(wrapper: ReturnType<typeof mount>, name: string) {
  return wrapper.get(`details:has(> summary[aria-label="Filter events by ${name}"])`);
}

describe("EventsView", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    vi.mocked(fetchEventFilters).mockReset();
    vi.mocked(fetchEventFilters).mockResolvedValue(filterOptions);
    vi.mocked(fetchEvents).mockReset();
    vi.mocked(fetchEvents).mockImplementation(async (options = {}) => {
      const severityRank = { info: 0, warn: 1, error: 2, alert: 3 };
      const sorted = [...events].sort((left, right) => {
        if (options.sort === "severity") {
          const delta = severityRank[left.severity] - severityRank[right.severity];
          if (delta !== 0) return options.order === "asc" ? delta : -delta;
          return Date.parse(right.created_at) - Date.parse(left.created_at);
        }
        const delta = Date.parse(left.created_at) - Date.parse(right.created_at);
        return options.order === "asc" ? delta : -delta;
      });
      return { events: sorted };
    });
  });

  afterEach(() => { i18n.global.locale.value = "en"; });

  it.each([
    ["en", "Service prod/api replicas increased from 0 to 3"],
    ["ru", "Для сервиса prod/api число реплик увеличено с 0 до 3"],
  ] as const)("renders the same message in the event list and modal in %s", async (locale, expected) => {
    i18n.global.locale.value = locale;
    const scaled: EventHistoryItem = {
      ...events[0], type: "ServiceReplicasIncreased", message: "Stored scale message",
      details: { stack_name: "prod", service_name: "api", previous_replicas: "0", current_replicas: "3" },
    };
    const legacy = { ...scaled, id: "legacy", details: { stack_name: "prod" }, message: "Legacy scaling details" };
    const unknown = { ...scaled, id: "unknown", type: "FutureEvent", message: "Future diagnostic details" };
    vi.mocked(fetchEvents).mockResolvedValueOnce({ events: [scaled, legacy, unknown] });
    const { wrapper } = await mountView("/events?severity=info");

    expect(renderedMessages(wrapper)).toEqual([expected, legacy.message, unknown.message]);
    expect(fetchEvents).toHaveBeenCalledWith(expect.objectContaining({ severities: ["info"] }));
    const modal = mount(EventDetailsModal, {
      props: { open: true, event: scaled, loading: false, error: "" },
    });
    expect(modal.get("tr:nth-child(5) td").text()).toBe(expected);
    await modal.setProps({ event: legacy });
    expect(modal.get("tr:nth-child(5) td").text()).toBe(legacy.message);
    await modal.setProps({ event: unknown });
    expect(modal.get("tr:nth-child(5) td").text()).toBe(unknown.message);
    modal.unmount();
    wrapper.unmount();
  });

  it("localizes a sync pause outside the default type options", async () => {
    i18n.global.locale.value = "ru";
    vi.mocked(fetchEvents).mockResolvedValueOnce({ events: [{
      ...events[0], type: "SyncPaused", message: "Stored sync pause",
      details: { scope: "service", stack_name: "prod", service_name: "api" },
    }] });
    const { wrapper } = await mountView("/events?types=SyncPaused");

    expect(renderedMessages(wrapper)).toEqual(["Синхронизация для prod/api приостановлена"]);
    expect(wrapper.get(".events-type-filter .app-select-menu").text()).toContain("Синхронизация приостановлена");
    expect(fetchEvents).toHaveBeenCalledWith(expect.objectContaining({ types: ["SyncPaused"] }));
    wrapper.unmount();
  });

  it.each(["success", "error"])("ignores stale cursor %s after a filter change", async (outcome) => {
    let resolveOld!: (value: { events: EventHistoryItem[] }) => void;
    let rejectOld!: (reason: Error) => void;
    const old = new Promise<{ events: EventHistoryItem[] }>((resolve, reject) => {
      resolveOld = resolve;
      rejectOld = reject;
    });
    vi.mocked(fetchEvents)
      .mockResolvedValueOnce({ events: [events[0]], nextCursor: "old-next" })
      .mockReturnValueOnce(old)
      .mockResolvedValueOnce({ events: [events[2]] });
    const { wrapper, router } = await mountView();
    await wrapper.get(".events-pagination button:last-child").trigger("click");
    expect(fetchEvents).toHaveBeenLastCalledWith(expect.objectContaining({ cursor: "old-next" }));
    await router.replace("/events?severity=warn");
    await flushPromises();
    if (outcome === "success") resolveOld({ events: [events[3]] });
    else rejectOld(new Error("stale error"));
    await flushPromises();
    expect(renderedMessages(wrapper)).toEqual(["Warning"]);
    expect(wrapper.get(".events-pagination").text()).toContain("Page 1");
    expect(wrapper.text()).not.toContain("stale error");
    expect(fetchEvents).toHaveBeenCalledTimes(3);
    wrapper.unmount();
  });

  it("restores multiple event types from the URL and keeps them selected", async () => {
    const { wrapper } = await mountView("/events?types=DeploySuccess&types=SyncManualStarted");

    const checkedTypes = wrapper
      .findAll(".events-type-filter .app-select-option input")
      .filter((input) => (input.element as HTMLInputElement).checked)
      .map((input) => input.element.getAttribute("value") ?? input.element.parentElement?.textContent?.trim());

    expect(checkedTypes).toHaveLength(2);
    expect(wrapper.find(".events-type-filter .app-select-selection").text()).toContain("DeploySuccess");
    expect(wrapper.find(".events-type-filter .app-select-selection").text()).toContain("SyncManualStarted");
    expect(fetchEvents).toHaveBeenLastCalledWith({
      types: ["DeploySuccess", "SyncManualStarted"],
      severities: [],
      limit: 50,
      sort: "time",
      order: "desc",
    });
  });

  it("updates repeated type query parameters from the multi-select", async () => {
    const { wrapper, router } = await mountView();

    const option = wrapper
      .findAll(".events-type-filter .app-select-option")
      .find((item) => item.find(".app-select-option-label").text() === "DeploySuccess");
    expect(option).toBeDefined();

    await option!.find("input").setValue(true);
    await flushPromises();

    expect(router.currentRoute.value.query.types).toEqual(["DeploySuccess"]);

    const secondOption = wrapper
      .findAll(".events-type-filter .app-select-option")
      .find((item) => item.find(".app-select-option-label").text() === "SyncManualStarted");
    await secondOption!.find("input").setValue(true);
    await flushPromises();

    expect(router.currentRoute.value.query.types).toEqual(["DeploySuccess", "SyncManualStarted"]);
  });


  it("offers WebhookReceived in the type filter", async () => {
    const { wrapper } = await mountView();
    const option = wrapper
      .findAll(".events-type-filter .app-select-option")
      .find((item) => item.find(".app-select-option-label").text() === "WebhookReceived");
    expect(option).toBeDefined();
    await option!.find("input").setValue(true);
    await flushPromises();
    expect(fetchEvents).toHaveBeenLastCalledWith({
      types: ["WebhookReceived"],
      severities: [],
      limit: 50,
      sort: "time",
      order: "desc",
    });
  });

  it("shows concise descriptions for all event type filter options", async () => {
    const { wrapper } = await mountView();
    const options = wrapper.findAll(".events-type-filter .app-select-option");

    expect(options).toHaveLength(15);
    expect(options.map((option) => option.find(".app-select-option-label").text())).toEqual([
      "DeploySuccess",
      "SendNotificationFailed",
      "SyncManualStarted",
      "WebhookReceived",
      "NodeJoined",
      "NodeConnected",
      "NodeDisconnected",
      "ServiceMissed",
      "ServiceReplicasIncreased",
      "ServiceReplicasDecreased",
      "ServiceRestarted",
      "ServicePruned",
      "NetworkCreated",
      "UserAuthenticated",
      "AssistantPromptInjectionDetected",
    ]);
    expect(options.map((option) => option.find("small").text()))
      .not.toContain("");
    expect(wrapper.find(".events-type-filter .app-select-menu").text()).toContain("Deployment completed successfully");
  });

  it("shows only user-facing severity choices while keeping alert events visible by default", async () => {
    const alertEvent: EventHistoryItem = {
      id: "alert",
      type: "NodeDisconnected",
      severity: "alert",
      category: "swarm",
      created_at: "2026-10-06T19:00:00Z",
      message: "Node disconnected",
    };
    vi.mocked(fetchEvents).mockResolvedValueOnce({ events: [alertEvent] });
    const { wrapper } = await mountView();

    expect(filter(wrapper, "severity").findAll("button").map((option) => option.text())).toEqual([
      "All severities",
      "Info",
      "Warning",
      "Error",
    ]);
    expect(wrapper.find(".events-table").text()).toContain("Node disconnected");
    expect(wrapper.find(".event-severity").text()).toBe("Alert");
  });

  it("renders the five icon-led filters in the expected order", async () => {
    const { wrapper } = await mountView();
    const controls = wrapper.findAll(".events-filter-bar .app-select > summary");

    expect(controls.map((control) => control.attributes("aria-label"))).toEqual([
      "Filter events by type",
      "Filter events by severity",
      "Filter events by category",
      "Filter events by stack",
      "Filter events by service",
    ]);
    expect(wrapper.findAll(".events-filter-bar .events-filter-icon")).toHaveLength(5);
    expect(wrapper.findAll(".events-filter-bar select")).toHaveLength(0);
  });

  it("closes the type filter when clicking outside", async () => {
    const { wrapper } = await mountView();

    const filter = wrapper.find("details.events-type-filter").element as HTMLDetailsElement;
    filter.open = true;
    expect(filter.open).toBe(true);

    document.body.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await wrapper.vm.$nextTick();

    expect(filter.open).toBe(false);
  });

  it("sorts by time descending by default and toggles time direction", async () => {
    const { wrapper } = await mountView();

    expect(renderedMessages(wrapper)).toEqual(["Newer info", "Warning", "Manual sync started", "Error"]);

    const timeButton = wrapper.findAll("thead button").find((button) => button.text().startsWith("Time"));
    expect(timeButton).toBeDefined();
    expect(timeButton!.element.closest("th")?.getAttribute("aria-sort")).toBe("descending");

    await timeButton!.trigger("click");
    await flushPromises();

    expect(renderedMessages(wrapper)).toEqual(["Error", "Manual sync started", "Warning", "Newer info"]);
    const refreshedTimeButton = wrapper.findAll("thead button").find((button) => button.text().startsWith("Time"));
    expect(refreshedTimeButton!.element.closest("th")?.getAttribute("aria-sort")).toBe("ascending");
  });

  it("sorts severity semantically and uses time descending as the tie-breaker", async () => {
    const { wrapper, router } = await mountView();

    const severityButton = wrapper.findAll("thead button").find((button) => button.text().startsWith("Severity"));
    expect(severityButton).toBeDefined();

    await severityButton!.trigger("click");
    await flushPromises();

    expect(renderedMessages(wrapper)).toEqual(["Error", "Warning", "Newer info", "Manual sync started"]);
    expect(router.currentRoute.value.query.sort).toBe("severity");
    expect(router.currentRoute.value.query.order).toBe("desc");

    await severityButton!.trigger("click");
    await flushPromises();

    expect(renderedMessages(wrapper)).toEqual(["Newer info", "Manual sync started", "Warning", "Error"]);
    expect(router.currentRoute.value.query.order).toBe("asc");
  });

  it("navigates between cursor pages and replaces the visible rows", async () => {
    vi.mocked(fetchEvents)
      .mockResolvedValueOnce({ events: [events[1], events[2]], nextCursor: "page-2" })
      .mockResolvedValueOnce({ events: [events[0], events[3]] })
      .mockResolvedValueOnce({ events: [events[1], events[2]], nextCursor: "page-2" });
    const { wrapper } = await mountView();

    expect(renderedMessages(wrapper)).toEqual(["Newer info", "Warning"]);
    expect(wrapper.find(".events-pagination").text()).toContain("Page 1");
    expect(wrapper.findAll(".events-pagination button")[0].attributes("disabled")).toBeDefined();

    await wrapper.findAll(".events-pagination button")[1].trigger("click");
    await flushPromises();
    expect(fetchEvents).toHaveBeenLastCalledWith({
      types: [], severities: [], limit: 50, sort: "time", order: "desc", cursor: "page-2",
    });
    expect(renderedMessages(wrapper)).toEqual(["Manual sync started", "Error"]);
    expect(wrapper.find(".events-pagination").text()).toContain("Page 2");
    expect(wrapper.findAll(".events-pagination button")[1].attributes("disabled")).toBeDefined();

    await wrapper.findAll(".events-pagination button")[0].trigger("click");
    await flushPromises();
    expect(fetchEvents).toHaveBeenLastCalledWith({
      types: [], severities: [], limit: 50, sort: "time", order: "desc",
    });
    expect(renderedMessages(wrapper)).toEqual(["Newer info", "Warning"]);
    expect(wrapper.find(".events-pagination").text()).toContain("Page 1");
  });

  it("resets the cursor and loaded rows when the sort changes", async () => {
    vi.mocked(fetchEvents)
      .mockResolvedValueOnce({ events: [events[1]], nextCursor: "page-2" })
      .mockResolvedValueOnce({ events: [events[3], events[0]] });
    const { wrapper } = await mountView();
    const timeButton = wrapper.findAll("thead button").find((button) => button.text().startsWith("Time"));
    await timeButton!.trigger("click");
    await flushPromises();
    expect(fetchEvents).toHaveBeenLastCalledWith({
      types: [], severities: [], limit: 50, sort: "time", order: "asc",
    });
    expect(renderedMessages(wrapper)).toEqual(["Error", "Manual sync started"]);
  });

  it("restores sorting from the URL", async () => {
    const { wrapper } = await mountView("/events?sort=severity&order=asc");

    expect(renderedMessages(wrapper)).toEqual(["Newer info", "Manual sync started", "Warning", "Error"]);
    const severityHeader = wrapper.findAll("th").find((header) => header.text().startsWith("Severity"));
    expect(severityHeader?.attributes("aria-sort")).toBe("ascending");
  });

  it("restores contextual filters from the URL", async () => {
    const { wrapper } = await mountView(
      "/events?category=swarm&stack_name=deleted-stack&service_name=deleted-service&severity=warn",
    );

    expect(filter(wrapper, "category").get("button[aria-pressed=true]").text()).toBe("Swarm");
    expect(wrapper.find('summary[aria-label="Filter events by stack"]').text()).toContain("deleted-stack");
    expect(wrapper.find('summary[aria-label="Filter events by service"]').text()).toContain("deleted-service");
    expect(filter(wrapper, "stack").get("button[aria-pressed=true]").text()).toBe("deleted-stack");
    expect(filter(wrapper, "service").get("button[aria-pressed=true]").text()).toBe("deleted-service");
    expect(fetchEvents).toHaveBeenLastCalledWith({
      types: [],
      severities: ["warn"],
      categories: ["swarm"],
      stackNames: ["deleted-stack"],
      serviceNames: ["deleted-service"],
      limit: 50,
      sort: "time",
      order: "desc",
    });
  });

  it("applies stack and service filters from event details", async () => {
    vi.mocked(fetchEvents).mockResolvedValue({ events: [events[3]] });
    const { wrapper, router } = await mountView();
    await wrapper.find("tbody tr.app-table-row--clickable").trigger("click");

    const filterButtons = wrapper.findAll(".event-detail-filter");
    expect(filterButtons).toHaveLength(2);
    await filterButtons.find((button) => button.attributes("aria-label") === "Filter by Stack payments")!.trigger("click");
    await flushPromises();
    expect(router.currentRoute.value.query.stack_name).toBe("payments");
    expect(fetchEvents).toHaveBeenLastCalledWith(expect.objectContaining({ stackNames: ["payments"] }));

    await wrapper.find("tbody tr.app-table-row--clickable").trigger("click");
    await wrapper.findAll(".event-detail-filter")
      .find((button) => button.attributes("aria-label") === "Filter by Service api")!.trigger("click");
    await flushPromises();
    expect(router.currentRoute.value.query.service_name).toBe("api");
    expect(fetchEvents).toHaveBeenLastCalledWith(expect.objectContaining({
      stackNames: ["payments"], serviceNames: ["api"],
    }));
  });

  it("searches stacks and filters services by the selected stack", async () => {
    const { wrapper } = await mountView();
    const stack = filter(wrapper, "stack");
    const service = filter(wrapper, "service");

    await stack.get('input[aria-label="Search stacks"]').setValue("pay");
    expect(stack.findAll("button").map((button) => button.text())).toEqual(["All stacks", "payments"]);

    const payments = stack.findAll("button").find((button) => button.text() === "payments");
    await payments!.trigger("click");
    await flushPromises();

    expect(service.findAll("button").map((button) => button.text())).toEqual(["All services", "api", "worker"]);

    await service.get('input[aria-label="Search services"]').setValue("work");
    expect(service.findAll("button").map((button) => button.text())).toEqual(["All services", "worker"]);
    await service.get('input[aria-label="Search services"]').setValue("payments");
    expect(service.findAll("button").map((button) => button.text())).toEqual(["All services", "api", "worker"]);

    await stack.get('input[aria-label="Search stacks"]').setValue("missing");
    expect(stack.text()).toContain("No matching stacks");
    await stack.get("button").trigger("click");
    await flushPromises();
    expect(stack.get("summary").text()).toBe("All stacks");
  });

  it("distinguishes duplicate service names by stack and selects an unambiguous pair", async () => {
    const { wrapper, router } = await mountView();
    const serviceButtons = filter(wrapper, "service").findAll("button");
    expect(serviceButtons.map((button) => button.text())).toEqual([
      "All services", "apiarchive", "apipayments", "workerpayments",
    ]);

    await serviceButtons.find((button) => button.text() === "apipayments")!.trigger("click");
    await flushPromises();
    expect(router.currentRoute.value.query).toMatchObject({ stack_name: "payments", service_name: "api" });
  });

  it("resets an incompatible service when the stack changes", async () => {
    const { wrapper, router } = await mountView("/events?stack_name=payments&service_name=worker");
    const archive = filter(wrapper, "stack").findAll("button")
      .find((button) => button.text() === "archive");
    await archive!.trigger("click");
    await flushPromises();
    expect(router.currentRoute.value.query.stack_name).toBe("archive");
    expect(router.currentRoute.value.query.service_name).toBeUndefined();
  });

  it("uses a compact mobile filter toggle with the active filter count", async () => {
    const { wrapper } = await mountView("/events?severity=warn&stack_name=payments");
    const toggle = wrapper.find(".events-mobile-filter-toggle");
    expect(toggle.text()).toBe("Filters (2)");
    expect(toggle.attributes("aria-expanded")).toBe("false");
    await toggle.trigger("click");
    expect(toggle.attributes("aria-expanded")).toBe("true");
    expect(wrapper.find(".events-filter-bar").classes()).toContain("events-filter-bar--open");
  });

  it("resets pagination when an investigation filter changes", async () => {
    vi.mocked(fetchEvents)
      .mockResolvedValueOnce({ events: [events[1]], nextCursor: "page-2" })
      .mockResolvedValueOnce({ events: [events[2]] });
    const { wrapper } = await mountView();

    await filter(wrapper, "category").findAll("button").find((button) => button.text() === "Swarm")!.trigger("click");
    await flushPromises();

    expect(fetchEvents).toHaveBeenLastCalledWith({
      types: [], severities: [], categories: ["swarm"], limit: 50, sort: "time", order: "desc",
    });
    expect(renderedMessages(wrapper)).toEqual(["Warning"]);
    expect(wrapper.find(".events-pagination").text()).toContain("Page 1");
    expect(wrapper.findAll(".events-pagination button")[1].attributes("disabled")).toBeDefined();
  });
});
