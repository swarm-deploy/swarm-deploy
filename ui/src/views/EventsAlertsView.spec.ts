import { flushPromises, mount } from "@vue/test-utils";
import { createMemoryHistory, createRouter } from "vue-router";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { fetchAlerts, fetchEvents } from "../api/overview";
import type { Alert } from "../api/types";
import EventsAlertsView from "./EventsAlertsView.vue";

vi.mock("../stores/overview", () => ({ useOverviewStore: () => ({ openAlertDetailsModal: vi.fn() }) }));
vi.mock("../api/overview", () => ({ fetchEvents: vi.fn(), fetchAlerts: vi.fn() }));

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
  fingerprint: "node:restored-worker",
  resourceId: "restored-worker",
  status: "resolved",
  title: "Node reconnected",
  resolvedAt: "2026-10-10T12:00:00Z",
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

describe("Events & Alerts", () => {
  beforeEach(() => {
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
    vi.mocked(fetchAlerts).mockImplementation(async (options = {}) => ({
      alerts: options.status === "open" ? [activeAlert] : [activeAlert, resolvedAlert],
    }));
  });

  it("selects Alerts by default and loads active alerts only", async () => {
    const { wrapper, router } = await mountView();

    expect(wrapper.find("h1").text()).toBe("Events & Alerts");
    expect(tab(wrapper, "Alerts").attributes("aria-selected")).toBe("true");
    expect(tab(wrapper, "Events").attributes("aria-selected")).toBe("false");
    expect(wrapper.find("#alerts-panel").isVisible()).toBe(true);
    expect(wrapper.find("#events-panel").isVisible()).toBe(false);
    expect(router.currentRoute.value.query.tab).toBeUndefined();
    expect(fetchAlerts).toHaveBeenCalledWith({ status: "open" });
    expect(wrapper.find(".alerts-table").text()).toContain("Node disconnected");
    expect(wrapper.find(".alerts-table").text()).not.toContain("Node reconnected");
    expect(wrapper.findAll("h2")).toHaveLength(0);
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

  it("shows resolved alerts when the checkbox is enabled", async () => {
    const { wrapper } = await mountView();
    const checkbox = wrapper.find('.alerts-resolved-filter input[type="checkbox"]');

    expect((checkbox.element as HTMLInputElement).checked).toBe(false);
    await checkbox.setValue(true);
    await flushPromises();

    expect(fetchAlerts).toHaveBeenLastCalledWith({});
    expect(wrapper.find(".alerts-table").text()).toContain("Node disconnected");
    expect(wrapper.find(".alerts-table").text()).toContain("Node reconnected");
  });

  it("keeps the resolved-alert control visible in the active-alert empty state", async () => {
    vi.mocked(fetchAlerts).mockImplementation(async (options = {}) => ({
      alerts: options.status === "open" ? [] : [resolvedAlert],
    }));
    const { wrapper } = await mountView();

    expect(wrapper.find(".alerts-resolved-filter").exists()).toBe(true);
    expect(wrapper.find(".alerts-empty strong").text()).toBe("No active alerts");
    expect(wrapper.find(".alerts-empty .meta").text()).toBe("All alerts are resolved.");

    await wrapper.find('.alerts-resolved-filter input[type="checkbox"]').setValue(true);
    await flushPromises();
    expect(wrapper.find(".alerts-table").text()).toContain("Node reconnected");
  });

  it("preserves event filters and the resolved-alert checkbox across tab switches", async () => {
    const { wrapper, router } = await mountView();
    await wrapper.find('.alerts-resolved-filter input[type="checkbox"]').setValue(true);
    await tab(wrapper, "Events").trigger("click");
    await flushPromises();

    await wrapper.find(".events-filter-field select").setValue("error");
    await flushPromises();
    const typeOption = wrapper
      .findAll(".events-type-filter-option")
      .find((item) => item.find(".events-type-filter-option-name").text() === "NodeJoined")!;
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

    expect((wrapper.find(".events-filter-field select").element as HTMLSelectElement).value).toBe("error");
    expect((typeOption.find("input").element as HTMLInputElement).checked).toBe(true);

    await tab(wrapper, "Alerts").trigger("click");
    expect((wrapper.find('.alerts-resolved-filter input[type="checkbox"]').element as HTMLInputElement).checked).toBe(true);
  });
});
