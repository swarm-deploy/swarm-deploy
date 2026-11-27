import { createPinia, setActivePinia } from "pinia";
import { flushPromises, shallowMount } from "@vue/test-utils";
import { createMemoryHistory, createRouter } from "vue-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "../../api/client";
import * as overviewAPI from "../../api/overview";
import type { Alert } from "../../api/types";
import { useOverviewStore } from "../../stores/overview";
import { useSecretDetailsStore } from "../../stores/secretDetails";
import AppShell from "./AppShell.vue";

const assistantStore = {
  enabled: false,
  conversationID: "",
};
const currentUserStore = { displayName: "", loadCurrentUser: vi.fn() };
const uiStore = {
  assistantDrawerOpen: false,
  assistantPinned: false,
  sidebarCollapsed: false,
  initializeTheme: vi.fn(),
  toggleSidebar: vi.fn(),
};

vi.mock("../../stores/assistant", () => ({ useAssistantStore: () => assistantStore }));
vi.mock("../../stores/currentUser", () => ({ useCurrentUserStore: () => currentUserStore }));
vi.mock("../../stores/ui", () => ({ useUIStore: () => uiStore }));

function testRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: "/deployments", component: { template: "<div />" } },
      { path: "/events", name: "events", component: { template: "<div />" } },
    ],
  });
}

describe("assistant launcher", () => {
  afterEach(() => {
    assistantStore.enabled = false;
    assistantStore.conversationID = "";
    uiStore.assistantDrawerOpen = false;
  });

  it.each([
    { conversationID: "", destination: "new" },
    { conversationID: "chat-1", destination: "chat-1" },
  ])("opens $destination directly and preserves the page location", async ({ conversationID, destination }) => {
    setActivePinia(createPinia());
    assistantStore.enabled = true;
    assistantStore.conversationID = conversationID;
    const router = testRouter();
    await router.push("/events?tab=alerts#active");
    const push = vi.spyOn(router, "push");
    const wrapper = shallowMount(AppShell, { global: { plugins: [router] } });

    wrapper.getComponent({ name: "TopBar" }).vm.$emit("toggle-assistant");
    await flushPromises();

    expect(router.currentRoute.value.path).toBe("/events");
    expect(router.currentRoute.value.query).toEqual({ tab: "alerts", assistant: destination });
    expect(router.currentRoute.value.hash).toBe("#active");
    expect(push).toHaveBeenCalledOnce();
    wrapper.unmount();
  });

  it("uses the same launcher from mobile navigation", async () => {
    setActivePinia(createPinia());
    assistantStore.enabled = true;
    const router = testRouter();
    await router.push("/events?tab=alerts");
    const wrapper = shallowMount(AppShell, { global: { plugins: [router] } });

    wrapper.getComponent({ name: "MobileNavigation" }).vm.$emit("toggle-assistant");
    await flushPromises();

    expect(router.currentRoute.value.query).toEqual({ tab: "alerts", assistant: "new" });
    wrapper.unmount();
  });

  it("clears the reply and conversation when closing from the launcher", async () => {
    setActivePinia(createPinia());
    assistantStore.enabled = true;
    uiStore.assistantDrawerOpen = true;
    const router = testRouter();
    await router.push("/events?tab=alerts&assistant=chat-1&reply=1#active");
    const wrapper = shallowMount(AppShell, { global: { plugins: [router] } });

    wrapper.getComponent({ name: "TopBar" }).vm.$emit("toggle-assistant");
    await flushPromises();

    expect(router.currentRoute.value.fullPath).toBe("/events?tab=alerts#active");
    wrapper.unmount();
  });
});

describe("global modal navigation", () => {
  beforeEach(() => setActivePinia(createPinia()));

  it("closes every global modal when the page changes", async () => {
    const router = testRouter();
    await router.push("/deployments");
    const overviewStore = useOverviewStore();
    const secretDetailsStore = useSecretDetailsStore();
    overviewStore.serviceStatusModalOpen = true;
    overviewStore.alertDetailsModalOpen = true;
    overviewStore.deploymentDetailsModalOpen = true;
    overviewStore.commitDetailsModalOpen = true;
    overviewStore.stackManifestModalOpen = true;
    secretDetailsStore.modalOpen = true;
    const wrapper = shallowMount(AppShell, { global: { plugins: [router] } });

    await router.push("/events?tab=alerts");
    await flushPromises();

    expect(overviewStore.serviceStatusModalOpen).toBe(false);
    expect(overviewStore.alertDetailsModalOpen).toBe(false);
    expect(overviewStore.deploymentDetailsModalOpen).toBe(false);
    expect(overviewStore.commitDetailsModalOpen).toBe(false);
    expect(overviewStore.stackManifestModalOpen).toBe(false);
    expect(secretDetailsStore.modalOpen).toBe(false);
    wrapper.unmount();
  });

  it("keeps global modals open when only the page query changes", async () => {
    const router = testRouter();
    await router.push("/events?tab=alerts");
    const overviewStore = useOverviewStore();
    const secretDetailsStore = useSecretDetailsStore();
    overviewStore.commitDetailsModalOpen = true;
    secretDetailsStore.modalOpen = true;
    const wrapper = shallowMount(AppShell, { global: { plugins: [router] } });

    await router.push("/events?tab=events");
    await flushPromises();

    expect(overviewStore.commitDetailsModalOpen).toBe(true);
    expect(secretDetailsStore.modalOpen).toBe(true);
    wrapper.unmount();
  });
});

describe("deployment details deep link", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
  });

  it("opens the deployment modal from the deployment_id query and removes it when closed", async () => {
    const router = testRouter();
    await router.push("/deployments?deployment_id=deployment-1&source=notification");
    await router.isReady();
    const store = useOverviewStore();
    const open = vi.spyOn(store, "openDeploymentDetailsModal").mockImplementation(async (deploymentID: string) => {
      store.deploymentDetailsID = deploymentID;
      store.deploymentDetailsModalOpen = true;
    });
    const wrapper = shallowMount(AppShell, { global: { plugins: [router] } });
    await flushPromises();

    expect(open).toHaveBeenCalledWith("deployment-1");
    store.closeDeploymentDetailsModal();
    await flushPromises();

    expect(router.currentRoute.value.query).toEqual({ source: "notification" });
    wrapper.unmount();
  });

  it("closes a linked deployment modal when browser navigation removes the query", async () => {
    const router = testRouter();
    await router.push("/deployments?deployment_id=deployment-2");
    await router.isReady();
    const store = useOverviewStore();
    vi.spyOn(store, "openDeploymentDetailsModal").mockImplementation(async (deploymentID: string) => {
      store.deploymentDetailsID = deploymentID;
      store.deploymentDetailsModalOpen = true;
    });
    const close = vi.spyOn(store, "closeDeploymentDetailsModal");
    const wrapper = shallowMount(AppShell, { global: { plugins: [router] } });
    await flushPromises();

    await router.push("/deployments");
    await flushPromises();

    expect(close).toHaveBeenCalledOnce();
    expect(store.deploymentDetailsModalOpen).toBe(false);
    wrapper.unmount();
  });
});

describe("alert details deep link", () => {
  const alert: Alert = {
    id: "alert-1",
    fingerprint: "node:worker",
    kind: "node_disconnected",
    resourceType: "node",
    resourceId: "worker",
    status: "open",
    title: "Node disconnected",
    message: "Worker is unavailable",
    occurrences: 1,
    openedAt: "2026-10-10T11:00:00Z",
    updatedAt: "2026-10-10T11:00:00Z",
    openEventId: "open-event",
    latestEventId: "latest-event",
  };

  beforeEach(() => setActivePinia(createPinia()));
  afterEach(() => vi.restoreAllMocks());

  it("loads a directly linked alert by ID and preserves the filters on close", async () => {
    const fetchAlert = vi.spyOn(overviewAPI, "fetchAlert").mockResolvedValue(alert);
    const router = testRouter();
    await router.push("/events?tab=alerts&alert_id=alert-1&stack_name=app");
    const store = useOverviewStore();
    const wrapper = shallowMount(AppShell, { global: { plugins: [router] } });
    await flushPromises();

    expect(fetchAlert).toHaveBeenCalledExactlyOnceWith("alert-1");
    expect(store.alertDetailsModalOpen).toBe(true);
    expect(store.alertDetailsAlert).toEqual(alert);
    store.closeAlertDetailsModal();
    await flushPromises();
    expect(router.currentRoute.value.query).toEqual({ tab: "alerts", stack_name: "app" });
    wrapper.unmount();
  });

  it("keeps the current error when an earlier alert lookup completes late", async () => {
    let resolveFirst!: (value: Alert) => void;
    const fetchAlert = vi.spyOn(overviewAPI, "fetchAlert")
      .mockImplementationOnce(() => new Promise<Alert>((resolve) => { resolveFirst = resolve; }))
      .mockRejectedValueOnce(new ApiError("Alert not found", 404));
    const router = testRouter();
    await router.push("/events?tab=alerts");
    const store = useOverviewStore();
    const wrapper = shallowMount(AppShell, { global: { plugins: [router] } });

    await router.push("/events?tab=alerts&alert_id=alert-1");
    expect(store.alertDetailsLoading).toBe(true);
    await router.push("/events?tab=alerts&alert_id=missing");
    await flushPromises();
    resolveFirst(alert);
    await flushPromises();

    expect(fetchAlert).toHaveBeenLastCalledWith("missing");
    expect(store.alertDetailsModalOpen).toBe(true);
    expect(store.alertDetailsLoading).toBe(false);
    expect(store.alertDetailsAlert).toBeNull();
    expect(store.alertDetailsError).toBe("This alert was not found.");
    await router.push("/events?tab=alerts");
    expect(store.alertDetailsModalOpen).toBe(false);
    wrapper.unmount();
  });
});
