import { createPinia, setActivePinia } from "pinia";
import { flushPromises, shallowMount } from "@vue/test-utils";
import { createMemoryHistory, createRouter } from "vue-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "../../api/client";
import * as overviewAPI from "../../api/overview";
import type { Alert } from "../../api/types";
import { useOverviewStore } from "../../stores/overview";
import AppShell from "./AppShell.vue";

const assistantStore = {
  enabled: false,
  conversationID: "",
  messages: [],
  historyOpen: false,
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
