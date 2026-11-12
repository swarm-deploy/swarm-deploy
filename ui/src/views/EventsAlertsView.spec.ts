import { flushPromises, mount } from "@vue/test-utils";
import { createMemoryHistory, createRouter } from "vue-router";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { fetchAlerts, fetchEventFilters, fetchEvents } from "../api/overview";
import type { Alert } from "../api/types";
import EventsAlertsView from "./EventsAlertsView.vue";

const { openAlertDetailsModal } = vi.hoisted(() => ({ openAlertDetailsModal: vi.fn() }));
vi.mock("../stores/overview", () => ({ useOverviewStore: () => ({ openAlertDetailsModal }) }));
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
    routes: [
      { path: "/events", component: EventsAlertsView },
      { path: "/stacks/:stack", name: "stack-details", component: { template: "<div />" } },
      { path: "/services/:stack/:service", name: "service-details", component: { template: "<div />" } },
    ],
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
    openAlertDetailsModal.mockClear();
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

  it("shows all alerts by default and sorts by the displayed timestamp", async () => {
    const { wrapper, router } = await mountView();

    expect(wrapper.find("h1").text()).toBe("Events & Alerts");
    expect(tab(wrapper, "Alerts").attributes("aria-selected")).toBe("true");
    expect(tab(wrapper, "Events").attributes("aria-selected")).toBe("false");
    expect(wrapper.find("#alerts-panel").isVisible()).toBe(true);
    expect(wrapper.find("#events-panel").isVisible()).toBe(false);
    expect(router.currentRoute.value.query.tab).toBeUndefined();
    expect(fetchAlerts).toHaveBeenCalledWith({});
    expect(wrapper.findAll(".alerts-table th").slice(1).map((column) => column.text()))
      .toEqual(["Status", "Alert", "Resource", "Occurrences"]);
    expect(wrapper.get(".app-table-sort").text()).toContain("Updated");
    expect(wrapper.get(".alerts-table th").attributes("aria-sort")).toBe("descending");
    expect(wrapper.findAll(".alerts-title").map((title) => title.text()))
      .toEqual(["Service drift detected", "Node disconnected"]);
    expect(wrapper.get(".app-status-badge--error").text()).toBe("Active");
    expect(wrapper.get(".app-status-badge--neutral").text()).toBe("Resolved");
    expect(wrapper.get(".alerts-table").text()).not.toContain("Service recovered");
    await wrapper.get(".alerts-table .app-table-sort").trigger("click");
    expect(wrapper.get(".alerts-table th").attributes("aria-sort")).toBe("ascending");
    expect(wrapper.findAll(".alerts-title").map((title) => title.text()))
      .toEqual(["Node disconnected", "Service drift detected"]);
    expect(wrapper.findAll("h2")).toHaveLength(0);
  });

  it("links stack and service resources without opening the alert modal", async () => {
    vi.mocked(fetchAlerts).mockResolvedValue({ alerts: [
      activeAlert,
      resolvedAlert,
      { ...activeAlert, id: "stack-incident", resourceType: "stack", resourceId: "app" },
      { ...activeAlert, id: "malformed-service", resourceType: "service", resourceId: "missing-stack" },
    ] });
    const { wrapper, router } = await mountView();
    const resourceLinks = wrapper.findAll(".alerts-resource a");

    expect(resourceLinks.map((link) => link.attributes("href"))).toEqual(["/services/app/api", "/stacks/app"]);
    await resourceLinks[0]!.trigger("click");
    await flushPromises();
    expect(router.currentRoute.value.path).toBe("/services/app/api");
    expect(openAlertDetailsModal).not.toHaveBeenCalled();
    wrapper.unmount();
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

  it("searches resources, combines filters and opens the original alert details", async () => {
    const { wrapper } = await mountView();
    const status = wrapper.get('[aria-label="Filter alerts by status"]');
    const type = wrapper.get('[aria-label="Filter alerts by type"]');
    const resource = wrapper.get("#alerts-filter-panel .events-search-filter");
    const search = resource.get<HTMLInputElement>('[aria-label="Search resources"]');

    await status.setValue("open");
    expect(wrapper.findAll(".alerts-title").map((title) => title.text())).toEqual(["Node disconnected"]);
    await status.setValue("resolved");
    expect(wrapper.findAll(".alerts-title").map((title) => title.text())).toEqual(["Service drift detected"]);

    await type.setValue("node_disconnected");
    expect(wrapper.text()).toContain("No alerts match the selected filters.");
    await type.setValue("service_drift");
    (resource.element as HTMLDetailsElement).open = true;
    await search.setValue("WORKER");
    expect(resource.findAll("button").map((button) => button.text())).toEqual(["All resources", "worker (node)"]);
    await resource.findAll("button")[1].trigger("click");
    expect(resource.attributes("open")).toBeUndefined();
    expect(search.element.value).toBe("");
    expect(wrapper.text()).toContain("No alerts match the selected filters.");
    await resource.findAll("button").find((button) => button.text() === "app/api (service)")!.trigger("click");
    expect(wrapper.findAll(".alerts-title").map((title) => title.text())).toEqual(["Service drift detected"]);

    await wrapper.get(".alerts-table tbody tr").trigger("click");
    await wrapper.get(".alerts-title").trigger("click");
    expect(openAlertDetailsModal.mock.calls).toEqual([[resolvedAlert], [resolvedAlert]]);
    wrapper.unmount();
  });

  it("preserves filters in both tabs across tab switches", async () => {
    const { wrapper, router } = await mountView();
    const status = wrapper.get<HTMLSelectElement>('[aria-label="Filter alerts by status"]');
    const resource = wrapper.get("#alerts-filter-panel .events-search-filter");
    await status.setValue("resolved");
    await resource.findAll("button").find((button) => button.text() === "app/api (service)")!.trigger("click");
    await tab(wrapper, "Events").trigger("click");
    await flushPromises();

    await wrapper.find("#events-filter-panel .events-filter-field select").setValue("error");
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

    expect((wrapper.find("#events-filter-panel .events-filter-field select").element as HTMLSelectElement).value).toBe("error");
    expect((typeOption.find("input").element as HTMLInputElement).checked).toBe(true);

    await tab(wrapper, "Alerts").trigger("click");
    expect(status.element.value).toBe("resolved");
    expect(resource.get(".events-filter-selection").text()).toBe("app/api (service)");
    wrapper.unmount();
  });
});
