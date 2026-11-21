import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMemoryHistory, createRouter } from "vue-router";

import * as assistantApi from "../../api/assistant";
import type { AssistantChatHistory, AssistantChatResponse } from "../../api/types";
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
  it.each([
    { device: "mobile", coarsePointer: true, autoFocus: false },
    { device: "desktop", coarsePointer: false, autoFocus: true },
  ])("sets composer focus on opening as expected on $device", async ({ coarsePointer, autoFocus }) => {
    const mediaQuery = window.matchMedia("(pointer: coarse)");
    vi.spyOn(window, "matchMedia").mockImplementation((query) => ({
      ...mediaQuery,
      media: query,
      matches: query === "(pointer: coarse)" && coarsePointer,
    }));
    vi.spyOn(assistantApi, "getAssistantSuggestions").mockResolvedValue({ suggestions: [] });
    const pinia = createPinia();
    const router = createRouter({ history: createMemoryHistory(), routes });
    await router.push("/");
    const wrapper = mount(AssistantDrawer, {
      attachTo: document.body,
      global: { plugins: [pinia, router] },
    });

    await router.push("/?assistant=new");
    await flushPromises();

    const composer = wrapper.get<HTMLTextAreaElement>(".assistant-chat-form textarea").element;
    expect(document.activeElement === composer).toBe(autoFocus);
    wrapper.unmount();
  });

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
    expect(link.element.previousElementSibling?.getAttribute("aria-label")).toBe("Search chats");

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
    assistantStore.conversationID = "pending-chat";
    assistantStore.pushMessage("user", "Check the cluster");
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

    await router.push("/?assistant=pending-chat");
    await flushPromises();

    expect(wrapper.find(".assistant-history").exists()).toBe(false);
    expect(wrapper.text()).toContain("Check the cluster");
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

  it.each(["chats", "new"])("opens a saved conversation from %s before the reply arrives", async (from) => {
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = createRouter({ history: createMemoryHistory(), routes });
    const store = useAssistantStore();
    store.loadChats = vi.fn(async () => undefined);
    vi.spyOn(assistantApi, "getAssistantSuggestions").mockResolvedValue({ suggestions: [] });
    await router.push(`/?assistant=${from}&stack=demo`);

    let finish!: (response: AssistantChatResponse) => void;
    const request = vi.spyOn(store, "requestAssistant")
      .mockResolvedValueOnce({
        status: "in_progress",
        conversation_id: "created",
        request_id: "request-id",
        activity: ["Routing request"],
        poll_after_ms: 1,
      })
      .mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
    const wrapper = mount(AssistantDrawer, { global: { plugins: [pinia, router] } });
    await flushPromises();
    const form = wrapper.get(from === "chats" ? ".assistant-history-form" : ".assistant-chat-form");
    await form.get("textarea").setValue("Check the cluster");
    await form.trigger("submit");
    await flushPromises();

    expect(router.currentRoute.value.query).toEqual({ assistant: "created", stack: "demo" });
    expect(store.pending).toBe(true);
    expect(wrapper.get(".assistant-chat-message-user").text()).toBe("Check the cluster");
    expect(wrapper.text()).toContain("Routing request");
    expect(wrapper.find(".assistant-chat-message-assistant").exists()).toBe(false);
    expect(request.mock.calls[0][0].wait_timeout_ms).toBe(1);

    await vi.waitFor(() => expect(request).toHaveBeenCalledTimes(2));
    expect(request.mock.calls[1][0]).toEqual({
      conversation_id: "created",
      request_id: "request-id",
      wait_timeout_ms: 750,
    });
    finish({ status: "completed", conversation_id: "created", request_id: "request-id", answer: "Healthy" });
    await flushPromises();

    expect(store.pending).toBe(false);
    expect(wrapper.findAll(".assistant-chat-message-user")).toHaveLength(1);
    expect(wrapper.get(".assistant-chat-message-assistant").text()).toContain("Healthy");
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

  it("updates search and the history composer during text composition", async () => {
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: "/", component: { template: "<div />" } }] });
    await router.push("/?assistant=chats");
    await router.isReady();
    const assistantStore = useAssistantStore();
    const date = new Date().toISOString();
    assistantStore.chats = [
      { id: "first", title: "Inspect proxy", created_at: date, updated_at: date },
      { id: "second", title: "Check nodes", created_at: date, updated_at: date },
    ];
    assistantStore.loadChats = vi.fn(async () => undefined);
    vi.spyOn(assistantStore, "requestAssistant");
    const wrapper = mount(AssistantDrawer, { global: { plugins: [pinia, router] } });
    await flushPromises();

    const draft = wrapper.get<HTMLTextAreaElement>(".assistant-history-form textarea");
    await draft.trigger("compositionstart");
    draft.element.value = "Hello";
    await draft.trigger("input", { isComposing: true });
    expect(wrapper.find('.assistant-history-form button[type="submit"]').exists()).toBe(true);
    const enter = new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true });
    draft.element.dispatchEvent(enter);
    expect(enter.defaultPrevented).toBe(false);
    expect(assistantStore.requestAssistant).not.toHaveBeenCalled();

    await wrapper.get('button[aria-label="Search chats"]').trigger("click");
    const search = wrapper.get<HTMLInputElement>('input[type="search"]');
    await search.trigger("compositionstart");
    search.element.value = "proxy";
    await search.trigger("input", { isComposing: true });
    expect(wrapper.findAll(".assistant-history-title").map((item) => item.text())).toEqual(["Inspect proxy"]);
    wrapper.unmount();
  });

  it("does not send the chat composer on Enter and submits with the button", async () => {
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: "/", component: { template: "<div />" } }] });
    await router.push("/?assistant=new");
    await router.isReady();
    const assistantStore = useAssistantStore();
    const sendMessage = vi.spyOn(assistantStore, "sendMessage").mockResolvedValue();
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("unavailable")));
    const wrapper = mount(AssistantDrawer, { global: { plugins: [pinia, router] } });
    await flushPromises();

    const input = wrapper.get<HTMLTextAreaElement>(".assistant-chat-form textarea");
    await input.trigger("compositionstart");
    input.element.value = "Hello";
    await input.trigger("input", { isComposing: true });
    expect(wrapper.find("#assistant-chat-send").exists()).toBe(true);
    await input.trigger("keydown", { key: "Enter", isComposing: true });
    expect(sendMessage).not.toHaveBeenCalled();
    await input.trigger("compositionend");
    const enter = new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true });
    input.element.dispatchEvent(enter);
    expect(enter.defaultPrevented).toBe(false);
    expect(sendMessage).not.toHaveBeenCalled();
    await wrapper.get(".assistant-chat-form").trigger("submit");
    expect(sendMessage).toHaveBeenCalledWith("Hello");
    wrapper.unmount();
  });

  it("shows API suggestions in an empty chat and submits the exact prompt", async () => {
    const prompt = "API-provided prompt\nwith exact context.";
    const fetchMock = vi.fn().mockImplementation(async () =>
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

    await wrapper.get('[aria-label="New chat"]').trigger("click");
    await flushPromises();

    expect(assistantStore.messages).toEqual([]);
    expect(wrapper.text()).toContain("API-provided title");
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

  it("resumes a running reply when the chat is reopened", async () => {
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = createRouter({ history: createMemoryHistory(), routes });
    const store = useAssistantStore();
    store.loadChats = vi.fn(async () => undefined);
    vi.spyOn(assistantApi, "getAssistantChat").mockResolvedValue(savedChat({
      request_id: "request-id",
      activity: ["Routing request"],
    }));
    let finish!: (response: AssistantChatResponse) => void;
    const request = vi.spyOn(store, "requestAssistant")
      .mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
    await router.push("/?assistant=chat-1");

    const wrapper = mount(AssistantDrawer, { global: { plugins: [pinia, router] } });
    await flushPromises();

    expect(store.pending).toBe(true);
    expect(wrapper.text()).toContain("Routing request");
    expect(wrapper.text()).not.toContain("No reply received");
    expect(request).toHaveBeenCalledWith({ conversation_id: "chat-1", request_id: "request-id", wait_timeout_ms: 750 });

    finish({ status: "completed", conversation_id: "chat-1", request_id: "request-id", answer: "Healthy" });
    await flushPromises();

    expect(store.pending).toBe(false);
    expect(wrapper.findAll(".assistant-chat-message-user")).toHaveLength(1);
    expect(wrapper.get(".assistant-chat-message-assistant").text()).toContain("Healthy");
    wrapper.unmount();
  });

  it.each([
    {
      name: "the last question",
      messages: [{ role: "user" as const, content: "Check the cluster" }],
    },
    {
      name: "a question followed by another exchange",
      messages: [
        { role: "user" as const, content: "Check the cluster" },
        { role: "user" as const, content: "List services" },
        { role: "assistant" as const, content: "api, worker" },
      ],
    },
  ])("marks $name that has no reply and no running request", async ({ messages }) => {
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = createRouter({ history: createMemoryHistory(), routes });
    const store = useAssistantStore();
    vi.spyOn(assistantApi, "getAssistantChat").mockResolvedValue({ ...savedChat(), messages });
    const request = vi.spyOn(store, "requestAssistant");
    await router.push("/?assistant=chat-1");

    const wrapper = mount(AssistantDrawer, { global: { plugins: [pinia, router] } });
    await flushPromises();

    expect(store.pending).toBe(false);
    expect(request).not.toHaveBeenCalled();
    const hints = wrapper.findAll(".assistant-chat-message-system");
    expect(hints).toHaveLength(1);
    expect(hints[0].text()).toBe("No reply received. Send your message again.");
    expect(hints[0].element.previousElementSibling?.textContent).toContain("Check the cluster");
    wrapper.unmount();
  });

  it("keeps the selected chat when an earlier chat finishes loading later", async () => {
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = createRouter({ history: createMemoryHistory(), routes });
    const store = useAssistantStore();
    store.loadChats = vi.fn(async () => undefined);
    let loadFirst!: (chat: AssistantChatHistory) => void;
    vi.spyOn(assistantApi, "getAssistantChat").mockImplementation((conversationID) => {
      if (conversationID === "chat-a") {
        return new Promise((resolve) => { loadFirst = resolve; });
      }
      return Promise.resolve({
        ...savedChat(),
        id: "chat-b",
        messages: [{ role: "user", content: "Question B" }, { role: "assistant", content: "Answer B" }],
      });
    });
    const request = vi.spyOn(store, "requestAssistant");
    await router.push("/?assistant=chat-a");

    const wrapper = mount(AssistantDrawer, { global: { plugins: [pinia, router] } });
    await flushPromises();
    await router.push("/?assistant=chat-b");
    await flushPromises();
    loadFirst({
      ...savedChat({ request_id: "request-a", activity: ["Routing request"] }),
      id: "chat-a",
      messages: [{ role: "user", content: "Question A" }],
    });
    await flushPromises();

    expect(router.currentRoute.value.query.assistant).toBe("chat-b");
    expect(store.conversationID).toBe("chat-b");
    expect(store.pending).toBe(false);
    expect(request).not.toHaveBeenCalled();
    expect(wrapper.text()).toContain("Question B");
    expect(wrapper.text()).not.toContain("Question A");
    wrapper.unmount();
  });
});

function savedChat(activeRequest?: AssistantChatHistory["active_request"]): AssistantChatHistory {
  return {
    id: "chat-1",
    title: "Check the cluster",
    created_at: "2026-11-21T10:00:00Z",
    updated_at: "2026-11-21T10:00:00Z",
    messages: [{ role: "user", content: "Check the cluster" }],
    active_request: activeRequest,
  };
}
