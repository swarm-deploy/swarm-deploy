import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { afterEach, describe, expect, it, vi } from "vitest";
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

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
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

  it("shows API suggestions in an empty chat and submits the exact prompt", async () => {
    const prompt = "API-provided prompt\nwith exact context.";
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({
        suggestions: [
          { id: "from-api", title: "API-provided title", prompt },
          { id: "second", title: "Another API title", prompt: "Second prompt" },
        ],
      }), { status: 200, headers: { "Content-Type": "application/json" } }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: "/", component: { template: "<div />" } }],
    });
    await router.push("/?assistant=new");
    await router.isReady();
    const assistantStore = useAssistantStore();
    const sendMessage = vi.spyOn(assistantStore, "sendMessage").mockImplementation(async (message) => {
      assistantStore.pushMessage("user", message);
    });

    const wrapper = mount(AssistantDrawer, {
      global: { plugins: [pinia, router] },
    });
    await flushPromises();

    expect(fetchMock).toHaveBeenCalledOnce();
    expect(fetchMock).toHaveBeenCalledWith("/api/assistant/suggestions", undefined);
    expect(wrapper.text()).toContain("API-provided title");
    expect(wrapper.text()).toContain("Another API title");
    await wrapper.get(".assistant-suggestion").trigger("click");
    await flushPromises();

    expect(sendMessage).toHaveBeenCalledWith(prompt);
    expect(wrapper.find(".assistant-suggestions").exists()).toBe(false);
    wrapper.unmount();
  });

  it("does not fetch or show suggestions for an existing conversation", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: "/", component: { template: "<div />" } }],
    });
    await router.push("/?assistant=conversation-1");
    await router.isReady();
    const assistantStore = useAssistantStore();
    vi.spyOn(assistantStore, "openChat").mockImplementation(async (conversationID) => {
      assistantStore.conversationID = conversationID;
      assistantStore.messages = [{ role: "user", text: "Existing message" }];
      assistantStore.historyOpen = false;
    });

    const wrapper = mount(AssistantDrawer, {
      global: { plugins: [pinia, router] },
    });
    await flushPromises();

    expect(fetchMock).not.toHaveBeenCalled();
    expect(wrapper.find(".assistant-suggestions").exists()).toBe(false);
    expect(wrapper.text()).toContain("Existing message");
    wrapper.unmount();
  });

  it("keeps normal chat submission working when suggestions fail", async () => {
    const fetchMock = vi.fn().mockRejectedValue(new Error("suggestions unavailable"));
    vi.stubGlobal("fetch", fetchMock);
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: "/", component: { template: "<div />" } }],
    });
    await router.push("/?assistant=new");
    await router.isReady();
    const assistantStore = useAssistantStore();
    const sendMessage = vi.spyOn(assistantStore, "sendMessage").mockImplementation(async (message) => {
      assistantStore.pushMessage("user", message);
    });

    const wrapper = mount(AssistantDrawer, {
      global: { plugins: [pinia, router] },
    });
    await flushPromises();

    expect(wrapper.find(".assistant-suggestions").exists()).toBe(false);
    await wrapper.get("textarea").setValue("Show cluster services");
    await wrapper.get("form").trigger("submit");
    await flushPromises();

    expect(sendMessage).toHaveBeenCalledWith("Show cluster services");
    expect(wrapper.text()).toContain("Show cluster services");
    expect(fetchMock).toHaveBeenCalledOnce();
    wrapper.unmount();
  });
});
