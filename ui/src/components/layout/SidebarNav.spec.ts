import { mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { describe, expect, it, vi } from "vitest";
import { createMemoryHistory, createRouter } from "vue-router";

import SidebarNav from "./SidebarNav.vue";

vi.hoisted(() => {
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: vi.fn().mockReturnValue({
      matches: false,
      addEventListener: vi.fn(),
    }),
  });
});

describe("SidebarNav", () => {
  it("closes the user menu when clicking outside it", async () => {
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: "/overview", component: { template: "<div />" } }],
    });
    await router.push("/overview");
    await router.isReady();

    const wrapper = mount(SidebarNav, {
      attachTo: document.body,
      props: { currentUserLabel: "User", collapsed: false },
      global: { plugins: [pinia, router] },
    });
    const userMenu = wrapper.find(".sidebar-user-menu").element as HTMLDetailsElement;
    userMenu.open = true;

    wrapper.find(".sidebar-settings-menu").element.dispatchEvent(new MouseEvent("mousedown", { bubbles: true }));
    expect(userMenu.open).toBe(true);

    document.body.dispatchEvent(new MouseEvent("mousedown", { bubbles: true }));
    expect(userMenu.open).toBe(false);

    wrapper.unmount();
  });
});
