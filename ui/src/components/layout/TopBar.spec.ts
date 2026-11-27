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

  it("opens a service result on the service page", async () => {
    vi.mocked(fetchSearch).mockResolvedValue({
      results: [
        {
          kind: "service",
          match: "service_name",
          label: "api",
          stack: "payments",
          service: "api",
        },
      ],
    });

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: "/", component: { template: "<div />" } },
        { path: "/services/:stack/:service", name: "service-details", component: { template: "<div />" } },
      ],
    });
    await router.push("/");
    await router.isReady();

    const wrapper = mount(TopBar, {
      props: { assistantEnabled: false },
      global: { plugins: [router] },
    });

    await wrapper.get<HTMLInputElement>('input[type="search"]').setValue("api");
    await vi.advanceTimersByTimeAsync(300);
    await flushPromises();
    await wrapper.get(".topbar-search-item").trigger("click");
    await flushPromises();

    expect(router.currentRoute.value.path).toBe("/services/payments/api");
    wrapper.unmount();
  });

  it("opens a secret result on the filtered secrets page", async () => {
    vi.mocked(fetchSearch).mockResolvedValue({
      results: [
        {
          kind: "secret",
          match: "secret_name",
          label: "database-password",
          secret_name: "database-password",
        },
      ],
    });

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: "/", component: { template: "<div />" } },
        { path: "/secrets", name: "secrets", component: { template: "<div />" } },
      ],
    });
    await router.push("/");
    await router.isReady();

    const wrapper = mount(TopBar, {
      props: { assistantEnabled: false },
      global: { plugins: [router] },
    });

    await wrapper.get<HTMLInputElement>('input[type="search"]').setValue("database");
    await vi.advanceTimersByTimeAsync(300);
    await flushPromises();
    await wrapper.get(".topbar-search-item").trigger("click");
    await flushPromises();

    expect(router.currentRoute.value.path).toBe("/secrets");
    expect(router.currentRoute.value.query.search).toBe("database-password");
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
    expect(wrapper.get(".topbar-mobile-title").element.tagName).toBe("H1");
    await toggle.trigger("click");

    expect(toggle.attributes("aria-expanded")).toBe("true");
    expect(wrapper.get(".topbar-search").classes()).toContain("mobile-open");
    expect(document.activeElement).toBe(wrapper.get('input[type="search"]').element);
    wrapper.unmount();
  });
});
