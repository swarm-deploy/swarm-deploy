import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMemoryHistory, createRouter } from "vue-router";

import * as assistantApi from "../../api/assistant";
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

const routes = [{ path: "/", component: { template: "<div />" } }];

describe("Assistant Drawer", () => {
  it("opens Usage inside the drawer and returns to the same chat list", async () => {
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = createRouter({ history: createMemoryHistory(), routes });
    vi.spyOn(assistantApi, "getAssistantUsage").mockResolvedValue({ buckets: [] });
    const store = useAssistantStore();
    store.loadChats = vi.fn(async () => undefined);
    await router.push("/?assistant=chats&tab=alerts#active");

    const wrapper = mount(AssistantDrawer, { global: { plugins: [pinia, router] } });
    const link = wrapper.get('[aria-label="Assistant usage"]');
    expect(link.attributes("href")).toBe("/?assistant=usage&tab=alerts#active");
    expect(link.element.nextElementSibling?.getAttribute("aria-label")).toBe("Search chats");

    await link.trigger("click");
    await flushPromises();
    await vi.dynamicImportSettled();
    expect(wrapper.find(".assistant-drawer .assistant-usage").exists()).toBe(true);
    expect(router.currentRoute.value.fullPath).toBe("/?assistant=usage&tab=alerts#active");
    expect(useUIStore().assistantDrawerOpen).toBe(true);
    expect(wrapper.get(".assistant-drawer-header h2").text()).toBe("Usage");
    expect(wrapper.find(".assistant-history").exists()).toBe(false);
    expect(wrapper.find("textarea").exists()).toBe(false);

    await wrapper.get('[aria-label="Back to chats"]').trigger("click");
    await flushPromises();
    expect(router.currentRoute.value.fullPath).toBe("/?assistant=chats&tab=alerts#active");
    expect(wrapper.find(".assistant-history").exists()).toBe(true);
    expect(useUIStore().assistantDrawerOpen).toBe(true);
    expect(store.loadChats).toHaveBeenCalledOnce();
    wrapper.unmount();
  });

  it("opens a direct Usage link without loading a chat and closes the drawer in place", async () => {
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = createRouter({ history: createMemoryHistory(), routes });
    vi.spyOn(assistantApi, "getAssistantUsage").mockResolvedValue({ buckets: [] });
    const store = useAssistantStore();
    store.openChat = vi.fn(async () => undefined);
    store.loadChats = vi.fn(async () => undefined);
    await router.push("/?assistant=usage&tab=alerts#active");

    const wrapper = mount(AssistantDrawer, { global: { plugins: [pinia, router] } });
    await flushPromises();
    await vi.dynamicImportSettled();
    expect(wrapper.find(".assistant-drawer .assistant-usage").exists()).toBe(true);
    expect(useUIStore().assistantDrawerOpen).toBe(true);
    expect(store.openChat).not.toHaveBeenCalled();
    expect(store.loadChats).not.toHaveBeenCalled();

    await wrapper.get('[aria-label="Close assistant"]').trigger("click");
    await flushPromises();
    expect(router.currentRoute.value.fullPath).toBe("/?tab=alerts#active");
    expect(useUIStore().assistantDrawerOpen).toBe(false);
    wrapper.unmount();
  });

  it("closes when it was opened without an assistant route query", async () => {
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = createRouter({
      history: createMemoryHistory(),
      routes,
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
      routes,
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

  it("keeps the history draft and deduplicates a retry after a transport failure", async () => {
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = createRouter({
      history: createMemoryHistory(),
      routes,
    });
    await router.push("/?assistant=chats&stack=demo");
    await router.isReady();

    const assistantStore = useAssistantStore();
    assistantStore.conversationID = "existing";
    assistantStore.loadChats = vi.fn(async () => undefined);
    let failRequest!: (reason: Error) => void;
    const request = vi.spyOn(assistantStore, "requestAssistant")
      .mockImplementationOnce(() => new Promise((_, reject) => { failRequest = reject; }))
      .mockResolvedValueOnce({
        status: "completed",
        conversation_id: "created",
        request_id: "request-id",
        answer: "Assistant answer",
      });

    const wrapper = mount(AssistantDrawer, { global: { plugins: [pinia, router] } });
    await flushPromises();
    const form = wrapper.get(".assistant-history-form");
    const draft = form.get("textarea");
    expect(form.find('button[type="submit"]').exists()).toBe(false);
    await draft.setValue(" \n ");
    expect(form.find('button[type="submit"]').exists()).toBe(false);
    await draft.setValue("First message\nSecond line");
    await form.trigger("submit");
    await form.trigger("submit");

    expect(request).toHaveBeenCalledOnce();
    expect(request.mock.calls[0][0]).toMatchObject({
      message: "First message\nSecond line",
      request_id: expect.any(String),
    });
    expect(request.mock.calls[0][0]).not.toHaveProperty("conversation_id");
    failRequest(new Error("Connection lost"));
    await flushPromises();

    expect(draft.element.value).toBe("First message\nSecond line");
    expect(form.get('[role="alert"]').text()).toContain("Connection lost");
    expect(assistantStore.conversationID).toBe("existing");
    expect(router.currentRoute.value.query.assistant).toBe("chats");

    await form.trigger("submit");
    await flushPromises();

    expect(request.mock.calls[1][0]).toEqual(request.mock.calls[0][0]);
    expect(router.currentRoute.value.query).toEqual({ assistant: "created", stack: "demo" });
    expect(wrapper.text()).toContain("Assistant answer");
    expect(assistantStore.historyOpen).toBe(false);
    wrapper.unmount();
  });

  it("filters loaded history titles and preserves the draft when closing search", async () => {
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = createRouter({
      history: createMemoryHistory(),
      routes,
    });
    await router.push("/?assistant=chats");
    await router.isReady();

    const assistantStore = useAssistantStore();
    const date = new Date().toISOString();
    assistantStore.chats = [
      { id: "first", title: "Inspect proxy", created_at: date, updated_at: date },
      { id: "second", title: "Check worker nodes", created_at: date, updated_at: date },
    ];
    assistantStore.loadChats = vi.fn(async () => undefined);
    const wrapper = mount(AssistantDrawer, { global: { plugins: [pinia, router] } });
    await flushPromises();
    await wrapper.get(".assistant-history-form textarea").setValue("Keep this draft");
    await wrapper.get('button[aria-label="Search chats"]').trigger("click");
    await wrapper.get('input[type="search"]').setValue(" PROXY ");

    expect(wrapper.find(".assistant-history-form").exists()).toBe(false);
    expect(wrapper.findAll(".assistant-history-title").map((item) => item.text())).toEqual(["Inspect proxy"]);
    await wrapper.get('button[aria-label="Clear search"]').trigger("click");
    expect(wrapper.findAll(".assistant-history-title")).toHaveLength(2);
    await wrapper.get('input[type="search"]').trigger("keydown", { key: "Escape" });

    expect(wrapper.find('input[type="search"]').exists()).toBe(false);
    expect(wrapper.get<HTMLTextAreaElement>(".assistant-history-form textarea").element.value).toBe("Keep this draft");
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
      routes,
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
      routes,
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
      routes,
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
    expect(wrapper.find("#assistant-chat-send").exists()).toBe(false);
    await wrapper.get("textarea").setValue("Show cluster services");
    expect(wrapper.find("#assistant-chat-send").exists()).toBe(true);
    await wrapper.get("form").trigger("submit");
    await flushPromises();

    expect(sendMessage).toHaveBeenCalledWith("Show cluster services");
    expect(wrapper.find("#assistant-chat-send").exists()).toBe(false);
    expect(wrapper.text()).toContain("Show cluster services");
    expect(fetchMock).toHaveBeenCalledOnce();
    wrapper.unmount();
  });
});
