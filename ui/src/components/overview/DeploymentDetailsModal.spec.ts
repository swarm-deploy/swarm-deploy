import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { defineComponent } from "vue";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Deployment } from "../../api/types";
import { useOverviewStore } from "../../stores/overview";
import CommitDetailsModal from "./CommitDetailsModal.vue";
import DeploymentDetailsModal from "./DeploymentDetailsModal.vue";

vi.mock("../../stores/assistant", () => ({
  useAssistantStore: () => ({ enabled: false, pending: false }),
}));
vi.mock("../../stores/ui", () => ({
  useUIStore: () => ({ openAssistantDrawer: vi.fn() }),
}));

const ModalHost = defineComponent({
  components: { CommitDetailsModal, DeploymentDetailsModal },
  template: "<DeploymentDetailsModal /><CommitDetailsModal />",
});

function deployment(
  status: Deployment["status"] = "failed",
  basis: Deployment["comparison_basis"] = "none",
): Deployment {
  return {
    id: "one", stack: "app", commit: "abcdef012345", status, phase: "apply", reason: "apply_failed",
    apply_status: "failed", verification_status: "skipped", cleanup_status: "skipped", actual_state_status: "unknown",
    started_at: "2026-10-10T10:00:00Z", comparison_basis: basis, comparison_status: "unknown",
    diff_available: true, diagnostic: "update service api: image not found",
    configuration_yaml: "services:\n  api:\n    image: api:2\n    environment:\n      API_TOKEN: '****'\n",
    services: [{ name: "api", image: "api:2", replicas: "3" }],
    summary: { added: 1, changed: 2, removed: 0, redacted: 1 },
    resources: { services: 1, configs: 1, secrets: 1, networks: 2, volumes: 0 },
    changes: [
      { resourceType: "service", resourceName: "api", field: "image", operation: "changed",
        before: "api:1", after: "api:2", redacted: false },
      { resourceType: "service", resourceName: "api", field: "deploy.replicas", operation: "changed",
        before: "2", after: "3", redacted: false },
      { resourceType: "service", resourceName: "api", field: "environment.API_TOKEN", operation: "added",
        after: "plaintext", redacted: true },
    ],
  };
}

describe("Deployment Details modal", () => {
  beforeEach(() => setActivePinia(createPinia()));

  it("shows an initial summary and keeps the full masked configuration secondary", async () => {
    const store = useOverviewStore();
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsData = deployment("succeeded");
    const wrapper = mount(DeploymentDetailsModal);

    expect(wrapper.text()).toContain("Initial snapshot");
    expect(wrapper.text()).toContain("First recorded desired-state snapshot");
    expect(wrapper.findAll(".deployment-counts strong").map((item) => item.text())).toEqual([
      "1 service", "2 networks", "1 secret", "1 config",
    ]);
    expect(wrapper.text()).toContain("api:2");
    expect(wrapper.findAll(".deployment-resource-section")).toHaveLength(4);
    expect(wrapper.findAll(".deployment-service-table th").map((item) => item.text())).toEqual(["Name", "Image", "Replicas"]);
    expect(wrapper.find(".deployment-service-table tbody td:last-child").text()).toBe("3");
    expect(wrapper.text()).not.toContain("API_TOKEN");
    expect(wrapper.find(".deployment-outcome").exists()).toBe(false);

    await wrapper.get('[role="tab"]:last-child').trigger("click");
    expect(wrapper.text()).toContain("Configuration Snapshot");
    expect(wrapper.find('[aria-label="Recorded desired configuration YAML"]').html()).toContain("API_TOKEN");
    expect(wrapper.find('[aria-label="Recorded desired configuration YAML"]').html()).toContain("****");
    expect(wrapper.html()).not.toContain("plaintext");
    wrapper.unmount();
  });

  it.each([
    ["3", "3"],
    ["1", "1"],
    ["Global", "Global"],
    ["Job: 2 concurrent, 5 total", "Job: 2 concurrent, 5 total"],
    ["Replicated job", "Replicated job"],
  ])("renders snapshot replica setting %s", (replicas, expected) => {
    const store = useOverviewStore();
    const details = deployment("succeeded");
    details.services = [{ name: "api", image: "api:2", replicas }];
    details.changes = details.changes?.filter((change) => change.field !== "deploy.replicas") ?? [];
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsData = details;
    const wrapper = mount(DeploymentDetailsModal);

    expect(wrapper.get(".deployment-service-table tbody td:last-child").text()).toBe(expected);
    wrapper.unmount();
  });

  it("renders historical deployments without service summaries", () => {
    const store = useOverviewStore();
    const details = deployment("succeeded");
    delete details.services;
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsData = details;
    const wrapper = mount(DeploymentDetailsModal);

    expect(wrapper.get(".deployment-service-table tbody td:last-child").text()).toBe("3");
    wrapper.unmount();
  });

  it("shows an unknown setting when an old record has no replica information", () => {
    const store = useOverviewStore();
    const details = deployment("succeeded");
    delete details.services;
    details.changes = details.changes?.filter((change) => change.field !== "deploy.replicas") ?? [];
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsData = details;
    const wrapper = mount(DeploymentDetailsModal);

    expect(wrapper.get(".deployment-service-table tbody td:last-child").text()).toBe("—");
    wrapper.unmount();
  });

  it("keeps a very long YAML value inside the configuration viewer while switching tabs", async () => {
    const store = useOverviewStore();
    const details = deployment("succeeded");
    const longValue = `https://registry.example.test/image@sha256:${"a".repeat(4096)}`;
    details.configuration_yaml = `services:\n  api:\n    image: ${longValue}\n`;
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsData = details;
    const wrapper = mount(DeploymentDetailsModal);
    const modal = wrapper.get(".deployment-modal").element;

    await wrapper.get('[role="tab"]:last-child').trigger("click");
    const yaml = wrapper.get('[aria-label="Recorded desired configuration YAML"]');
    expect(yaml.classes()).toEqual(expect.arrayContaining(["deployment-yaml", "manifest-code"]));
    expect(yaml.text()).toContain(longValue);
    expect(wrapper.get(".deployment-modal").element).toBe(modal);

    await wrapper.get('[role="tab"]:first-child').trigger("click");
    expect(wrapper.get('[role="tab"]:first-child').attributes("aria-selected")).toBe("true");
    expect(wrapper.get(".deployment-modal").element).toBe(modal);
    wrapper.unmount();
  });

  it("groups a real desired-state diff with readable old and new values", () => {
    const store = useOverviewStore();
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsData = deployment("failed", "successful_baseline");
    const wrapper = mount(DeploymentDetailsModal);

    expect(wrapper.text()).toContain("Desired Changes");
    expect(wrapper.text()).toContain("serviceapi");
    expect(wrapper.text()).toContain("Environment");
    expect(wrapper.text()).toContain("api:1");
    expect(wrapper.text()).toContain("api:2");
    expect(wrapper.text()).toContain("****");
    expect(wrapper.text()).not.toContain("plaintext");
    expect(wrapper.text()).toContain("update service api: image not found");
    expect(wrapper.findAll(".deployment-resource")).toHaveLength(1);
    wrapper.unmount();
  });

  it("shows change details on demand instead of taking vertical space", async () => {
    const store = useOverviewStore();
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsData = deployment("succeeded", "successful_baseline");
    const wrapper = mount(DeploymentDetailsModal, { attachTo: document.body });

    const button = wrapper.get('[aria-label="About desired changes"]');
    const message = "Compared with the last successful desired snapshot.";
    expect(wrapper.get(".deployment-info-heading h3").text()).toBe("Desired Changes");
    expect(wrapper.text()).not.toContain(message);
    expect(button.attributes("aria-expanded")).toBe("false");
    expect(wrapper.find('[role="tooltip"]').exists()).toBe(false);

    await button.trigger("click");
    expect(button.attributes("aria-expanded")).toBe("true");
    expect(wrapper.get('[role="tooltip"]').text()).toContain(message);
    expect(wrapper.get('[role="tooltip"]').text()).toContain("do not confirm runtime convergence");
    expect(button.attributes("aria-describedby")).toBe("deployment-changes-tooltip");

    await wrapper.get(".deployment-tabs").trigger("pointerdown");
    expect(wrapper.find('[role="tooltip"]').exists()).toBe(false);
    expect(button.attributes("aria-expanded")).toBe("false");
    expect(store.deploymentDetailsModalOpen).toBe(true);
    wrapper.unmount();
  });

  it("supports hover and Escape without closing the deployment modal", async () => {
    const store = useOverviewStore();
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsData = deployment("succeeded", "successful_baseline");
    const wrapper = mount(DeploymentDetailsModal);

    const trigger = wrapper.get(".deployment-info");
    const hover = (name: string) => trigger.element.dispatchEvent(
      Object.assign(new Event(name), { pointerType: "mouse" }),
    );
    hover("pointerenter");
    await wrapper.vm.$nextTick();
    expect(wrapper.find('[role="tooltip"]').exists()).toBe(true);
    hover("pointerleave");
    await wrapper.vm.$nextTick();
    expect(wrapper.find('[role="tooltip"]').exists()).toBe(false);

    await trigger.get("button").trigger("click");
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await wrapper.vm.$nextTick();
    expect(wrapper.find('[role="tooltip"]').exists()).toBe(false);
    expect(store.deploymentDetailsModalOpen).toBe(true);
    wrapper.unmount();
  });

  it("shows the configuration masking explanation without modifying the YAML viewer", async () => {
    const store = useOverviewStore();
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsData = deployment("succeeded", "successful_baseline");
    const wrapper = mount(DeploymentDetailsModal);

    await wrapper.get('[role="tab"]:last-child').trigger("click");
    expect(wrapper.get(".deployment-info-heading h3").text()).toBe("Configuration Snapshot");
    expect(wrapper.text()).not.toContain("Sensitive values are masked before storage.");
    expect(wrapper.get('[aria-label="Recorded desired configuration YAML"]').html()).toContain("****");
    await wrapper.get('[aria-label="About configuration snapshot"]').trigger("click");
    expect(wrapper.get('[role="tooltip"]').text()).toBe("Sensitive values are masked before storage.");
    await wrapper.get('[role="tab"]:first-child').trigger("click");
    expect(wrapper.find('[role="tooltip"]').exists()).toBe(false);
    wrapper.unmount();
  });

  it("stacks long diff values without a redundant field counter", async () => {
    const store = useOverviewStore();
    const details = deployment("succeeded", "successful_baseline");
    const longImage = `registry.example.test/platform/api@sha256:${"a".repeat(64)}`;
    details.changes![0].after = longImage;
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsData = details;
    const wrapper = mount(DeploymentDetailsModal);

    expect(wrapper.findAll(".deployment-change")[0].classes()).toContain("deployment-change--long");
    expect(wrapper.findAll(".deployment-change")[1].classes()).not.toContain("deployment-change--long");
    expect(wrapper.text()).not.toContain("3 fields");
    expect(wrapper.text()).toContain("1 added · 2 changed · 0 removed");
    expect(wrapper.text()).toContain(longImage);

    details.changes = [details.changes![0]];
    details.summary = { added: 0, changed: 1, removed: 0, redacted: 0 };
    store.deploymentDetailsData = { ...details };
    await wrapper.vm.$nextTick();
    expect(wrapper.text()).not.toContain("1 field");
    expect(wrapper.text()).toContain("0 added · 1 changed · 0 removed");
    wrapper.unmount();
  });

  it("distinguishes zero changes from an unavailable comparison", async () => {
    const store = useOverviewStore();
    const unchanged = deployment("succeeded", "successful_baseline");
    unchanged.changes = [];
    unchanged.summary = { added: 0, changed: 0, removed: 0, redacted: 0 };
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsData = unchanged;
    const wrapper = mount(DeploymentDetailsModal);
    expect(wrapper.text()).toContain("No configuration changes");

    store.deploymentDetailsData = { ...unchanged, diff_available: false };
    await wrapper.vm.$nextTick();
    expect(wrapper.text()).toContain("Changes unavailable");
    expect(wrapper.text()).not.toContain("No configuration changes");
    wrapper.unmount();
  });

  it.each(["running", "succeeded"] as const)("shows %s status without a redundant outcome banner", (status) => {
    const store = useOverviewStore();
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsData = deployment(status);
    const wrapper = mount(DeploymentDetailsModal);

    expect(wrapper.get(".deployment-title-topline .deployment-modal-eyebrow").text()).toBe("Deployment");
    expect(wrapper.get(".deployment-title-topline .deployment-status").text()).toBe(status[0].toUpperCase() + status.slice(1));
    expect(wrapper.get(".deployment-status-dot").attributes("aria-hidden")).toBe("true");
    expect(wrapper.find(".deployment-status-icon").exists()).toBe(false);
    expect(wrapper.find(".deployment-outcome").exists()).toBe(false);
    expect(wrapper.get(".deployment-modal-content").classes()).toContain("deployment-modal-content--compact");
    expect(wrapper.text()).not.toContain("Apply pipeline completed");
    expect(wrapper.text()).toContain("Initial snapshot");
    wrapper.unmount();
  });

  it.each([
    ["failed", "Apply attempt failed"],
    ["interrupted", "Apply outcome is unknown"],
  ] as const)("keeps %s diagnostics visible", (status, expected) => {
    const store = useOverviewStore();
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsData = deployment(status);
    const wrapper = mount(DeploymentDetailsModal);

    expect(wrapper.get(".deployment-title-topline .deployment-modal-eyebrow").text()).toBe("Deployment");
    expect(wrapper.get(".deployment-title-topline .deployment-status").text()).toBe(status[0].toUpperCase() + status.slice(1));
    expect(wrapper.get(".deployment-status-dot").attributes("aria-hidden")).toBe("true");
    expect(wrapper.find(".deployment-status-icon").exists()).toBe(false);
    expect(wrapper.get(".deployment-outcome").classes()).toContain(`deployment-outcome--${status}`);
    expect(wrapper.get(".deployment-modal-content").classes()).not.toContain("deployment-modal-content--compact");
    expect(wrapper.get(".deployment-diagnostic").text()).toContain("update service api: image not found");
    expect(wrapper.get(".deployment-code").text()).toContain("apply_failed");
    expect(wrapper.text()).toContain(expected);
    wrapper.unmount();
  });

  it("closes on Escape and restores focus", async () => {
    const store = useOverviewStore();
    store.deploymentDetailsData = deployment();
    const trigger = document.createElement("button");
    document.body.appendChild(trigger);
    trigger.focus();
    const wrapper = mount(DeploymentDetailsModal, { attachTo: document.body });
    store.deploymentDetailsModalOpen = true;
    await flushPromises();
    expect(document.activeElement).toBe(wrapper.get('[aria-label="Close deployment details"]').element);
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await wrapper.vm.$nextTick();
    expect(store.deploymentDetailsModalOpen).toBe(false);
    expect(document.activeElement).toBe(trigger);
    wrapper.unmount();
    trigger.remove();
  });

  it("keeps deployment state and focus when commit details is the top dialog", async () => {
    const store = useOverviewStore();
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsData = deployment("succeeded");
    vi.spyOn(store, "openCommitDetailsModal").mockImplementation(async (hash) => {
      store.commitDetailsHash = hash;
      store.commitDetailsData = {
        full_hash: hash,
        author: "Developer",
        message: "Keep nested dialogs ordered",
        date: "2026-10-10T10:00:00Z",
        changed_files: ["compose.yaml"],
      };
      store.commitDetailsModalOpen = true;
    });
    const wrapper = mount(ModalHost, { attachTo: document.body });

    await wrapper.get('[role="tab"]:last-child').trigger("click");
    const commitTrigger = wrapper.get(".deployment-commit-link");
    (commitTrigger.element as HTMLElement).focus();
    await commitTrigger.trigger("click");
    await flushPromises();

    const dialogs = wrapper.findAll('[role="dialog"]');
    expect(dialogs).toHaveLength(2);
    expect(dialogs[1].attributes("aria-labelledby")).toBe("commit-details-title");
    expect(wrapper.get(".deployment-modal").element.parentElement?.hasAttribute("inert")).toBe(true);
    expect(wrapper.get(".deployment-modal").element.parentElement?.getAttribute("aria-hidden")).toBe("true");
    expect(document.activeElement).toBe(wrapper.get('[aria-label="Close modal"]').element);

    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await flushPromises();
    expect(store.commitDetailsModalOpen).toBe(false);
    expect(store.deploymentDetailsModalOpen).toBe(true);
    expect(wrapper.get('[role="tab"]:last-child').attributes("aria-selected")).toBe("true");
    expect(wrapper.find('[aria-label="Recorded desired configuration YAML"]').exists()).toBe(true);
    expect(document.activeElement).toBe(commitTrigger.element);

    await commitTrigger.trigger("click");
    await flushPromises();
    await wrapper.findAll(".modal-overlay").at(-1)!.trigger("click");
    await flushPromises();
    expect(store.commitDetailsModalOpen).toBe(false);
    expect(store.deploymentDetailsModalOpen).toBe(true);
    expect(wrapper.get('[role="tab"]:last-child').attributes("aria-selected")).toBe("true");

    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await flushPromises();
    expect(store.deploymentDetailsModalOpen).toBe(false);
    wrapper.unmount();
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
    await wrapper.get("button:not(.modal-close)").trigger("click");
    expect(retry).toHaveBeenCalledWith("one");
    wrapper.unmount();
  });
});
