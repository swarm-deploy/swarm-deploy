import { flushPromises, mount } from "@vue/test-utils";
import { createMemoryHistory, createRouter } from "vue-router";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { fetchEvents } from "../api/overview";
import type { EventHistoryItem } from "../api/types";
import EventsView from "./EventsView.vue";

vi.mock("../api/overview", () => ({
  fetchEvents: vi.fn(),
}));

const events: EventHistoryItem[] = [
  {
    id: "older-info",
    type: "syncManualStarted",
    severity: "info",
    category: "sync",
    created_at: "2026-10-06T16:00:00Z",
    message: "Older info",
  },
  {
    id: "newer-info",
    type: "deploySuccess",
    severity: "info",
    category: "sync",
    created_at: "2026-10-06T18:00:00Z",
    message: "Newer info",
  },
  {
    id: "warning",
    type: "serviceMissed",
    severity: "warn",
    category: "swarm",
    created_at: "2026-10-06T17:00:00Z",
    message: "Warning",
  },
  {
    id: "error",
    type: "deployFailed",
    severity: "error",
    category: "sync",
    created_at: "2026-10-06T15:00:00Z",
    message: "Error",
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
    vi.mocked(fetchEvents).mockResolvedValue({ events });
  });

  it("restores multiple event types from the URL and keeps them selected", async () => {
    const { wrapper } = await mountView("/events?types=deploySuccess&types=syncManualStarted");

    const checkedTypes = wrapper
      .findAll(".events-type-filter-option input")
      .filter((input) => (input.element as HTMLInputElement).checked)
      .map((input) => input.element.getAttribute("value") ?? input.element.parentElement?.textContent?.trim());

    expect(checkedTypes).toHaveLength(2);
    expect(wrapper.find(".events-type-filter-selection").text()).toContain("deploySuccess");
    expect(wrapper.find(".events-type-filter-selection").text()).toContain("syncManualStarted");
    expect(fetchEvents).toHaveBeenLastCalledWith({
      types: ["deploySuccess", "syncManualStarted"],
      severities: [],
    });
  });

  it("updates repeated type query parameters from the multi-select", async () => {
    const { wrapper, router } = await mountView();

    const option = wrapper
      .findAll(".events-type-filter-option")
      .find((item) => item.text() === "deploySuccess");
    expect(option).toBeDefined();

    await option!.find("input").setValue(true);
    await flushPromises();

    expect(router.currentRoute.value.query.types).toEqual(["deploySuccess"]);

    const secondOption = wrapper
      .findAll(".events-type-filter-option")
      .find((item) => item.text() === "syncManualStarted");
    await secondOption!.find("input").setValue(true);
    await flushPromises();

    expect(router.currentRoute.value.query.types).toEqual(["deploySuccess", "syncManualStarted"]);
  });


  it("offers webhookReceived in the type filter", async () => {
    const { wrapper } = await mountView();
    const option = wrapper.findAll(".events-type-filter-option").find((item) => item.text() === "webhookReceived");
    expect(option).toBeDefined();
    await option!.find("input").setValue(true);
    await flushPromises();
    expect(fetchEvents).toHaveBeenLastCalledWith({
      types: ["webhookReceived"],
      severities: [],
    });
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

    expect(renderedMessages(wrapper)).toEqual(["Error", "Older info", "Warning", "Newer info"]);
    expect(timeButton!.element.closest("th")?.getAttribute("aria-sort")).toBe("ascending");
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

  it("restores sorting from the URL", async () => {
    const { wrapper } = await mountView("/events?sort=severity&order=asc");

    expect(renderedMessages(wrapper)).toEqual(["Newer info", "Older info", "Warning", "Error"]);
    const severityHeader = wrapper.findAll("th").find((header) => header.text().startsWith("Severity"));
    expect(severityHeader?.attributes("aria-sort")).toBe("ascending");
  });
});
