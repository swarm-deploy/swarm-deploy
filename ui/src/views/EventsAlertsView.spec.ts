import { flushPromises, mount } from "@vue/test-utils";
import { createMemoryHistory, createRouter } from "vue-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { fetchAlerts, fetchEventFilters, fetchEvents } from "../api/overview";
import type { Alert } from "../api/types";
import { i18n } from "../i18n";
import EventsAlertsView from "./EventsAlertsView.vue";

vi.mock("../api/overview", () => ({ fetchEvents: vi.fn(), fetchEventFilters: vi.fn(), fetchAlerts: vi.fn() }));

const activeAlert: Alert = {
  id: "active-incident",
  fingerprint: "node:worker",
  kind: "node_disconnected",
  resourceType: "node",
  resourceId: "worker",
  status: "open",
  title: "Node disconnected",
  message: "Worker is unavailable",
  occurrences: 1,
  openedAt: "2026-10-10T11:00:00Z",
  updatedAt: "2026-10-10T11:00:00Z",
  openEventId: "open-event",
  latestEventId: "latest-event",
};

const resolvedAlert: Alert = {
  ...activeAlert,
  id: "resolved-incident",
  fingerprint: "service:app/api",
  status: "resolved",
  kind: "service_drift",
  resourceType: "service",
  resourceId: "app/api",
  title: "Service drift detected",
  message: "Service image differs from the manifest",
  resolvedAt: "2026-10-10T12:00:00Z",
  resolution: { reason: "recovered", message: "Service recovered", eventId: "recovery" },
};

async function mountView(path = "/events") {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: "/events", component: EventsAlertsView }],
  });
  await router.push(path);
  await router.isReady();
  const wrapper = mount(EventsAlertsView, { global: { plugins: [router] } });
  await flushPromises();
  return { wrapper, router };
}

function tab(wrapper: ReturnType<typeof mount>, label: string) {
  return wrapper.findAll('[role="tab"]').find((item) => item.text() === label)!;
}

function filter(wrapper: ReturnType<typeof mount>, label: string) {
  return wrapper.get<HTMLDetailsElement>(`details:has(> summary[aria-label="Filter ${label}"])`);
}

describe("Events & Alerts", () => {
  it("loads alerts for the linked stack and retains the stack navigation", async () => {
    vi.mocked(fetchAlerts).mockImplementation(async (options = {}) => ({ alerts: options.stack === "app" ? [{ ...activeAlert, resourceType: "stack", resourceId: "app" }] : [activeAlert] }));
    const { wrapper, router } = await mountView("/events?tab=alerts&stack_name=app&keep=value");
    expect(fetchAlerts).toHaveBeenLastCalledWith({ stack: "app" });
    expect(wrapper.get(".alerts-page header").text()).toContain("Stack: app");
    await router.push("/events?tab=alerts&stack_name=other&keep=value");
    await flushPromises();
    expect(fetchAlerts).toHaveBeenLastCalledWith({ stack: "other" });
    await wrapper.get(".alerts-page header .meta button").trigger("click");
    await flushPromises();
    expect(router.currentRoute.value.query).toEqual({ tab: "alerts", keep: "value" });
    expect(fetchAlerts).toHaveBeenLastCalledWith({});
    wrapper.unmount();
  });

  beforeEach(() => {
    vi.mocked(fetchEventFilters).mockResolvedValue({ stacks: [], services: [] });
    vi.mocked(fetchEvents).mockReset();
    vi.mocked(fetchEvents).mockResolvedValue({ events: [{
      id: "fact",
      type: "NodeJoined",
      severity: "info",
      category: "swarm",
      created_at: "2026-10-10T10:00:00Z",
      message: "Worker joined",
    }] });
    vi.mocked(fetchAlerts).mockReset();
    vi.mocked(fetchAlerts).mockResolvedValue({ alerts: [activeAlert, resolvedAlert] });
  });

  afterEach(() => { i18n.global.locale.value = "en"; });

  it("translates the standard node diagnostic in the alert list", async () => {
    i18n.global.locale.value = "ru";
    vi.mocked(fetchAlerts).mockResolvedValue({ alerts: [{ ...activeAlert, message: "Swarm node is not ready" }] });
    const { wrapper } = await mountView();
    expect(wrapper.get(".alerts-message").text()).toBe("Узел Swarm не готов к работе");
    wrapper.unmount();
  });

  it("shows all alerts by default and sorts by the displayed timestamp", async () => {
    const { wrapper, router } = await mountView();

    expect(wrapper.find("h2").text()).toBe("Events & Alerts");
    expect(tab(wrapper, "Alerts").attributes("aria-selected")).toBe("true");
    expect(tab(wrapper, "Events").attributes("aria-selected")).toBe("false");
    expect(wrapper.find("#alerts-panel").isVisible()).toBe(true);
    expect(wrapper.find("#events-panel").isVisible()).toBe(false);
    expect(router.currentRoute.value.query.tab).toBeUndefined();
    expect(fetchAlerts).toHaveBeenCalledWith({});
    expect(wrapper.findAll(".alerts-table thead th").map((column) => column.text()))
      .toEqual(["Alert", "Status", "Resource", "Updated ↓", "Occurrences"]);
    expect(wrapper.get(".app-table-sort").text()).toContain("Updated");
    expect(wrapper.findAll(".alerts-table thead th")[3].attributes("aria-sort")).toBe("descending");
    expect(wrapper.findAll(".alerts-title").map((title) => title.text()))
      .toEqual(["Service drift detected", "Node disconnected"]);
    expect(wrapper.get(".alerts-table").text()).not.toContain("Service recovered");
    await wrapper.get(".alerts-table .app-table-sort").trigger("click");
    expect(wrapper.findAll(".alerts-table thead th")[3].attributes("aria-sort")).toBe("ascending");
    expect(wrapper.findAll(".alerts-title").map((title) => title.text()))
      .toEqual(["Node disconnected", "Service drift detected"]);
    expect(wrapper.findAll("h2")).toHaveLength(1);
  });

  it("switches tabs and synchronizes the URL without dropping other query parameters", async () => {
    const { wrapper, router } = await mountView("/events?severity=warn");

    await tab(wrapper, "Events").trigger("click");
    await flushPromises();

    expect(router.currentRoute.value.query).toMatchObject({ tab: "events", severity: "warn" });
    expect(tab(wrapper, "Events").attributes("aria-selected")).toBe("true");
    expect(wrapper.find("#events-panel").isVisible()).toBe(true);
    expect(wrapper.find(".events-table").text()).toContain("Worker joined");

    await tab(wrapper, "Alerts").trigger("click");
    await flushPromises();
    expect(router.currentRoute.value.query).toMatchObject({ tab: "alerts", severity: "warn" });
  });

  it("opens direct event links and defaults invalid tab values to Alerts", async () => {
    const eventsView = await mountView("/events?tab=events");
    expect(tab(eventsView.wrapper, "Events").attributes("aria-selected")).toBe("true");
    eventsView.wrapper.unmount();

    const invalidView = await mountView("/events?tab=unknown");
    expect(tab(invalidView.wrapper, "Alerts").attributes("aria-selected")).toBe("true");
  });

  it("searches resources, combines filters and links to the selected alert", async () => {
    const { wrapper, router } = await mountView("/events?severity=warn");
    const status = filter(wrapper, "alerts by status");
    const type = filter(wrapper, "alerts by type");
    const resource = filter(wrapper, "alerts by resource");
    const search = resource.get<HTMLInputElement>('[aria-label="Search resources"]');

    await status.findAll("button").find((button) => button.text() === "Active")!.trigger("click");
    expect(wrapper.findAll(".alerts-title").map((title) => title.text())).toEqual(["Node disconnected"]);
    await status.findAll("button").find((button) => button.text() === "Resolved")!.trigger("click");
    expect(wrapper.findAll(".alerts-title").map((title) => title.text())).toEqual(["Service drift detected"]);

    await type.findAll("button").find((button) => button.text() === "node disconnected")!.trigger("click");
    expect(wrapper.text()).toContain("No alerts match the selected filters.");
    await type.findAll("button").find((button) => button.text() === "service drift")!.trigger("click");
    resource.element.open = true;
    await search.setValue("WORKER");
    expect(resource.findAll("button").map((button) => button.text())).toEqual(["All resources", "worker (node)"]);
    await resource.findAll("button")[1].trigger("click");
    expect(resource.attributes("open")).toBeUndefined();
    expect(search.element.value).toBe("");
    expect(wrapper.text()).toContain("No alerts match the selected filters.");
    await resource.findAll("button").find((button) => button.text() === "app/api (service)")!.trigger("click");
    expect(wrapper.findAll(".alerts-title").map((title) => title.text())).toEqual(["Service drift detected"]);

    const detailsURL = "/events?severity=warn&tab=alerts&alert_id=resolved-incident";
    expect(wrapper.get("a.alerts-title").attributes("href")).toBe(detailsURL);
    await wrapper.get(".alerts-table .alerts-row").trigger("click");
    await flushPromises();
    expect(router.currentRoute.value.fullPath).toBe(detailsURL);
    wrapper.unmount();
  });

  it("preserves filters in both tabs across tab switches", async () => {
    const { wrapper, router } = await mountView();
    const status = filter(wrapper, "alerts by status");
    const resource = filter(wrapper, "alerts by resource");
    const severity = filter(wrapper, "events by severity");
    await status.findAll("button").find((button) => button.text() === "Resolved")!.trigger("click");
    await resource.findAll("button").find((button) => button.text() === "app/api (service)")!.trigger("click");
    await tab(wrapper, "Events").trigger("click");
    await flushPromises();

    await severity.findAll("button").find((button) => button.text() === "Error")!.trigger("click");
    await flushPromises();
    const typeOption = wrapper
      .findAll(".events-type-filter .app-select-option")
      .find((item) => item.find(".app-select-option-label").text() === "NodeJoined")!;
    await typeOption.find("input").setValue(true);
    await flushPromises();

    expect(router.currentRoute.value.query).toMatchObject({
      tab: "events",
      severity: "error",
      types: ["NodeJoined"],
    });

    await tab(wrapper, "Alerts").trigger("click");
    await tab(wrapper, "Events").trigger("click");
    await flushPromises();

    expect(severity.get("button[aria-pressed=true]").text()).toBe("Error");
    expect((typeOption.find("input").element as HTMLInputElement).checked).toBe(true);

    await tab(wrapper, "Alerts").trigger("click");
    expect(status.get("button[aria-pressed=true]").text()).toBe("Resolved");
    expect(resource.get("summary").text()).toBe("app/api (service)");
    wrapper.unmount();
  });
});
