import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";

import SyncPauseModal from "./SyncPauseModal.vue";

const api = vi.hoisted(() => ({
  getStackSyncPauses: vi.fn(),
  setStackSyncPause: vi.fn(),
  resumeStackSync: vi.fn(),
  removeServiceSyncPause: vi.fn(),
}));

vi.mock("../../api/syncPauses", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/syncPauses")>()),
  ...api,
}));

function buttonByText(wrapper: ReturnType<typeof mount>, text: string) {
  return wrapper.findAll("button").find((button) => button.text() === text)!;
}

describe("SyncPauseModal", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.setStackSyncPause.mockResolvedValue({});
    api.resumeStackSync.mockResolvedValue(undefined);
    api.removeServiceSyncPause.mockResolvedValue(undefined);
  });

  it("shows direct service pauses and pauses the entire stack", async () => {
    api.getStackSyncPauses.mockResolvedValue({
      service_pauses: [{ scope: "service", stack: "app", service: "api", expires_at: null }],
      effectively_paused_services: 1,
    });
    const wrapper = mount(SyncPauseModal, { props: { open: true, stack: "app" } });
    await flushPromises();

    expect(wrapper.text()).toContain("Independent service pauses remain active")
    expect(wrapper.text()).toContain("api");
    expect(wrapper.text()).toContain("No expiration");
    await wrapper.get("form").trigger("submit");
    await flushPromises();
    expect(api.setStackSyncPause).toHaveBeenCalledWith("app", expect.any(String));
    expect(wrapper.emitted("changed")).toHaveLength(1);
  });

  it("removes only direct pauses and warns when the stack still pauses a service", async () => {
    api.getStackSyncPauses.mockResolvedValue({
      stack_pause: { scope: "stack", stack: "app", expires_at: null },
      service_pauses: [{ scope: "service", stack: "app", service: "api", expires_at: null }],
      effectively_paused_services: 2,
    });
    const wrapper = mount(SyncPauseModal, { props: { open: true, stack: "app" } });
    await flushPromises();

    expect(wrapper.text()).toContain("Listed services remain paused");
    await buttonByText(wrapper, "Remove service pause").trigger("click");
    await flushPromises();
    expect(api.removeServiceSyncPause).toHaveBeenCalledWith("app", "api");
    expect(wrapper.text()).toContain("Service pause removed. The service remains paused by the stack-level pause.");

    await wrapper.get("form").trigger("submit");
    await flushPromises();
    expect(api.resumeStackSync).toHaveBeenCalledWith("app");
  });

  it("disables mutations when loading the current state fails", async () => {
    api.getStackSyncPauses.mockRejectedValue(new Error("manager unavailable"));
    const wrapper = mount(SyncPauseModal, { props: { open: true, stack: "app" } });
    await flushPromises();

    expect(wrapper.get('[role="alert"]').text()).toContain("manager unavailable");
    expect(wrapper.get('button[type="submit"]').attributes("disabled")).toBeDefined();
    await wrapper.get("form").trigger("submit");
    expect(api.setStackSyncPause).not.toHaveBeenCalled();
    expect(api.resumeStackSync).not.toHaveBeenCalled();
  });

  it("ignores an older response after the stack changes", async () => {
    let resolveApp!: (value: object) => void;
    let resolveOps!: (value: object) => void;
    api.getStackSyncPauses.mockImplementation((stack: string) => new Promise((resolve) => {
      if (stack === "app") resolveApp = resolve;
      else resolveOps = resolve;
    }));
    const wrapper = mount(SyncPauseModal, { props: { open: true, stack: "app" } });
    await wrapper.setProps({ stack: "ops" });
    resolveOps({
      stack_pause: { scope: "stack", stack: "ops", expires_at: null },
      service_pauses: [],
      effectively_paused_services: 1,
    });
    await flushPromises();
    expect(wrapper.get('button[type="submit"]').text()).toBe("Resume stack sync");

    resolveApp({ service_pauses: [], effectively_paused_services: 0 });
    await flushPromises();
    expect(wrapper.get('button[type="submit"]').text()).toBe("Resume stack sync");
  });
});
