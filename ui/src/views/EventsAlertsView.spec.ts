import { flushPromises, mount } from "@vue/test-utils";
import { createMemoryHistory, createRouter } from "vue-router";
import { describe, expect, it, vi } from "vitest";
import EventsAlertsView from "./EventsAlertsView.vue";

vi.mock("../stores/overview", () => ({ useOverviewStore: () => ({ openAlertDetailsModal: vi.fn() }) }));
vi.mock("../api/overview", () => ({
  fetchEvents: vi.fn().mockResolvedValue({ events: [{
    id: "fact", type: "nodeJoined", severity: "info", category: "swarm",
    created_at: "2026-10-10T10:00:00Z", message: "Worker joined",
  }] }),
  fetchAlerts: vi.fn().mockResolvedValue({ alerts: [{
    id: "incident", title: "Node disconnected", resourceType: "node", resourceId: "worker",
    status: "open", occurrences: 1, openedAt: "2026-10-10T11:00:00Z", updatedAt: "2026-10-10T11:00:00Z",
  }] }),
}));

describe("Events & Alerts", () => {
  it("renders separate tables on one page", async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: "/events", component: EventsAlertsView }] });
    await router.push("/events"); await router.isReady();
    const wrapper = mount(EventsAlertsView, { global: { plugins: [router] } });
    await flushPromises();
    expect(wrapper.findAll("table")).toHaveLength(2);
    expect(wrapper.find(".events-table").text()).toContain("Worker joined");
    expect(wrapper.find(".events-table").text()).not.toContain("Node disconnected");
    expect(wrapper.find(".alerts-table").text()).toContain("Node disconnected");
    wrapper.unmount();
  });
});
