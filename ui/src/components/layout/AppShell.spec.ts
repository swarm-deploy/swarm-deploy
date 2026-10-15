import { createPinia, setActivePinia } from "pinia";
import { flushPromises, shallowMount } from "@vue/test-utils";
import { createMemoryHistory, createRouter } from "vue-router";
import { beforeEach, describe, expect, it, vi } from "vitest";

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
    routes: [{ path: "/deployments", component: { template: "<div />" } }],
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
