import { mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it } from "vitest";
import type { Deployment } from "../../api/types";
import { useOverviewStore } from "../../stores/overview";
import DeploymentDetailsModal from "./DeploymentDetailsModal.vue";

describe("Deployment Details modal", () => {
  beforeEach(() => setActivePinia(createPinia()));
  it("shows semantic changes, masked values and distinct missing/empty values", async () => {
    const store = useOverviewStore();
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsData = {
      id: "one", stack: "app", commit: "abcdef012345", status: "failed",
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
  it("shows loading and errors without stale details", async () => {
    const store = useOverviewStore();
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsLoading = true;
    const wrapper = mount(DeploymentDetailsModal);
    expect(wrapper.text()).toContain("Loading deployment details");
    store.deploymentDetailsLoading = false;
    store.deploymentDetailsError = "unavailable";
    await wrapper.vm.$nextTick();
    expect(wrapper.find('[role="alert"]').text()).toContain("unavailable");
    wrapper.unmount();
  });
});
