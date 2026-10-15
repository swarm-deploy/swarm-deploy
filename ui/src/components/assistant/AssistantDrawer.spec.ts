import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { describe, expect, it, vi } from "vitest";
import { createMemoryHistory, createRouter } from "vue-router";

import { useAssistantStore } from "../../stores/assistant";
import { useUIStore } from "../../stores/ui";
import AssistantDrawer from "./AssistantDrawer.vue";

vi.hoisted(() => {
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: vi.fn().mockReturnValue({
      matches: false,
      addEventListener: vi.fn(),
    }),
  });
  Object.defineProperty(HTMLElement.prototype, "scrollTo", {
    configurable: true,
    value: vi.fn(),
  });
});

describe("Assistant Drawer", () => {
  it("closes when it was opened without an assistant route query", async () => {
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: "/", component: { template: "<div />" } }],
    });
    await router.push("/");
    await router.isReady();

    const wrapper = mount(AssistantDrawer, {
      global: { plugins: [pinia, router] },
    });
    const uiStore = useUIStore();
    uiStore.openAssistantDrawer();
    await wrapper.vm.$nextTick();

    await wrapper.get('[aria-label="Close assistant"]').trigger("click");
    await flushPromises();

    expect(uiStore.assistantDrawerOpen).toBe(false);
    expect(router.currentRoute.value.query.assistant).toBeUndefined();
    wrapper.unmount();
  });

  it("opens the chat list while an assistant request is pending", async () => {
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: "/", component: { template: "<div />" } }],
    });
    await router.push("/");
    await router.isReady();

    const wrapper = mount(AssistantDrawer, {
      global: { plugins: [pinia, router] },
    });
    const assistantStore = useAssistantStore();
    const uiStore = useUIStore();
    assistantStore.pending = true;
    assistantStore.loadChats = vi.fn(async () => undefined);
    uiStore.openAssistantDrawer();
    await wrapper.vm.$nextTick();

    const backButton = wrapper.get<HTMLButtonElement>('[aria-label="Back to chats"]');
    expect(backButton.element.disabled).toBe(false);
    await backButton.trigger("click");
    await flushPromises();

    expect(router.currentRoute.value.query.assistant).toBe("chats");
    expect(assistantStore.historyOpen).toBe(true);
    expect(assistantStore.loadChats).toHaveBeenCalledOnce();
    wrapper.unmount();
  });
});
