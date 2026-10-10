import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Deployment } from "../../api/types";
import { useOverviewStore } from "../../stores/overview";
import DeploymentDetailsModal from "./DeploymentDetailsModal.vue";

function deployment(status: Deployment["status"] = "failed"): Deployment {
  return {
    id: "one", stack: "app", commit: "abcdef012345", status,
    started_at: "2026-10-10T10:00:00Z", comparison_basis: "none", comparison_status: "unknown",
    summary: { added: 1, changed: 2, removed: 0, redacted: 1 },
    changes: [
      { resourceType: "service", resourceName: "api", field: "image", operation: "changed",
        before: "api:1", after: "api:2", redacted: false },
      { resourceType: "service", resourceName: "api", field: "environment.EMPTY", operation: "added",
        after: "", redacted: false },
      { resourceType: "secret", resourceName: "key", field: "content", operation: "changed",
        before: "plaintext-before", after: "plaintext-after", redacted: true },
    ],
  } as Deployment;
}

describe("Deployment Details modal", () => {
  beforeEach(() => setActivePinia(createPinia()));

  it("shows semantic changes, masked values and distinct missing/empty values", async () => {
    const store = useOverviewStore();
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsData = deployment();
    const wrapper = mount(DeploymentDetailsModal);

    expect(wrapper.text()).toContain("planned desired changes");
    expect(wrapper.text()).toContain("no reliable previous state");
    expect(wrapper.text()).toContain("api:1");
    expect(wrapper.text()).toContain("api:2");
    expect(wrapper.text()).toContain('"" (empty)');
    expect(wrapper.text()).toContain("—");
    expect(wrapper.text()).toContain("****");
    expect(wrapper.text()).not.toContain("plaintext-before");
    expect(wrapper.text()).not.toContain("plaintext-after");
    await wrapper.find('[aria-label="Close deployment details"]').trigger("click");
    expect(store.deploymentDetailsModalOpen).toBe(false);
    wrapper.unmount();
  });

  it("renders interrupted as warning instead of failed", () => {
    const store = useOverviewStore();
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsData = deployment("interrupted");
    const wrapper = mount(DeploymentDetailsModal);

    const status = wrapper.find(".deployment-status");
    expect(status.text()).toBe("interrupted");
    expect(status.classes()).toContain("deployment-status--interrupted");
    expect(status.classes()).not.toContain("deployment-status--failed");
    wrapper.unmount();
  });

  it("traps keyboard focus, closes on Escape and restores the trigger", async () => {
    const store = useOverviewStore();
    store.deploymentDetailsData = deployment();

    const trigger = document.createElement("button");
    document.body.appendChild(trigger);
    trigger.focus();
    const wrapper = mount(DeploymentDetailsModal, { attachTo: document.body });

    store.deploymentDetailsModalOpen = true;
    await flushPromises();

    const close = wrapper.find('[aria-label="Close deployment details"]');
    const commit = wrapper.find("button.button-ghost");
    expect(document.activeElement).toBe(close.element);

    (commit.element as HTMLElement).focus();
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true }));
    expect(document.activeElement).toBe(close.element);

    (close.element as HTMLElement).focus();
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", shiftKey: true, bubbles: true }));
    expect(document.activeElement).toBe(commit.element);

    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await wrapper.vm.$nextTick();
    expect(store.deploymentDetailsModalOpen).toBe(false);
    expect(document.activeElement).toBe(trigger);

    wrapper.unmount();
    trigger.remove();
  });

  it("shows loading and errors without stale details and retries by ID", async () => {
    const store = useOverviewStore();
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsID = "one";
    store.deploymentDetailsLoading = true;
    store.deploymentDetailsData = deployment();
    const retry = vi.spyOn(store, "openDeploymentDetailsModal").mockResolvedValue();
    const wrapper = mount(DeploymentDetailsModal);

    expect(wrapper.text()).toContain("Loading deployment details");
    expect(wrapper.text()).not.toContain("api:1");

    store.deploymentDetailsLoading = false;
    store.deploymentDetailsError = "unavailable";
    await wrapper.vm.$nextTick();
    expect(wrapper.find('[role="alert"]').text()).toContain("unavailable");
    expect(wrapper.text()).not.toContain("api:1");

    await wrapper.get("button:not(.modal-close)").trigger("click");
    expect(retry).toHaveBeenCalledWith("one");
    wrapper.unmount();
  });
});
