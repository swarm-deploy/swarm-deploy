import { createPinia, setActivePinia } from "pinia";
import { flushPromises, mount } from "@vue/test-utils";
import { createMemoryHistory, createRouter } from "vue-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { fetchSearch } from "../../api/search";
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
        syncDisabled: false,
        syncPending: false,
        assistantEnabled: false,
      },
      global: { plugins: [router] },
    });

    const input = wrapper.get<HTMLInputElement>('input[type="search"]');
    expect(input.attributes("placeholder")).toBe("Search services, secrets, and stacks");

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
});
