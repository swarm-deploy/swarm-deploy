import { mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useOverviewStore } from "../../stores/overview";
import AlertDetailsModal from "./AlertDetailsModal.vue";

describe("Alert details modal", () => {
  beforeEach(() => setActivePinia(createPinia()));

  it("shows actionable deployment diagnostics and related navigation", async () => {
    const store = useOverviewStore();
    store.alertDetailsModalOpen = true;
    store.alertDetailsAlert = {
      id: "alert", fingerprint: "sync_failed:stack:app", kind: "sync_failed", resourceType: "stack",
      resourceId: "app", status: "open", title: "Synchronization failed",
      message: "update service api: rpc error: image not found", failureStage: "apply", errorCode: "apply_failed",
      deploymentId: "deployment", commit: "abcdef012345", occurrences: 3,
      openedAt: "2026-10-10T10:00:00Z", updatedAt: "2026-10-10T10:05:00Z",
      openEventId: "one", latestEventId: "three",
    };
    const openDeployment = vi.spyOn(store, "openDeploymentDetailsModal").mockResolvedValue();
    const openCommit = vi.spyOn(store, "openCommitDetailsModal").mockResolvedValue();
    const wrapper = mount(AlertDetailsModal, {
      global: { stubs: { RouterLink: { props: ["to"], template: "<a><slot /></a>" } } },
    });

    expect(wrapper.text()).toContain("update service api: rpc error: image not found");
    expect(wrapper.text()).toContain("Failure stageapply");
    expect(wrapper.text()).toContain("Occurrences3");
    expect(wrapper.text()).toContain("Technical codeapply_failed");
    expect(wrapper.text()).toContain("View stack");
    await wrapper.get("button.button-ghost").trigger("click");
    expect(openCommit).toHaveBeenCalledWith("abcdef012345");

    store.alertDetailsModalOpen = true;
    store.alertDetailsAlert = {
      id: "alert", fingerprint: "sync_failed:stack:app", kind: "sync_failed", resourceType: "stack",
      resourceId: "app", status: "open", title: "Synchronization failed", message: "image not found",
      deploymentId: "deployment", occurrences: 3, openedAt: "2026-10-10T10:00:00Z",
      updatedAt: "2026-10-10T10:05:00Z", openEventId: "one", latestEventId: "three",
    };
    await wrapper.vm.$nextTick();
    const deploymentButton = wrapper.findAll("button").find((button) => button.text() === "View deployment");
    expect(deploymentButton).toBeDefined();
    await deploymentButton!.trigger("click");
    expect(openDeployment).toHaveBeenCalledWith("deployment");
    wrapper.unmount();
  });

  it("shows an honest fallback without inventing a deployment link", () => {
    const store = useOverviewStore();
    store.alertDetailsModalOpen = true;
    store.alertDetailsAlert = {
      id: "alert", fingerprint: "sync_failed:stack:app", kind: "sync_failed", resourceType: "stack",
      resourceId: "app", status: "open", title: "Synchronization failed",
      message: "Synchronization failed during validation. No detailed diagnostic was recorded.",
      failureStage: "validation", errorCode: "preparation_failed", occurrences: 1,
      openedAt: "2026-10-10T10:00:00Z", updatedAt: "2026-10-10T10:00:00Z",
      openEventId: "one", latestEventId: "one",
    };
    const wrapper = mount(AlertDetailsModal, {
      global: { stubs: { RouterLink: { template: "<a><slot /></a>" } } },
    });
    expect(wrapper.text()).toContain("No detailed diagnostic was recorded");
    expect(wrapper.text()).not.toContain("View deployment");
    wrapper.unmount();
  });
});
