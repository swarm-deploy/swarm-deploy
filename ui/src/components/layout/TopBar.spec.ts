import { createPinia, setActivePinia } from "pinia";
import { flushPromises, mount } from "@vue/test-utils";
import { createMemoryHistory, createRouter } from "vue-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { fetchSearch } from "../../api/search";
import { i18n } from "../../i18n";
import TopBar from "./TopBar.vue";

vi.mock("../../api/search", () => ({
  fetchSearch: vi.fn(),
}));

describe("TopBar search", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.useFakeTimers();
    vi.clearAllMocks();
  });

  afterEach(() => {
    i18n.global.locale.value = "en";
    vi.useRealTimers();
  });

  it("shows stack matches and opens the selected stack", async () => {
    vi.mocked(fetchSearch).mockResolvedValue({
      results: [
        {
          kind: "stack",
          match: "stack_name",
          label: "payments",
          stack: "payments",
        },
      ],
    });

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: "/", component: { template: "<div />" } },
        { path: "/stacks/:stack", name: "stack-details", component: { template: "<div />" } },
      ],
    });
    await router.push("/");
    await router.isReady();

    const wrapper = mount(TopBar, {
      props: {
        assistantEnabled: false,
      },
      global: { plugins: [router] },
    });

    const input = wrapper.get<HTMLInputElement>('input[type="search"]');
    expect(input.attributes("placeholder")).toBe("Search services, secrets, and stacks");
    expect(wrapper.text()).not.toContain("Sync now");

    await input.setValue("payments");
    await vi.advanceTimersByTimeAsync(300);
    await flushPromises();

    expect(fetchSearch).toHaveBeenCalledWith("payments");
    expect(wrapper.text()).toContain("Stacks");

    await wrapper.get(".topbar-search-item").trigger("click");
    await flushPromises();

    expect(router.currentRoute.value.path).toBe("/stacks/payments");
    wrapper.unmount();
  });

  it("localizes the visible and accessible assistant labels", async () => {
    i18n.global.locale.value = "ru";
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: "/", component: { template: "<div />" } }],
    });
    await router.push("/");

    const wrapper = mount(TopBar, {
      props: { assistantEnabled: true },
      global: { plugins: [router] },
    });

    const button = wrapper.get(".topbar-assistant-button");
    expect(button.get(".topbar-assistant-label").text()).toBe("Ассистент");
    expect(button.attributes("aria-label")).toBe("Ассистент");
    wrapper.unmount();
  });

  it("expands the mobile search from its icon", async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: "/overview", name: "overview", component: { template: "<div />" } }],
    });
    await router.push("/overview");

    const wrapper = mount(TopBar, {
      attachTo: document.body,
      props: { assistantEnabled: true },
      global: { plugins: [router] },
    });
    const toggle = wrapper.get(".topbar-search-toggle");

    expect(toggle.attributes("aria-expanded")).toBe("false");
    expect(wrapper.get(".topbar-mobile-title").text()).toBe("Overview");
    await toggle.trigger("click");

    expect(toggle.attributes("aria-expanded")).toBe("true");
    expect(wrapper.get(".topbar-search").classes()).toContain("mobile-open");
    expect(document.activeElement).toBe(wrapper.get('input[type="search"]').element);
    wrapper.unmount();
  });
});
