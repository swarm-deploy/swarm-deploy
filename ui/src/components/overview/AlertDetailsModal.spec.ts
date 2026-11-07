import { mount, RouterLinkStub } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { nextTick } from "vue";

import type { Alert, AlertFinding } from "../../api/types";
import { useOverviewStore } from "../../stores/overview";
import AlertDetailsModal from "./AlertDetailsModal.vue";

const openedAt = "2026-10-10T10:00:00Z";
const updatedAt = "2026-10-10T10:05:00Z";

const alert: Alert = {
  id: "alert", fingerprint: "sync_failed:stack:app", kind: "sync_failed", resourceType: "stack",
  resourceId: "app", status: "open", title: "Synchronization failed",
  message: "update service api: rpc error: image not found", errorCode: "apply_failed",
  occurrences: 3, openedAt, updatedAt, openEventId: "one", latestEventId: "three",
};

const imageFinding: AlertFinding = {
  code: "image_not_found", summary: "No such image: api:1", detectedAt: openedAt,
};

function mountAlert(overrides: Partial<Alert> = {}) {
  const store = useOverviewStore();
  store.openAlertDetailsModal({ ...alert, ...overrides });
  const wrapper = mount(AlertDetailsModal, { global: { stubs: { RouterLink: RouterLinkStub } } });
  return { store, wrapper };
}

describe("Alert details modal", () => {
  beforeEach(() => setActivePinia(createPinia()));
  afterEach(() => vi.restoreAllMocks());

  it("shows the open incident and omits findings and actions without data", () => {
    const { wrapper } = mountAlert({ findings: [] });

    expect(wrapper.get(".app-status-badge--error").text()).toBe("Active");
    expect(wrapper.text()).toContain("3 occurrences");
    expect(wrapper.get(".alert-details-diagnostic").text()).toBe(alert.message);
    expect(wrapper.get(".alert-details-metadata code").text()).toBe("apply_failed");
    expect(wrapper.get("footer time").attributes("datetime")).toBe(updatedAt);
    expect(wrapper.find(".alert-details-resolution").exists()).toBe(false);
    expect(wrapper.find("details").exists()).toBe(false);
    expect(wrapper.text()).not.toContain("View deployment");
    expect(wrapper.text()).not.toContain("Commit");
    wrapper.unmount();
  });

  it("shows the recorded resolution and keeps a single finding collapsed", () => {
    const { wrapper } = mountAlert({
      status: "resolved",
      resolvedAt: updatedAt,
      resolution: { reason: "recovered", message: "Sync completed on the next attempt", eventId: "four" },
      findings: [imageFinding],
    });

    expect(wrapper.get(".app-status-badge--success").text()).toBe("Resolved");
    expect(wrapper.get(".alert-details-resolution h4").text()).toBe("Recovered successfully");
    expect(wrapper.get(".alert-details-resolution p").text()).toBe("Sync completed on the next attempt");
    expect(wrapper.get(".alert-details-resolution time").attributes("datetime")).toBe(updatedAt);
    expect(wrapper.get(".alert-details-metadata").text()).toContain("Resolved");
    expect(wrapper.get("details").attributes("open")).toBeUndefined();
    expect(wrapper.get("summary").text()).toMatch(/Findings\s+1/);
    wrapper.unmount();
  });

  it("keeps multiple findings separate from the alert code and resets disclosure on reopen", async () => {
    const findings = [imageFinding, { code: "another_cause", summary: "Another diagnostic cause", detectedAt: updatedAt }];
    const { store, wrapper } = mountAlert({ findings });
    const disclosure = wrapper.get<HTMLDetailsElement>("details");

    expect(disclosure.element.open).toBe(false);
    expect(disclosure.get("summary").text()).toMatch(/Findings\s+2/);
    expect(disclosure.findAll("li strong").map((item) => item.text())).toEqual(findings.map(({ summary }) => summary));
    expect(disclosure.findAll("li code").map((item) => item.text())).toEqual(findings.map(({ code }) => code));
    expect(wrapper.get(".alert-details-metadata code").text()).toBe("apply_failed");

    disclosure.element.open = true;
    store.closeAlertDetailsModal();
    await nextTick();
    store.openAlertDetailsModal({ ...alert, findings });
    await nextTick();
    expect(wrapper.get<HTMLDetailsElement>("details").element.open).toBe(false);
    wrapper.unmount();
  });

  it("uses the existing stack, commit and deployment destinations", async () => {
    const details = { ...alert, resourceId: "app/name", deploymentId: "deployment", commit: "abcdef012345" };
    const { store, wrapper } = mountAlert(details);
    const openDeployment = vi.spyOn(store, "openDeploymentDetailsModal").mockResolvedValue();
    const openCommit = vi.spyOn(store, "openCommitDetailsModal").mockResolvedValue();

    const links = wrapper.findAllComponents(RouterLinkStub);
    expect(links.map((link) => link.props("to"))).toEqual([
      { path: "/overview", hash: "#stack-app%2Fname" },
      { path: "/overview", hash: "#stack-app%2Fname" },
    ]);
    await wrapper.get(".alert-details-metadata button").trigger("click");
    expect(openCommit).toHaveBeenCalledWith("abcdef012345");
    expect(store.alertDetailsModalOpen).toBe(false);

    store.openAlertDetailsModal(details);
    await nextTick();
    await wrapper.get("footer button").trigger("click");
    expect(openDeployment).toHaveBeenCalledWith("deployment");
    expect(store.alertDetailsModalOpen).toBe(false);
    wrapper.unmount();
  });

  it("links a service resource and displays resource removal as its resolution", async () => {
    const { store, wrapper } = mountAlert({
      kind: "service_drift", resourceType: "service", resourceId: "app/api", status: "resolved",
      resolution: { reason: "resource_removed", message: "Service removed from desired configuration", eventId: "four" },
    });

    expect(wrapper.get(".alert-details-resolution h4").text()).toBe("Resource removed");
    expect(wrapper.get(".alert-details-resolution p").text()).toBe("Service removed from desired configuration");
    const link = wrapper.getComponent(RouterLinkStub);
    expect(link.props("to")).toEqual({ name: "service-details", params: { stack: "app", service: "api" } });
    await link.trigger("click");
    expect(store.alertDetailsModalOpen).toBe(false);
    wrapper.unmount();
  });
});
