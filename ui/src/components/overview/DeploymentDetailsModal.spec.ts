import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { defineComponent } from "vue";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Deployment } from "../../api/types";
import { useOverviewStore } from "../../stores/overview";
import CommitDetailsModal from "./CommitDetailsModal.vue";
import DeploymentDetailsModal from "./DeploymentDetailsModal.vue";

const apiMocks = vi.hoisted(() => ({ fetchDeploymentInitJobLogs: vi.fn() }));

const assistantStore = vi.hoisted(() => ({
  enabled: true,
  pending: false,
  newChat: vi.fn(),
  sendMessage: vi.fn(async () => undefined),
}));
const uiStore = vi.hoisted(() => ({ openAssistantDrawer: vi.fn() }));

vi.mock("../../stores/assistant", () => ({ useAssistantStore: () => assistantStore }));
vi.mock("../../stores/ui", () => ({ useUIStore: () => uiStore }));
vi.mock("../../api/overview", () => ({ fetchDeploymentInitJobLogs: apiMocks.fetchDeploymentInitJobLogs }));

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
    latest_attempt_id: "attempt-one", attempt_count: 1,
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
    attempts: [{
      id: "attempt-one", number: 1, status: status === "superseded" ? "interrupted" : status,
      phase: "apply", reason: "apply_failed", diagnostic: "update service api: image not found",
      apply_status: "failed", verification_status: "skipped", cleanup_status: "skipped",
      actual_state_status: "unknown", started_at: "2026-10-10T10:00:00Z",
      init_jobs: [],
    }],
  };
}

describe("Deployment Details modal", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    assistantStore.enabled = true;
    assistantStore.pending = false;
    assistantStore.newChat.mockClear();
    assistantStore.sendMessage.mockClear();
    uiStore.openAssistantDrawer.mockClear();
    apiMocks.fetchDeploymentInitJobLogs.mockReset();
  });

  it("puts attempts first with a compact total-count badge", () => {
    const store = useOverviewStore();
    const details = deployment("failed");
    details.attempt_count = 3;
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsData = details;
    const wrapper = mount(DeploymentDetailsModal);

    expect(wrapper.findAll('[role="tab"]').map((tab) => tab.text())).toEqual(["Attempts 3", "Changes", "Configuration"]);
    expect(wrapper.get(".deployment-tab-count").text()).toBe("3");
    expect(wrapper.get('[role="tab"]:first-child').attributes("aria-selected")).toBe("true");
    wrapper.unmount();
  });

  it.each([
    ["running", "Attempts"],
    ["pending", "Attempts"],
    ["failed", "Attempts"],
    ["interrupted", "Attempts"],
    ["succeeded", "Changes"],
  ] as const)("defaults %s deployments to %s", (status, selected) => {
    const store = useOverviewStore();
    store.deploymentDetailsModalOpen = true;
    const details = deployment(status === "pending" ? "running" : status);
    if (status === "pending") (details as { status: string }).status = status;
    store.deploymentDetailsData = details;
    const wrapper = mount(DeploymentDetailsModal);

    expect(wrapper.findAll('[role="tab"]').find((tab) => tab.text().startsWith(selected))?.attributes("aria-selected")).toBe("true");
    wrapper.unmount();
  });

  it("preserves a manually selected tab across realtime status updates", async () => {
    const store = useOverviewStore();
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsData = deployment("running");
    const wrapper = mount(DeploymentDetailsModal);

    const configurationTab = wrapper.findAll('[role="tab"]')[2];
    await configurationTab.trigger("click");
    store.deploymentDetailsData = { ...deployment("succeeded"), configuration_yaml: "services: {}\n" };
    await wrapper.vm.$nextTick();

    expect(configurationTab.attributes("aria-selected")).toBe("true");
    expect(wrapper.find('[aria-label="Recorded desired configuration YAML"]').exists()).toBe(true);
    wrapper.unmount();
  });

  it("shows newest attempts first and only expands unsuccessful rows", async () => {
    const store = useOverviewStore();
    const details = deployment("failed");
    const longDiagnostic = `docker service update --image registry.example.test/api:${"x".repeat(512)}\npermission denied`;
    details.attempt_count = 5;
    details.attempts = [
      { ...details.attempts[0], id: "old", number: 1, status: "succeeded", diagnostic: "old diagnostic" },
      { ...details.attempts[0], id: "running", number: 3, status: "running", diagnostic: "running diagnostic" },
      { ...details.attempts[0], id: "new", number: 5, status: "failed", diagnostic: longDiagnostic },
      { ...details.attempts[0], id: "pending", number: 4, status: "pending" as never, diagnostic: "pending diagnostic" },
      { ...details.attempts[0], id: "interrupted", number: 2, status: "interrupted", diagnostic: "interrupted diagnostic" },
    ];
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsData = details;
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    const wrapper = mount(DeploymentDetailsModal);
    const rows = wrapper.findAll(".deployment-attempt-row");

    expect(rows.map((row) => row.attributes("open"))).toEqual([undefined, undefined, undefined, undefined, undefined]);
    expect(rows.map((row) => row.get(".deployment-attempt-status").classes().at(-1))).toEqual([
      "deployment-attempt-status--failed",
      "deployment-attempt-status--pending",
      "deployment-attempt-status--running",
      "deployment-attempt-status--interrupted",
      "deployment-attempt-status--succeeded",
    ]);
    expect(wrapper.text()).not.toContain("Attempt 4");
    expect(wrapper.find(".deployment-attempt-details").isVisible()).toBe(false);
    expect(rows.slice(0, 4).every((row) => row.element.tagName === "DETAILS")).toBe(true);
    expect(rows[4].element.tagName).toBe("DIV");
    expect(rows[4].find("summary").exists()).toBe(false);
    expect(rows[4].find(".deployment-chevron").exists()).toBe(false);
    expect(rows[4].find(".deployment-attempt-details").exists()).toBe(false);

    await rows[0].get("summary").trigger("click");
    expect(rows[0].attributes("open")).toBeDefined();
    expect(rows[0].get(".deployment-attempt-details code").text()).toBe(longDiagnostic);
    expect(rows[0].get(".deployment-attempt-details pre").attributes("style")).toBeUndefined();
    const detailButtons = rows[0].findAll(".deployment-attempt-details button");
    expect(detailButtons.map((button) => button.text())).toEqual(["Explain", "Copy"]);
    await detailButtons[1].trigger("click");
    expect(writeText).toHaveBeenCalledWith(longDiagnostic);
    wrapper.unmount();
  });

  it("loads Init Job logs on demand and renders missing and truncated states safely", async () => {
    const store = useOverviewStore();
    const details = deployment("succeeded");
    details.attempts[0].status = "succeeded";
    details.attempts[0].init_jobs = [{
      ordinal: 1, service_name: "api", job_name: "migrate", status: "succeeded",
      started_at: "2026-10-10T10:00:00Z", finished_at: "2026-10-10T10:00:01Z",
      logs_available: true, logs_truncated: true,
    }, {
      ordinal: 2, service_name: "api", job_name: "seed", status: "succeeded",
      started_at: "2026-10-10T10:00:01Z", finished_at: "2026-10-10T10:00:02Z",
      logs_available: false, logs_truncated: false,
    }];
    apiMocks.fetchDeploymentInitJobLogs.mockResolvedValue("done\n<script>alert(1)</script>\n");
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsData = details;
    const wrapper = mount(DeploymentDetailsModal);

    expect(apiMocks.fetchDeploymentInitJobLogs).not.toHaveBeenCalled();
    await wrapper.findAll('[role="tab"]')[0].trigger("click");
    const attempt = wrapper.get(".deployment-attempt-row");
    expect(attempt.element.tagName).toBe("DETAILS");
    await attempt.get("summary").trigger("click");
    expect(attempt.text()).toContain("migrate");
    expect(attempt.text()).toContain("seed");
    expect(attempt.text()).toContain("Last 64 KiB");
    expect(attempt.text()).toContain("Logs unavailable");

    const firstJob = attempt.findAll(".deployment-init-job")[0];
    const title = firstJob.get(".deployment-init-job-title");
    expect(title.text()).toContain("migrate");
    expect(title.get(".deployment-attempt-status").attributes("aria-label")).toBe("succeeded Init Job");
    expect(firstJob.find(".deployment-init-job-summary > .deployment-attempt-status").exists()).toBe(false);
    const logsButton = firstJob.get(".deployment-init-job-logs-button");
    expect(logsButton.text()).toBe("");
    expect(logsButton.attributes("aria-label")).toBe("View logs for Init Job migrate");
    expect(logsButton.attributes("aria-expanded")).toBe("false");
    expect(logsButton.get("svg path").attributes("d")).toContain("M3 2.5");

    await logsButton.trigger("click");
    await flushPromises();
    expect(apiMocks.fetchDeploymentInitJobLogs).toHaveBeenCalledWith("one", "attempt-one", 1);
    expect(logsButton.attributes("aria-label")).toBe("Hide logs for Init Job migrate");
    expect(logsButton.attributes("aria-expanded")).toBe("true");
    expect(attempt.get(".deployment-init-job-logs code").text()).toContain("<script>alert(1)</script>");
    expect(attempt.get(".deployment-init-job-logs").html()).toContain("&lt;script&gt;alert(1)&lt;/script&gt;");
    expect(attempt.findAll(".deployment-init-job button")).toHaveLength(1);
    wrapper.unmount();
  });

  it("starts a new assistant chat with deployment and diagnostic context", async () => {
    const store = useOverviewStore();
    const details = deployment("failed");
    details.services = [
      { name: "api", image: "api:2", replicas: "3" },
      { name: "worker", image: "worker:2", replicas: "2" },
    ];
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsData = details;
    const wrapper = mount(DeploymentDetailsModal);

    await wrapper.get(".deployment-attempt-row summary").trigger("click");
    await wrapper.findAll(".deployment-attempt-details button")[0].trigger("click");
    await flushPromises();

    expect(store.deploymentDetailsModalOpen).toBe(false);
    expect(assistantStore.newChat).toHaveBeenCalledOnce();
    expect(uiStore.openAssistantDrawer).toHaveBeenCalledOnce();
    expect(assistantStore.sendMessage).toHaveBeenCalledWith([
      "Explain this deployment error.",
      "",
      "deployment_id: one",
      "stack_name: app",
      "service_name: api",
      "error:",
      "update service api: image not found",
    ].join("\n"));
    expect(assistantStore.newChat.mock.invocationCallOrder[0]).toBeLessThan(
      assistantStore.sendMessage.mock.invocationCallOrder[0],
    );
    wrapper.unmount();
  });

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

    await wrapper.findAll('[role="tab"]')[1].trigger("click");
    expect(wrapper.findAll('[role="tab"]')[1].attributes("aria-selected")).toBe("true");
    expect(wrapper.get(".deployment-modal").element).toBe(modal);
    wrapper.unmount();
  });

  it("groups a real desired-state diff with readable old and new values", async () => {
    const store = useOverviewStore();
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsData = deployment("failed", "successful_baseline");
    const wrapper = mount(DeploymentDetailsModal);
    await wrapper.findAll('[role="tab"]')[1].trigger("click");

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

  it("renders removed resources as non-expandable summaries without configuration fields", () => {
    const store = useOverviewStore();
    const details = deployment("succeeded", "successful_baseline");
    const removedResources = [
      ["service", "legacy-api"],
      ["secret", "legacy-secret"],
      ["config", "legacy-config"],
      ["network", "legacy-network"],
      ["volume", "legacy-volume"],
      ["extension", "legacy-extension"],
    ];
    details.changes = [
      ...removedResources.map(([resourceType, resourceName]) => ({
        resourceType,
        resourceName,
        field: "definition",
        operation: "removed" as const,
        before: `configuration-for-${resourceName}`,
        redacted: false,
      })),
      { resourceType: "service", resourceName: "worker", field: "definition", operation: "added",
        after: "worker-definition", redacted: false },
      { resourceType: "service", resourceName: "api", field: "image", operation: "changed",
        before: "api:1", after: "api:2", redacted: false },
      { resourceType: "secret", resourceName: "api-token", field: "definition", operation: "added",
        after: "secret-plaintext", redacted: true },
    ];
    details.summary = { added: 2, changed: 1, removed: removedResources.length, redacted: 1 };
    store.deploymentDetailsModalOpen = true;
    store.deploymentDetailsData = details;
    const wrapper = mount(DeploymentDetailsModal);

    const removed = wrapper.findAll(".deployment-resource--removed");
    expect(removed).toHaveLength(removedResources.length);
    removed.forEach((resource, index) => {
      expect(resource.element.tagName).toBe("DIV");
      expect(resource.text()).toContain(removedResources[index][0]);
      expect(resource.text()).toContain(removedResources[index][1]);
      expect(resource.get(".deployment-resource-operation").text()).toBe("Removed");
      expect(resource.find("summary").exists()).toBe(false);
      expect(resource.find(".deployment-chevron").exists()).toBe(false);
      expect(resource.find(".deployment-change-section").exists()).toBe(false);
      expect(resource.text()).not.toContain(`configuration-for-${removedResources[index][1]}`);
    });

    expect(wrapper.findAll("details.deployment-resource")).toHaveLength(3);
    expect(wrapper.text()).toContain("worker-definition");
    expect(wrapper.text()).toContain("api:1");
    expect(wrapper.text()).toContain("api:2");
    expect(wrapper.text()).toContain("****");
    expect(wrapper.html()).not.toContain("secret-plaintext");
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
    if (status === "succeeded") expect(wrapper.text()).toContain("Initial snapshot");
    else expect(wrapper.find(".deployment-attempts-panel").exists()).toBe(true);
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
