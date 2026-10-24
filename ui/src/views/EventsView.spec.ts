import { flushPromises, mount } from "@vue/test-utils";
import { createMemoryHistory, createRouter } from "vue-router";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { fetchEventFilters, fetchEvents } from "../api/overview";
import type { EventHistoryItem } from "../api/types";
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
    message: "Older info",
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
    type: "NodeDisconnected",
    severity: "alert",
    category: "swarm",
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
  return wrapper.findAll("tbody tr:not(.events-details-row) td:nth-child(4)").map((cell) => cell.text());
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

  it("restores multiple event types from the URL and keeps them selected", async () => {
    const { wrapper } = await mountView("/events?types=DeploySuccess&types=SyncManualStarted");

    const checkedTypes = wrapper
      .findAll(".events-type-filter-option input")
      .filter((input) => (input.element as HTMLInputElement).checked)
      .map((input) => input.element.getAttribute("value") ?? input.element.parentElement?.textContent?.trim());

    expect(checkedTypes).toHaveLength(2);
    expect(wrapper.find(".events-type-filter-selection").text()).toContain("DeploySuccess");
    expect(wrapper.find(".events-type-filter-selection").text()).toContain("SyncManualStarted");
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
      .findAll(".events-type-filter-option")
      .find((item) => item.find(".events-type-filter-option-name").text() === "DeploySuccess");
    expect(option).toBeDefined();

    await option!.find("input").setValue(true);
    await flushPromises();

    expect(router.currentRoute.value.query.types).toEqual(["DeploySuccess"]);

    const secondOption = wrapper
      .findAll(".events-type-filter-option")
      .find((item) => item.find(".events-type-filter-option-name").text() === "SyncManualStarted");
    await secondOption!.find("input").setValue(true);
    await flushPromises();

    expect(router.currentRoute.value.query.types).toEqual(["DeploySuccess", "SyncManualStarted"]);
  });


  it("offers WebhookReceived in the type filter", async () => {
    const { wrapper } = await mountView();
    const option = wrapper
      .findAll(".events-type-filter-option")
      .find((item) => item.find(".events-type-filter-option-name").text() === "WebhookReceived");
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
    const options = wrapper.findAll(".events-type-filter-option");

    expect(options).toHaveLength(14);
    expect(options.map((option) => option.find(".events-type-filter-option-name").text())).toEqual([
      "DeploySuccess",
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
    expect(options.map((option) => option.find(".events-type-filter-option-description").text()))
      .not.toContain("");
    expect(wrapper.find(".events-type-filter-menu").text()).toContain("Deployment completed successfully");
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

    expect(wrapper.find('select[aria-label="Filter events by severity"]').findAll("option").map((option) => option.text())).toEqual([
      "All severities",
      "Info",
      "Warning",
      "Error",
    ]);
    expect(wrapper.find(".events-table").text()).toContain("Node disconnected");
    expect(wrapper.find(".event-severity").text()).toBe("Alert");
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

    expect(renderedMessages(wrapper)).toEqual(["Newer info", "Warning", "Older info", "Error"]);

    const timeButton = wrapper.findAll("thead button").find((button) => button.text().startsWith("Time"));
    expect(timeButton).toBeDefined();
    expect(timeButton!.element.closest("th")?.getAttribute("aria-sort")).toBe("descending");

    await timeButton!.trigger("click");
    await flushPromises();

    expect(renderedMessages(wrapper)).toEqual(["Error", "Older info", "Warning", "Newer info"]);
    const refreshedTimeButton = wrapper.findAll("thead button").find((button) => button.text().startsWith("Time"));
    expect(refreshedTimeButton!.element.closest("th")?.getAttribute("aria-sort")).toBe("ascending");
  });

  it("sorts severity semantically and uses time descending as the tie-breaker", async () => {
    const { wrapper, router } = await mountView();

    const severityButton = wrapper.findAll("thead button").find((button) => button.text().startsWith("Severity"));
    expect(severityButton).toBeDefined();

    await severityButton!.trigger("click");
    await flushPromises();

    expect(renderedMessages(wrapper)).toEqual(["Error", "Warning", "Newer info", "Older info"]);
    expect(router.currentRoute.value.query.sort).toBe("severity");
    expect(router.currentRoute.value.query.order).toBe("desc");

    await severityButton!.trigger("click");
    await flushPromises();

    expect(renderedMessages(wrapper)).toEqual(["Newer info", "Older info", "Warning", "Error"]);
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
    expect(renderedMessages(wrapper)).toEqual(["Older info", "Error"]);
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
    expect(renderedMessages(wrapper)).toEqual(["Error", "Older info"]);
  });

  it("restores sorting from the URL", async () => {
    const { wrapper } = await mountView("/events?sort=severity&order=asc");

    expect(renderedMessages(wrapper)).toEqual(["Newer info", "Older info", "Warning", "Error"]);
    const severityHeader = wrapper.findAll("th").find((header) => header.text().startsWith("Severity"));
    expect(severityHeader?.attributes("aria-sort")).toBe("ascending");
  });

  it("restores contextual filters from the URL", async () => {
    const { wrapper } = await mountView(
      "/events?category=swarm&stack_name=deleted-stack&service_name=deleted-service&severity=warn",
    );

    expect((wrapper.find('select[aria-label="Filter events by category"]').element as HTMLSelectElement).value).toBe("swarm");
    expect(wrapper.find('summary[aria-label="Filter events by stack"]').text()).toContain("deleted-stack");
    expect(wrapper.find('summary[aria-label="Filter events by service"]').text()).toContain("deleted-service");
    expect(wrapper.findAll(".events-search-filter-menu button").map((button) => button.text())).toContain("deleted-stack");
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
    await filterButtons.find((button) => button.attributes("aria-label")?.includes("stack_name"))!.trigger("click");
    await flushPromises();
    expect(router.currentRoute.value.query.stack_name).toBe("payments");
    expect(fetchEvents).toHaveBeenLastCalledWith(expect.objectContaining({ stackNames: ["payments"] }));

    await wrapper.find("tbody tr.app-table-row--clickable").trigger("click");
    await wrapper.findAll(".event-detail-filter")
      .find((button) => button.attributes("aria-label")?.includes("service_name"))!.trigger("click");
    await flushPromises();
    expect(router.currentRoute.value.query.service_name).toBe("api");
    expect(fetchEvents).toHaveBeenLastCalledWith(expect.objectContaining({
      stackNames: ["payments"], serviceNames: ["api"],
    }));
  });

  it("searches stacks and filters services by the selected stack", async () => {
    const { wrapper } = await mountView();

    await wrapper.find('input[aria-label="Search stacks"]').setValue("pay");
    expect(wrapper.findAll(".events-search-filter-menu")[0].findAll("button").map((button) => button.text()))
      .toEqual(["All stacks", "payments"]);

    const payments = wrapper.findAll(".events-search-filter-menu")[0].findAll("button")
      .find((button) => button.text() === "payments");
    await payments!.trigger("click");
    await flushPromises();

    expect(wrapper.findAll(".events-search-filter-menu")[1].findAll("button").map((button) => button.text()))
      .toEqual(["All services", "api", "worker"]);

    await wrapper.find('input[aria-label="Search services"]').setValue("work");
    expect(wrapper.findAll(".events-search-filter-menu")[1].findAll("button").map((button) => button.text()))
      .toEqual(["All services", "worker"]);
  });

  it("distinguishes duplicate service names by stack and selects an unambiguous pair", async () => {
    const { wrapper, router } = await mountView();
    const serviceButtons = wrapper.findAll(".events-search-filter-menu")[1].findAll("button");
    expect(serviceButtons.map((button) => button.text())).toEqual([
      "All services", "apiarchive", "apipayments", "workerpayments",
    ]);

    await serviceButtons.find((button) => button.text() === "apipayments")!.trigger("click");
    await flushPromises();
    expect(router.currentRoute.value.query).toMatchObject({ stack_name: "payments", service_name: "api" });
  });

  it("resets an incompatible service when the stack changes", async () => {
    const { wrapper, router } = await mountView("/events?stack_name=payments&service_name=worker");
    const archive = wrapper.findAll(".events-search-filter-menu")[0].findAll("button")
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

    await wrapper.find('select[aria-label="Filter events by category"]').setValue("swarm");
    await flushPromises();

    expect(fetchEvents).toHaveBeenLastCalledWith({
      types: [], severities: [], categories: ["swarm"], limit: 50, sort: "time", order: "desc",
    });
    expect(renderedMessages(wrapper)).toEqual(["Warning"]);
    expect(wrapper.find(".events-pagination").text()).toContain("Page 1");
    expect(wrapper.findAll(".events-pagination button")[1].attributes("disabled")).toBeDefined();
  });
});
