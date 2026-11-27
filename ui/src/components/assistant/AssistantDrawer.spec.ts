import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMemoryHistory, createRouter } from "vue-router";

import * as assistantApi from "../../api/assistant";
import type { AssistantChatHistory, AssistantChatResponse } from "../../api/types";
import { i18n } from "../../i18n";
import { useAssistantStore } from "../../stores/assistant";
import { useOverviewStore } from "../../stores/overview";
import CommitDetailsModal from "../overview/CommitDetailsModal.vue";
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
});

afterEach(() => {
  i18n.global.locale.value = "en";
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

const routes = [
  { path: "/", component: { template: "<div />" } },
  { path: "/overview", name: "overview", component: { template: "<div />" } },
];

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

  it.each([
    { device: "mobile", coarsePointer: true, refocus: false },
    { device: "desktop", coarsePointer: false, refocus: true },
  ])("restores composer focus after a reply as expected on $device", async ({ coarsePointer, refocus }) => {
    const mediaQuery = window.matchMedia("(pointer: coarse)");
    vi.spyOn(window, "matchMedia").mockImplementation((query) => ({
      ...mediaQuery,
      media: query,
      matches: query === "(pointer: coarse)" && coarsePointer,
    }));
    vi.spyOn(assistantApi, "getAssistantSuggestions").mockResolvedValue({ suggestions: [] });
    const pinia = createPinia();
    const router = createRouter({ history: createMemoryHistory(), routes });
    await router.push("/?assistant=new");
    const store = useAssistantStore(pinia);
    let finishReply!: () => void;
    vi.spyOn(store, "sendMessage").mockImplementation(() => new Promise((resolve) => { finishReply = resolve; }));
    const wrapper = mount(AssistantDrawer, {
      attachTo: document.body,
      global: { plugins: [pinia, router] },
    });
    await flushPromises();

    const composer = wrapper.get<HTMLTextAreaElement>(".assistant-chat-form textarea");
    await composer.setValue("Check the cluster");
    await wrapper.get(".assistant-chat-form").trigger("submit");
    composer.element.blur();
    finishReply();
    await flushPromises();

    expect(document.activeElement === composer.element).toBe(refocus);
    wrapper.unmount();
  });

  it("opens Usage inside the drawer and refreshes the chat list on return", async () => {
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
    expect(store.loadChats).toHaveBeenCalledTimes(2);
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

  it("keeps the chat selected while a first message from the list is being accepted", async () => {
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = createRouter({ history: createMemoryHistory(), routes });
    const chat = savedChat();
    vi.spyOn(assistantApi, "getAssistantChat").mockResolvedValue(chat);
    vi.spyOn(assistantApi, "listAssistantChats").mockResolvedValue({ chats: [chat] });
    let accept!: (result: AssistantChatResponse) => void;
    const request = vi.spyOn(assistantApi, "sendAssistantChat").mockImplementationOnce(
      () => new Promise((resolve) => { accept = resolve; }),
    );
    await router.push({ query: { assistant: chat.id } });
    const wrapper = mount(AssistantDrawer, { global: { plugins: [pinia, router] } });
    await flushPromises();
    await wrapper.get('[aria-label="Back to chats"]').trigger("click");
    await flushPromises();
    const form = wrapper.get(".assistant-history-form");
    await form.get("textarea").setValue("Another question");
    await form.trigger("submit");
    await wrapper.get('button[title="Check the cluster"]').trigger("click");
    await flushPromises();

    accept({ status: "in_progress", conversation_id: "new-id", request_id: "new-request" });
    await flushPromises();
    expect(router.currentRoute.value.query.assistant).toBe(chat.id);
    expect(wrapper.get(".assistant-chat-message-user").text()).toBe("Check the cluster");
    expect(wrapper.get<HTMLTextAreaElement>(".assistant-chat-form textarea").element.disabled).toBe(false);
    expect(request).toHaveBeenCalledOnce();
    wrapper.unmount();
  });

  it.each(["/", "/?assistant=chat-1"])("opens an explanation from %s at its saved chat URL", async (path) => {
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = createRouter({ history: createMemoryHistory(), routes });
    await router.push(path);
    const overview = useOverviewStore();
    vi.spyOn(assistantApi, "getAssistantChat").mockResolvedValue(savedChat());
    vi.spyOn(assistantApi, "getAssistantSuggestions").mockResolvedValue({ suggestions: [] });
    vi.spyOn(assistantApi, "listAssistantChats").mockResolvedValue({ chats: [] });
    let finish!: (response: AssistantChatResponse) => void;
    const send = vi.spyOn(assistantApi, "sendAssistantChat")
      .mockResolvedValueOnce({ status: "in_progress", conversation_id: "explanation", request_id: "request", poll_after_ms: 1 })
      .mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
    const wrapper = mount({
      components: { AssistantDrawer, CommitDetailsModal },
      template: "<AssistantDrawer /><CommitDetailsModal />",
    }, { attachTo: document.body, global: { plugins: [pinia, router] } });
    await flushPromises();
    if (path.includes("assistant")) useUIStore().toggleAssistantPinned();
    overview.commitDetailsData = { full_hash: "abc123", author: "Developer", date: "2026-11-21T00:00:00Z", message: "Update", changed_files: [] };
    overview.commitDetailsModalOpen = true;
    await flushPromises();
    await wrapper.get('[aria-label="Explain commit"]').trigger("click");
    await flushPromises();

    expect(router.currentRoute.value.query.assistant).toBe("explanation");
    expect(send.mock.calls[0][0].conversation_id).toBeUndefined();
    expect(wrapper.get(".assistant-chat-message-user").text()).toContain("abc123");
    await vi.waitFor(() => expect(send).toHaveBeenCalledTimes(2));
    finish({ status: "completed", conversation_id: "explanation", request_id: "request", answer: "Explanation" });
    await flushPromises();
    expect(wrapper.get<HTMLTextAreaElement>(".assistant-chat-form textarea").element.disabled).toBe(false);
    wrapper.unmount();
  });

  it.each([false, true])("reconciles a conflicting send when the other request is still running: %s", async (running) => {
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = createRouter({ history: createMemoryHistory(), routes });
    const chat = savedChat();
    chat.messages.push({ role: "assistant", content: "Original answer", sequence: 1 });
    const current = { ...chat, messages: [...chat.messages, { role: "user" as const, content: "Other tab's question", sequence: 2 }],
      active_request: running ? { request_id: "other" } : undefined };
    if (!running) current.messages.push({ role: "assistant", content: "Saved reply", sequence: 3 });
    const get = vi.spyOn(assistantApi, "getAssistantChat").mockResolvedValueOnce(chat).mockResolvedValueOnce(current);
    vi.spyOn(assistantApi, "listAssistantChats").mockResolvedValue({ chats: [chat] });
    const send = vi.spyOn(assistantApi, "sendAssistantChat")
      .mockResolvedValueOnce({ status: "failed", conversation_id: chat.id, request_id: "declined", error_message: "Chat is busy" })
      .mockResolvedValueOnce({ status: "completed", conversation_id: chat.id, request_id: "other", answer: "Saved reply" });
    await router.push({ query: { assistant: chat.id } });
    const wrapper = mount(AssistantDrawer, { global: { plugins: [pinia, router] } });
    await flushPromises();
    const form = wrapper.get(".assistant-chat-form");
    await form.get("textarea").setValue("Declined question");
    await form.trigger("submit");
    await flushPromises();

    expect(get).toHaveBeenCalledTimes(2);
    expect(wrapper.text()).not.toContain("Declined question");
    expect(wrapper.text()).toContain("Chat is busy");
    expect(wrapper.text()).toContain("Other tab's question");
    expect(wrapper.text()).toContain("Saved reply");
    expect(send).toHaveBeenCalledTimes(running ? 2 : 1);
    if (running) expect(send.mock.calls[1][0]).toMatchObject({ request_id: "other", conversation_id: chat.id });
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
      { id: "first", title: "Inspect proxy", created_at: date, updated_at: date, unread: false },
      { id: "second", title: "Check worker nodes", created_at: date, updated_at: date, unread: false },
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
      { id: "first", title: "Inspect proxy", created_at: date, updated_at: date, unread: false },
      { id: "second", title: "Check nodes", created_at: date, updated_at: date, unread: false },
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

  it.each([
    { locale: "en", heading: "How can I help?", newChat: "New chat", title: "API-provided title" },
    { locale: "ru", heading: "Чем помочь?", newChat: "Новый чат", title: "Заголовок из API" },
  ] as const)("displays the API title and sends the exact prompt in $locale", async ({ locale, heading, newChat, title }) => {
    i18n.global.locale.value = locale;
    const prompt = "  API-provided prompt\nwith exact context.  ";
    const fetchMock = vi.fn(async (path: string, _request?: RequestInit) => {
      const payload = path === "/api/assistant/suggestions"
        ? { suggestions: [{ id: "from-api", title, prompt }] }
        : path === "/api/v1/assistant/chat"
          ? { status: "completed", conversation_id: "chat-1", request_id: "turn-1", answer: "Done" }
          : { chats: [] };
      return new Response(JSON.stringify(payload), {
        status: 200, headers: { "Content-Type": "application/json" },
      });
    });
    vi.stubGlobal("fetch", fetchMock);
    const pinia = createPinia();
    const router = createRouter({ history: createMemoryHistory(), routes });
    await router.push("/?assistant=new");

    const wrapper = mount(AssistantDrawer, { global: { plugins: [pinia, router] } });
    await flushPromises();

    expect(fetchMock).toHaveBeenCalledWith("/api/assistant/suggestions", undefined);
    expect(wrapper.get(".assistant-empty-state strong").text()).toBe(heading);
    expect(wrapper.get(".assistant-suggestion").text()).toBe(title);
    await wrapper.get(".assistant-suggestion").trigger("click");
    await flushPromises();

    const request = fetchMock.mock.calls.find(([path]) => path === "/api/v1/assistant/chat")?.[1];
    expect(JSON.parse(request?.body as string).message).toBe(prompt);
    expect(wrapper.find(".assistant-suggestions").exists()).toBe(false);

    await wrapper.get(`[aria-label="${newChat}"]`).trigger("click");
    await flushPromises();

    expect(wrapper.get(".assistant-suggestion").text()).toBe(title);
    wrapper.unmount();
  });

  it("localizes Pinia request errors while preserving the error detail", async () => {
    i18n.global.locale.value = "ru";
    vi.spyOn(assistantApi, "getAssistantSuggestions").mockResolvedValue({ suggestions: [] });
    vi.spyOn(assistantApi, "sendAssistantChat").mockRejectedValue(new Error("upstream unavailable"));
    const { wrapper, store } = await mountDrawer("/?assistant=new");

    await store.sendMessage("Inspect the cluster");
    expect(wrapper.get(".assistant-chat-message-system").text()).toBe("Ошибка ассистента: upstream unavailable");
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

  it("renders persisted response options and submits the exact selected value", async () => {
    const chat = savedChat();
    chat.messages.push({
      role: "assistant",
      content: "Multiple services found. Specify the target.",
      sequence: 1,
      selection: {
        options: [
          {
            label: "infra-prometheus",
            description: "infra-monitoring",
            value: "infra-monitoring/infra-prometheus",
          },
          {
            label: "infra-prometheus-pushgateway",
            description: "infra-monitoring",
            value: "infra-monitoring/infra-prometheus-pushgateway",
          },
        ],
      },
    });
    vi.spyOn(assistantApi, "getAssistantChat").mockResolvedValue(chat);
    vi.spyOn(assistantApi, "listAssistantChats").mockResolvedValue({ chats: [chat] });
    const send = vi.spyOn(assistantApi, "sendAssistantChat").mockResolvedValue({
      status: "completed",
      conversation_id: chat.id,
      request_id: "selection-request",
      answer: "Restart infra-prometheus-pushgateway?",
      answer_sequence: 3,
    });

    const { wrapper } = await mountDrawer(`/?assistant=${chat.id}`);
    const options = wrapper.findAll(".assistant-selection-option");
    expect(options).toHaveLength(2);
    expect(options[0].text()).toContain("infra-prometheus");
    expect(options[0].text()).toContain("infra-monitoring");

    await options[1].trigger("click");
    await flushPromises();

    expect(send).toHaveBeenCalledWith(expect.objectContaining({
      conversation_id: chat.id,
      message: "infra-monitoring/infra-prometheus-pushgateway",
    }));
    expect(wrapper.findAll(".assistant-chat-message-user").at(-1)?.text()).toBe(
      "infra-monitoring/infra-prometheus-pushgateway",
    );
    const renderedOptions = wrapper.findAll(".assistant-selection-option");
    expect(renderedOptions.every((option) => option.attributes("disabled") !== undefined)).toBe(true);
    expect(renderedOptions[1].classes()).toContain("selected");
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
      messages: [{ role: "user" as const, content: "Check the cluster", sequence: 0 }],
    },
    {
      name: "a question followed by another exchange",
      messages: [
        { role: "user" as const, content: "Check the cluster", sequence: 0 },
        { role: "user" as const, content: "List services", sequence: 1 },
        { role: "assistant" as const, content: "api, worker", sequence: 2 },
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
        messages: [{ role: "user", content: "Question B", sequence: 0 }, { role: "assistant", content: "Answer B", sequence: 1 }],
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
      messages: [{ role: "user", content: "Question A", sequence: 0 }],
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

  it("sends to the cached chat when returning to it during another history load", async () => {
    const pinia = createPinia();
    setActivePinia(pinia);
    const store = useAssistantStore();
    const router = createRouter({ history: createMemoryHistory(), routes });
    const first = savedChat();
    const second = { ...first, id: "chat-2", title: "Second chat" };
    store.loadChats = vi.fn(async () => { store.chats = [first, second]; });
    let loadSecond!: (chat: AssistantChatHistory) => void;
    vi.spyOn(assistantApi, "getAssistantChat").mockResolvedValueOnce(first)
      .mockImplementationOnce(() => new Promise((resolve) => { loadSecond = resolve; }));
    const request = vi.spyOn(store, "requestAssistant").mockResolvedValue({
      status: "completed", conversation_id: first.id, request_id: "request", answer: "Healthy",
    });
    await router.push({ query: { assistant: first.id } });
    const wrapper = mount(AssistantDrawer, { global: { plugins: [pinia, router] } });
    await flushPromises();
    await wrapper.get('[aria-label="Back to chats"]').trigger("click");
    await flushPromises();
    await wrapper.get('button[title="Second chat"]').trigger("click");
    await flushPromises();
    await wrapper.get('button[title="Check the cluster"]').trigger("click");
    await flushPromises();

    loadSecond(second);
    await flushPromises();
    expect(router.currentRoute.value.query.assistant).toBe(first.id);
    expect(store.conversationID).toBe(first.id);
    const form = wrapper.get(".assistant-chat-form");
    await form.get("textarea").setValue("Continue");
    await form.trigger("submit");
    expect(request).toHaveBeenCalledWith(expect.objectContaining({ conversation_id: first.id, message: "Continue" }));
    await flushPromises();
    wrapper.unmount();
  });

  it("enables the composer for a direct link only after that chat loads", async () => {
    const pinia = createPinia();
    setActivePinia(pinia);
    const store = useAssistantStore();
    const router = createRouter({ history: createMemoryHistory(), routes });
    const chat = savedChat();
    let finishLoad!: (result: AssistantChatHistory) => void;
    vi.spyOn(assistantApi, "getAssistantChat").mockImplementationOnce(
      () => new Promise((resolve) => { finishLoad = resolve; }),
    );
    store.loadChats = vi.fn(async () => undefined);
    const request = vi.spyOn(store, "requestAssistant").mockResolvedValue({
      status: "completed", conversation_id: chat.id, request_id: "request", answer: "Healthy",
    });
    await router.push({ query: { assistant: chat.id } });
    const wrapper = mount(AssistantDrawer, { global: { plugins: [pinia, router] } });
    const form = wrapper.get(".assistant-chat-form");
    const input = form.get("textarea");
    expect(input.element.disabled).toBe(true);
    await input.setValue("Continue");
    await form.trigger("submit");
    expect(request).not.toHaveBeenCalled();

    finishLoad(chat);
    await flushPromises();
    expect(input.element.disabled).toBe(false);
    await input.setValue("Continue");
    await form.trigger("submit");
    expect(request).toHaveBeenCalledWith(expect.objectContaining({ conversation_id: chat.id, message: "Continue" }));
    await flushPromises();
    wrapper.unmount();
  });

  it("starts a separate chat when Back returns to new during a running reply", async () => {
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = createRouter({ history: createMemoryHistory(), routes });
    const store = useAssistantStore();
    const chat = savedChat({ request_id: "running" });
    store.loadChats = vi.fn(async () => { store.chats = [chat]; });
    vi.spyOn(assistantApi, "getAssistantSuggestions").mockResolvedValue({ suggestions: [] });
    vi.spyOn(assistantApi, "getAssistantChat").mockResolvedValue(chat);
    let finish!: (response: AssistantChatResponse) => void;
    const request = vi.spyOn(store, "requestAssistant")
      .mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }))
      .mockResolvedValueOnce({ status: "completed", conversation_id: "separate", request_id: "second", answer: "New answer" });
    await router.push("/?assistant=new");
    const wrapper = mount(AssistantDrawer, { global: { plugins: [pinia, router] } });
    await flushPromises();
    await wrapper.get('[aria-label="Back to chats"]').trigger("click");
    await flushPromises();
    await wrapper.get('button[title="Check the cluster"]').trigger("click");
    await flushPromises();
    expect(store.pending).toBe(true);

    router.go(-2);
    await vi.waitFor(() => expect(router.currentRoute.value.query.assistant).toBe("new"));
    expect(store.pending).toBe(false);
    const form = wrapper.get(".assistant-chat-form");
    await form.get("textarea").setValue("New question");
    await form.trigger("submit");
    await flushPromises();
    expect(request.mock.calls[1][0].conversation_id).toBeUndefined();
    finish({ status: "completed", conversation_id: chat.id, request_id: "running", answer: "Old answer" });
    await flushPromises();
    expect(router.currentRoute.value.query.assistant).toBe("separate");
    expect(wrapper.get(".assistant-chat-message-assistant").text()).toContain("New answer");
    expect(wrapper.text()).not.toContain("Old answer");
    wrapper.unmount();
  });

  it.each(["completed", "running"])("reloads a %s reply after polling lost its connection", async (status) => {
    const pinia = createPinia();
    setActivePinia(pinia);
    const router = createRouter({ history: createMemoryHistory(), routes });
    const store = useAssistantStore();
    const running = savedChat({ request_id: "running" });
    const completed = savedChat();
    completed.messages.push({ role: "assistant", content: "Recovered answer", sequence: 1 });
    store.loadChats = vi.fn(async () => { store.chats = [running]; });
    let reload!: (chat: AssistantChatHistory) => void;
    const get = vi.spyOn(assistantApi, "getAssistantChat").mockResolvedValueOnce(running)
      .mockImplementationOnce(() => new Promise((resolve) => { reload = resolve; }));
    const request = vi.spyOn(store, "requestAssistant").mockRejectedValueOnce(new TypeError("Connection lost"))
      .mockResolvedValueOnce({ status: "completed", conversation_id: running.id, request_id: "running", answer: "Recovered answer" });
    await router.push({ query: { assistant: running.id } });
    const wrapper = mount(AssistantDrawer, { global: { plugins: [pinia, router] } });
    await flushPromises();
    expect(wrapper.text()).toContain("Connection lost");

    await wrapper.get('[aria-label="Back to chats"]').trigger("click");
    await flushPromises();
    await wrapper.get('button[title="Check the cluster"]').trigger("click");
    await flushPromises();
    expect(get).toHaveBeenCalledTimes(2);
    expect(wrapper.get("textarea").element.readOnly).toBe(true);
    reload(status === "running" ? running : completed);
    await flushPromises();
    expect(request).toHaveBeenCalledTimes(status === "running" ? 2 : 1);
    expect(wrapper.get(".assistant-chat-message-assistant").text()).toContain("Recovered answer");
    expect(wrapper.text()).not.toContain("Connection lost");
    wrapper.unmount();
  });
});

function savedChat(activeRequest?: AssistantChatHistory["active_request"]): AssistantChatHistory {
  return {
    id: "chat-1",
    unread: false,
    title: "Check the cluster",
    created_at: "2026-11-21T10:00:00Z",
    updated_at: "2026-11-21T10:00:00Z",
    messages: [{ role: "user", content: "Check the cluster", sequence: 0 }],
    active_request: activeRequest,
  };
}

describe("Assistant artifacts", () => {
  it("keeps chat and artifact links independent and restores message artifacts from history", async () => {
    const branch = "prod-api-enable-self-heal-with-a-very-long-descriptive-branch-name";
    const chat: AssistantChatHistory = {
      ...savedChat(),
      primary_artifact: { type: "pull_request", label: "PR #42", url: "https://example.com/pull/42" },
      messages: [
        { role: "user", content: "Enable self-heal", sequence: 0 },
        {
          role: "assistant",
          content: "Published the proposal.",
          sequence: 1,
          activity: ["Published Git branch"],
          artifacts: [
            { type: "pull_request", label: "PR #42", url: "https://example.com/pull/42" },
            { type: "git_branch", label: branch },
          ],
        },
      ],
    };
    const listChats = vi.spyOn(assistantApi, "listAssistantChats").mockResolvedValue({ chats: [chat] });
    const getChat = vi.spyOn(assistantApi, "getAssistantChat").mockResolvedValue(chat);
    const clipboard = { writeText: vi.fn().mockResolvedValue(undefined) };
    vi.stubGlobal("navigator", { clipboard });
    vi.stubGlobal("isSecureContext", true);

    const { wrapper, router } = await mountDrawer("/?assistant=chats");
    const primaryLink = wrapper.get<HTMLAnchorElement>('.assistant-history-item .assistant-artifact-label');
    expect(primaryLink.text()).toBe("PR #42");
    expect(primaryLink.attributes("href")).toBe("https://example.com/pull/42");
    expect(primaryLink.attributes("target")).toBe("_blank");
    await primaryLink.trigger("click");
    expect(router.currentRoute.value.query.assistant).toBe("chats");
    expect(getChat).not.toHaveBeenCalled();

    await wrapper.get(".assistant-history-open").trigger("click");
    await flushPromises();
    expect(router.currentRoute.value.query.assistant).toBe(chat.id);
    expect(getChat).toHaveBeenCalledOnce();
    expect(wrapper.findAll(".assistant-chat-message-assistant .assistant-artifact")).toHaveLength(2);
    expect(wrapper.get(".assistant-chat-message-assistant .assistant-artifact-git-branch").text()).toContain(branch);
    expect(wrapper.get(".assistant-activity-details").attributes()).not.toHaveProperty("open");

    await wrapper.get('.assistant-artifact-git-branch button[aria-label="Copy branch name"]').trigger("click");
    expect(clipboard.writeText).toHaveBeenCalledExactlyOnceWith(branch);
    expect(listChats).toHaveBeenCalled();
    wrapper.unmount();
  });
});

describe("Assistant unread replies", () => {
  it("acknowledges the latest reply only when visible in the foreground and refreshes the list", async () => {
    const observers = mockReplyObserver();
    const visibility = vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
    const chat = { ...savedChat(), unread: true };
    chat.messages.push({ role: "assistant", content: "Saved reply", sequence: 1 });
    chat.messages.push({ role: "user", content: "Follow-up question", sequence: 2 });
    const getChat = vi.spyOn(assistantApi, "getAssistantChat").mockImplementation(async () => chat);
    const listChats = vi.spyOn(assistantApi, "listAssistantChats").mockImplementation(async () => ({ chats: [{ ...chat }] }));
    let finishRead!: () => void;
    const markRead = vi.spyOn(assistantApi, "markAssistantChatRead").mockImplementation(() =>
      new Promise<void>((resolve) => { finishRead = resolve; }));
    const { wrapper, router } = await mountDrawer("/?assistant=chats");
    expect(wrapper.get(".assistant-history-title").classes()).toContain("unread");
    expect(wrapper.find(".assistant-history-unread-dot").exists()).toBe(true);
    expect(wrapper.find(".assistant-history-stats").exists()).toBe(true);

    await wrapper.get(".assistant-history-open").trigger("click");
    await flushPromises();
    const background = observers.at(-1)!;
    expect(background.root).toBe(wrapper.get(".assistant-drawer-body").element);
    expect(background.target.getAttribute("data-reply-sequence")).toBe("1");
    background.emit(1);
    expect(markRead).not.toHaveBeenCalled();

    visibility.mockReturnValue("visible");
    document.dispatchEvent(new Event("visibilitychange"));
    await flushPromises();
    const foreground = observers.at(-1)!;
    expect(foreground).not.toBe(background);
    foreground.emit(0);
    expect(markRead).not.toHaveBeenCalled();
    foreground.emit(0.01);
    foreground.emit(0.5);
    expect(markRead).toHaveBeenCalledExactlyOnceWith(chat.id, 1);

    // A list GET that took its snapshot before the read update must not restore the dot.
    let resolveStaleList!: (result: { chats: AssistantChatHistory[] }) => void;
    listChats.mockImplementationOnce(() => new Promise((resolve) => { resolveStaleList = resolve; }));
    await router.push("/?assistant=chats");
    await flushPromises();
    chat.unread = false;
    finishRead();
    await flushPromises();
    expect(wrapper.find(".assistant-history-unread-dot").exists()).toBe(false);
    expect(wrapper.get(".assistant-history-title").classes()).not.toContain("unread");
    resolveStaleList({ chats: [{ ...chat, unread: true }] });
    await flushPromises();
    expect(wrapper.find(".assistant-history-unread-dot").exists()).toBe(false);

    await wrapper.get(".assistant-history-open").trigger("click");
    await flushPromises();
    observers.at(-1)!.emit(1);
    expect(markRead).toHaveBeenCalledTimes(1);
    expect(getChat).toHaveBeenCalledOnce();

    // A newer reply can be generated elsewhere while the drawer is on the list.
    chat.unread = true;
    chat.messages.push({ role: "assistant", content: "Newer saved reply", sequence: 3 });
    await router.push("/?assistant=chats");
    await flushPromises();
    expect(wrapper.find(".assistant-history-unread-dot").exists()).toBe(true);
    await wrapper.get(".assistant-history-open").trigger("click");
    await flushPromises();
    expect(getChat).toHaveBeenCalledTimes(2);
    expect(observers.at(-1)!.target.getAttribute("data-reply-sequence")).toBe("3");
    wrapper.unmount();
  });

  it.each([
    { position: "bottom", scrollTop: 400, newScrollTop: 700, button: false },
    { position: "older messages", scrollTop: 120, newScrollTop: 120, button: true },
  ])("handles a new reply while reading $position", async ({ scrollTop, newScrollTop, button }) => {
    const observers = mockReplyObserver();
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    const chat = savedChat();
    vi.spyOn(assistantApi, "getAssistantChat").mockResolvedValue(chat);
    vi.spyOn(assistantApi, "listAssistantChats").mockResolvedValue({ chats: [] });
    const markRead = vi.spyOn(assistantApi, "markAssistantChatRead").mockResolvedValue(undefined);
    const { wrapper, store } = await mountDrawer(`/?assistant=${chat.id}`);
    const body = wrapper.get<HTMLElement>(".assistant-drawer-body").element;
    let top = scrollTop;
    Object.defineProperties(body, {
      clientHeight: { value: 200 },
      scrollHeight: { get: () => body.querySelector("[data-reply-sequence]") ? 900 : 600 },
      scrollTop: {
        get: () => top,
        set: (value: number) => { top = Math.min(value, body.scrollHeight - body.clientHeight); },
      },
    });

    await store.finishAssistantMessage({
      status: "completed", conversation_id: chat.id, request_id: "request", answer: "New answer", answer_sequence: 1,
    });
    await flushPromises();
    expect(body.scrollTop).toBe(newScrollTop);
    expect(wrapper.find(".assistant-new-reply").exists()).toBe(button);
    const reply = observers.at(-1)!;
    expect(reply.target.textContent).toContain("New answer");

    if (button) {
      reply.emit(0);
      expect(markRead).not.toHaveBeenCalled();
      expect(wrapper.get(".assistant-new-reply").text()).toBe("New reply");
      vi.spyOn(reply.target, "getBoundingClientRect").mockReturnValue(new DOMRect(0, 580, 100, 200));
      await wrapper.get(".assistant-new-reply").trigger("click");
      expect(body.scrollTop).toBe(700);
      expect(markRead).not.toHaveBeenCalled();
    }

    reply.emit(0.1);
    await flushPromises();
    expect(wrapper.find(".assistant-new-reply").exists()).toBe(false);
    expect(markRead).toHaveBeenCalledExactlyOnceWith(chat.id, 1);
    wrapper.unmount();
  });

  it("clears the button on manual scroll or chat switch and ignores an observer from the previous chat", async () => {
    const observers = mockReplyObserver();
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    const chat = savedChat();
    const other = { ...savedChat(), id: "other", unread: true };
    other.messages.push({ role: "assistant", content: "Other chat reply", sequence: 1 });
    const getChat = vi.spyOn(assistantApi, "getAssistantChat");
    getChat.mockResolvedValueOnce(chat).mockResolvedValueOnce(other);
    vi.spyOn(assistantApi, "listAssistantChats").mockResolvedValue({ chats: [] });
    let finishRead!: () => void;
    const markRead = vi.spyOn(assistantApi, "markAssistantChatRead").mockImplementationOnce(() =>
      new Promise<void>((resolve) => { finishRead = resolve; })).mockResolvedValue(undefined);
    const { wrapper, router, store } = await mountDrawer(`/?assistant=${chat.id}`);
    const body = wrapper.get<HTMLElement>(".assistant-drawer-body").element;
    Object.defineProperties(body, {
      clientHeight: { value: 200 },
      scrollHeight: { value: 800 },
    });
    body.scrollTop = 80;

    for (const sequence of [1, 2]) {
      await store.finishAssistantMessage({
        status: "completed", conversation_id: chat.id, request_id: String(sequence), answer: "Reply", answer_sequence: sequence,
      });
      await flushPromises();
      expect(wrapper.find(".assistant-new-reply").exists()).toBe(true);
      if (sequence === 1) {
        observers.at(-1)!.emit(0.1);
        await flushPromises();
        expect(wrapper.find(".assistant-new-reply").exists()).toBe(false);
      }
    }

    const previous = observers.at(-1)!;
    await router.push("/?assistant=other");
    await flushPromises();
    expect(wrapper.find(".assistant-new-reply").exists()).toBe(false);
    previous.emit(1);
    finishRead();
    await flushPromises();
    expect(store.conversationID).toBe(other.id);
    expect(store.unreadReplySequence).toBe(1);
    expect(markRead).toHaveBeenCalledExactlyOnceWith(chat.id, 1);
    observers.at(-1)!.emit(1);
    expect(markRead).toHaveBeenLastCalledWith(other.id, 1);
    wrapper.unmount();
  });
});

describe("Assistant reply sharing", () => {
  const copyButton = 'button[aria-label="Copy reply text"]';
  const shareButton = 'button[aria-label="Share reply"]';
  const highlightedReply = ".assistant-chat-message-highlighted";
  const markdown = "**Saved reply**\n\n- Check the cluster";

  function chatWithReplies() {
    const chat = savedChat();
    chat.messages.push(
      { role: "assistant", content: markdown, sequence: 1, activity: ["Tool output"],
        artifacts: [{ type: "git_branch", label: "branch-name" }] },
      { role: "user", content: "Next question", sequence: 2 },
      { role: "assistant", content: "Another answer", sequence: 3 },
    );
    return chat;
  }

  function mockScroll() {
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
      const body = this.closest(".assistant-drawer-body");
      const sequence = this.dataset.replySequence;
      if (sequence !== undefined && body) {
        return new DOMRect(0, Number(sequence) * 100 - body.scrollTop, 100, 40);
      }
      return new DOMRect(0, 0, 100, 200);
    });
    vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockReturnValue(800);
    vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(200);
  }

  it("shares only the selected raw reply and resolves its URL under the router base while a new reply is pending", async () => {
    const share = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { share });
    const chat = { ...chatWithReplies(), title: "A saved title" };
    vi.spyOn(assistantApi, "getAssistantChat").mockResolvedValue(chat);
    const { wrapper, store } = await mountDrawer(`/?assistant=${chat.id}&tab=events#active`, "/swarm/production");
    store.pushMessage("system", "System message");
    store.pushMessage("user", "A new question");
    store.pending = true;
    await wrapper.vm.$nextTick();

    expect(wrapper.find(".assistant-activity-live").exists()).toBe(true);
    expect(wrapper.findAll(copyButton)).toHaveLength(2);
    expect(wrapper.findAll(shareButton)).toHaveLength(2);
    const actions = wrapper.get(".assistant-reply-actions");
    const copy = actions.get(copyButton).element;
    const shareAction = actions.get(shareButton);
    expect(copy.nextElementSibling).toBe(shareAction.element);
    expect(actions.element.nextElementSibling).toBe(wrapper.get(".assistant-activity-details").element);

    const click = shareAction.trigger("click");
    expect(share).toHaveBeenCalledExactlyOnceWith({
      title: chat.title,
      text: markdown,
      url: `${window.location.origin}/swarm/production/overview?assistant=${chat.id}&reply=1`,
    });
    await click;
    wrapper.unmount();
  });

  it("copies only the reply Markdown when the browser has no Web Share API", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    vi.stubGlobal("isSecureContext", true);
    const chat = chatWithReplies();
    vi.spyOn(assistantApi, "getAssistantChat").mockResolvedValue(chat);
    const { wrapper } = await mountDrawer(`/?assistant=${chat.id}`);

    expect(wrapper.findAll(".assistant-chat-message-assistant")).toHaveLength(2);
    expect(wrapper.find(shareButton).exists()).toBe(false);
    await wrapper.get(copyButton).trigger("click");
    await flushPromises();
    expect(writeText).toHaveBeenCalledExactlyOnceWith(markdown);
    expect(wrapper.get(".assistant-reply-actions").get("button").attributes("aria-label")).toBe("Copied");
    wrapper.unmount();
  });

  it("shows a localized error on the selected reply and leaves no error after cancellation", async () => {
    const share = vi.fn()
      .mockRejectedValueOnce(new DOMException("Blocked", "NotAllowedError"))
      .mockRejectedValueOnce(new DOMException("Dismissed", "AbortError"));
    vi.stubGlobal("navigator", { share });
    const chat = chatWithReplies();
    vi.spyOn(assistantApi, "getAssistantChat").mockResolvedValue(chat);
    const { wrapper } = await mountDrawer(`/?assistant=${chat.id}`);
    const replies = wrapper.findAll(".assistant-chat-message-assistant");

    await replies[0]!.get(shareButton).trigger("click");
    await flushPromises();
    expect(replies[0]!.get('[role="alert"]').text()).toBe("Could not share this reply. Try again.");
    expect(replies[1]!.find('[role="alert"]').exists()).toBe(false);

    await replies[0]!.get(shareButton).trigger("click");
    await flushPromises();
    expect(wrapper.find(".assistant-share-error").exists()).toBe(false);
    wrapper.unmount();
  });

  it("waits for chat history, scrolls to the linked reply and moves between replies without reloading", async () => {
    mockScroll();
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    const chat = chatWithReplies();
    let finishLoad!: (chat: AssistantChatHistory) => void;
    const getChat = vi.spyOn(assistantApi, "getAssistantChat").mockImplementation(() =>
      new Promise((resolve) => { finishLoad = resolve; }));
    const { wrapper, router } = await mountDrawer(`/overview?assistant=${chat.id}&reply=1`);
    const body = wrapper.get<HTMLElement>(".assistant-drawer-body").element;

    expect(useUIStore().assistantDrawerOpen).toBe(true);
    expect(wrapper.find(highlightedReply).exists()).toBe(false);
    finishLoad(chat);
    await flushPromises();
    expect(body.scrollTop).toBe(100);
    expect(wrapper.get(highlightedReply).get("[data-reply-sequence]").attributes("data-reply-sequence")).toBe("1");

    await router.push({ query: { assistant: chat.id, reply: "3" } });
    await flushPromises();
    expect(body.scrollTop).toBe(300);
    expect(wrapper.get(highlightedReply).get("[data-reply-sequence]").attributes("data-reply-sequence")).toBe("3");
    expect(getChat).toHaveBeenCalledOnce();

    await vi.advanceTimersByTimeAsync(2000);
    expect(wrapper.find(highlightedReply).exists()).toBe(false);
    expect(body.scrollTop).toBe(300);
    wrapper.unmount();
  });

  it.each(["", "&reply=abc", "&reply", "&reply=1.5", "&reply=-1", "&reply=9007199254740992",
    "&reply=1&reply=3", "&reply=2", "&reply=99"])("opens the chat normally for query suffix %j", async (suffix) => {
    mockScroll();
    const chat = chatWithReplies();
    vi.spyOn(assistantApi, "getAssistantChat").mockResolvedValue(chat);
    const { wrapper } = await mountDrawer(`/overview?assistant=${chat.id}${suffix}`);

    expect(useUIStore().assistantDrawerOpen).toBe(true);
    expect(wrapper.text()).toContain("Another answer");
    expect(wrapper.find(highlightedReply).exists()).toBe(false);
    expect(wrapper.get<HTMLElement>(".assistant-drawer-body").element.scrollTop).toBe(800);
    wrapper.unmount();
  });

  it.each([
    { button: "Back to chats", assistant: "chats" },
    { button: "New chat", assistant: "new" },
    { button: "Close assistant", assistant: undefined },
  ])("clears reply on $button and keeps other route state", async ({ button, assistant }) => {
    const chat = chatWithReplies();
    const other = { ...savedChat(), id: "other", title: "Other chat" };
    vi.spyOn(assistantApi, "getAssistantChat").mockImplementation(async (id) => id === chat.id ? chat : other);
    vi.spyOn(assistantApi, "listAssistantChats").mockResolvedValue({ chats: [other] });
    vi.spyOn(assistantApi, "getAssistantSuggestions").mockResolvedValue({ suggestions: [] });
    const { wrapper, router } = await mountDrawer(`/overview?assistant=${chat.id}&reply=1&stack=demo#active`);

    await wrapper.get(`[aria-label="${button}"]`).trigger("click");
    await flushPromises();
    expect(router.currentRoute.value.query).toEqual(assistant ? { assistant, stack: "demo" } : { stack: "demo" });
    expect(router.currentRoute.value.hash).toBe("#active");
    expect(wrapper.find(highlightedReply).exists()).toBe(false);

    if (assistant === "chats") {
      await wrapper.get('button[title="Other chat"]').trigger("click");
      await flushPromises();
      expect(router.currentRoute.value.query).toEqual({ assistant: other.id, stack: "demo" });
    }
    wrapper.unmount();
  });
});

async function mountDrawer(path: string, base?: string) {
  const pinia = createPinia();
  const router = createRouter({ history: createMemoryHistory(base), routes });
  await router.push(path);
  const wrapper = mount(AssistantDrawer, { global: { plugins: [pinia, router] } });
  await flushPromises();
  return { wrapper, router, store: useAssistantStore(pinia) };
}

function mockReplyObserver() {
  const observers: Array<{ root: IntersectionObserverInit["root"]; target: Element; emit: (ratio: number) => void }> = [];
  vi.stubGlobal("IntersectionObserver", class {
    constructor(
      readonly callback: IntersectionObserverCallback,
      readonly options: IntersectionObserverInit,
    ) {}

    observe(target: Element) {
      observers.push({
        root: this.options.root,
        target,
        emit: (ratio) => this.callback([{
          target, isIntersecting: ratio > 0, intersectionRatio: ratio,
        } as IntersectionObserverEntry], this as unknown as IntersectionObserver),
      });
    }

    disconnect() {}
  });
  return observers;
}
