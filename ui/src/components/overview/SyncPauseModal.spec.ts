import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";

import SyncPauseModal from "./SyncPauseModal.vue";

const api = vi.hoisted(() => ({
  getStackSyncPauses: vi.fn(),
  setStackSyncPause: vi.fn(),
  resumeStackSync: vi.fn(),
}));

vi.mock("../../api/syncPauses", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/syncPauses")>()),
  ...api,
}));

describe("SyncPauseModal", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.setStackSyncPause.mockResolvedValue({});
    api.resumeStackSync.mockResolvedValue(undefined);
  });

  it("pauses the entire stack", async () => {
    api.getStackSyncPauses.mockResolvedValue({
      effectively_paused_services: 0,
    });
    const wrapper = mount(SyncPauseModal, { props: { open: true, stack: "app" } });
    await flushPromises();

    expect(wrapper.text()).toContain("preserves every service in the stack");
    await wrapper.get("form").trigger("submit");
    await flushPromises();
    expect(api.setStackSyncPause).toHaveBeenCalledWith("app", expect.any(String));
    expect(wrapper.emitted("changed")).toHaveLength(1);
  });

  it("shows the active stack pause and resumes it", async () => {
    api.getStackSyncPauses.mockResolvedValue({
      stack_pause: { stack: "app", expires_at: null },
      effectively_paused_services: 2,
    });
    const wrapper = mount(SyncPauseModal, { props: { open: true, stack: "app" } });
    await flushPromises();

    expect(wrapper.text()).toContain("Paused services2");
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
      stack_pause: { stack: "ops", expires_at: null },
      effectively_paused_services: 1,
    });
    await flushPromises();
    expect(wrapper.get('button[type="submit"]').text()).toBe("Resume stack sync");

    resolveApp({ effectively_paused_services: 0 });
    await flushPromises();
    expect(wrapper.get('button[type="submit"]').text()).toBe("Resume stack sync");
  });
});
