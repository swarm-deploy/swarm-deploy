import { createPinia } from "pinia";
import { flushPromises, mount } from "@vue/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createMemoryHistory, createRouter } from "vue-router";

import DeploymentDetailsModal from "../components/overview/DeploymentDetailsModal.vue";
import SecretDetailsModal from "../components/secrets/SecretDetailsModal.vue";
import ServiceConfigView from "./ServiceConfigView.vue";
import ServiceView from "./ServiceView.vue";

vi.hoisted(() => {
  window.matchMedia = () => ({ matches: false }) as MediaQueryList;
});

const api = vi.hoisted(() => ({
  fetchDeployment: vi.fn(),
  fetchNetworks: vi.fn(),
  fetchSecretByName: vi.fn(),
  fetchServiceDeployments: vi.fn(),
  fetchServiceStatus: vi.fn(),
  fetchServices: vi.fn(),
  fetchSwarmServices: vi.fn(),
}));

vi.mock("../api/cluster", () => ({ fetchNetworks: api.fetchNetworks }));
vi.mock("../api/overview", async (importOriginal) => ({
  ...await importOriginal<typeof import("../api/overview")>(),
  fetchDeployment: api.fetchDeployment,
  fetchServiceDeployments: api.fetchServiceDeployments,
  fetchServiceStatus: api.fetchServiceStatus,
}));
vi.mock("../api/secrets", () => ({ fetchSecretByName: api.fetchSecretByName }));
vi.mock("../api/services", () => ({
  fetchServices: api.fetchServices,
  fetchSwarmServices: api.fetchSwarmServices,
}));

const date = "2026-10-04T18:00:00Z";
const image = "registry.example.test/infra/api:v2.3.1";
const liveAPI = {
  id: "service-id", name: "api", full_name: "infra_api", stack: "infra", mode: "replicated",
  replicas: { running: 2, desired: 2 },
  created_at: date, endpoint_mode: "vip",
  published_ports: [{ published_port: 8080, target_port: 80, protocol: "tcp" }],
  update_parallelism: 1, update_state: "completed", update_completed_at: date,
  network_targets: ["overlay-id"],
};
const liveWorker = {
  ...liveAPI, id: "worker-id", name: "worker", full_name: "infra_worker",
  replicas: { running: 1, desired: 1 },
};
const storedSpec = {
  image,
  mode: "replicated",
  replicas: 2,
  labels: {
    docker: { "com.docker.stack.namespace": "infra" },
    custom: { "swarm-deploy.gitops": "true", environment: "production", team: "platform" },
  },
  network: [{ target: "overlay-id" }],
  secrets: [{ secret_name: "api-token" }],
  configs: [{ config_id: "config-id", config_name: "api-config", target: "/etc/api.yaml" }],
};
const blank = { template: "<div />" };

async function mountPage(path = "/services/infra/api") {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: "/services/:stack/:service", name: "service-details", component: ServiceView },
      { path: "/services/:stack/:service/configs/:config", name: "service-config", component: ServiceConfigView },
      { path: "/stacks/:stack", name: "stack-details", component: blank },
      { path: "/realtime", name: "realtime", component: blank },
      { path: "/deployments", name: "deployments", component: blank },
    ],
  });
  await router.push(path);
  const wrapper = mount({
    components: { DeploymentDetailsModal, SecretDetailsModal },
    template: "<RouterView /><DeploymentDetailsModal /><SecretDetailsModal />",
  }, { attachTo: document.body, global: { plugins: [createPinia(), router] } });
  cleanup.push(() => wrapper.unmount());
  await flushPromises();
  return wrapper;
}

const cleanup: Array<() => void> = [];
beforeEach(() => {
  vi.clearAllMocks();
  api.fetchServices.mockResolvedValue({
    services: [{
      name: "api", stack: "infra", image, type_title: "Application",
      description: "Serves the API", repository_url: "https://github.com/example/api",
    }],
  });
  api.fetchServiceStatus.mockResolvedValue({ stack: "infra", service: "api", links: [], spec: storedSpec });
  api.fetchSwarmServices.mockResolvedValue({ services: [liveAPI, liveWorker, { ...liveWorker, id: "unrelated", full_name: "infra_other", network_targets: ["unrelated"] }] });
  api.fetchNetworks.mockResolvedValue({
    networks: [{
      id: "overlay-id", name: "infra-overlay", scope: "swarm", driver: "overlay", attachable: true,
      internal: false, ingress: false, ipam: [{ subnet: "10.0.4.0/24", gateway: "10.0.4.1" }],
    }],
  });
  api.fetchServiceDeployments.mockResolvedValue({
    deployments: [
      { id: "latest", image, image_version: "v2.3.1", status: "succeeded", created_at: date },
      { id: "older", image_version: "v2.3.0", status: "superseded", created_at: date },
    ],
  });
  api.fetchDeployment.mockResolvedValue({
    id: "latest", stack: "infra", status: "succeeded", started_at: date,
    resources: { services: 1, configs: 1, secrets: 1, networks: 1, volumes: 0 },
    summary: { added: 0, changed: 1, removed: 0, redacted: 0 },
  });
  api.fetchSecretByName.mockResolvedValue({ id: "secret-id", name: "api-token", labels: {}, used_by: [] });
});
afterEach(() => {
  cleanup.splice(0).forEach((dispose) => dispose());
  vi.unstubAllGlobals();
});

describe("ServiceView", () => {
  it("shows service metadata and the live status with a scoped Realtime link", async () => {
    const wrapper = await mountPage();
    const header = wrapper.get(".service-heading");
    expect(header.text()).toContain("Available");
    expect(header.text()).toContain("2 / 2 replicas");
    expect(header.text()).toContain("Succeeded");
    expect(header.text()).not.toContain("Application");
    expect(header.get('a[aria-label="Open api in Realtime"]').attributes("href")).toBe("/realtime?stack=infra&service=api");
    const table = wrapper.get('table[aria-label="Service metadata"]');
    expect(table.findAll("th").map((th) => th.text())).toEqual([
      "Stack", "Type", "Description", "Mode", "Service ID", "Endpoint mode", "Published port",
      "Update strategy", "Image", "Repository", "Created", "Labels",
    ]);
    for (const value of ["Application", "Serves the API", "replicated", "service-id", "VIP", "8080 → 80/tcp", "Rolling / parallelism 1", image]) {
      expect(table.text()).toContain(value);
    }
    expect(table.get("time").attributes("datetime")).toBe(date);
  });

  it("copies the exact image reference, service ID and full label", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("isSecureContext", true);
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    const wrapper = await mountPage();
    await wrapper.get('button[aria-label="Copy image reference"]').trigger("click");
    await wrapper.get('button[aria-label="Copy service ID"]').trigger("click");
    await wrapper.get(".service-labels-show").trigger("click");
    await wrapper.get('button[aria-label="Copy swarm-deploy.gitops label"]').trigger("click");
    expect(writeText.mock.calls).toEqual([[image], ["service-id"], ["swarm-deploy.gitops=true"]]);
  });

  it("shows three labels in the preview and exact keys in their existing groups", async () => {
    const wrapper = await mountPage();
    expect(wrapper.findAll(".service-label-token").map((item) => item.text())).toEqual([
      "gitops=true", "environment=production", "team=platform",
    ]);
    await wrapper.get(".service-labels-show").trigger("click");
    const labels = wrapper.get('.modal[aria-hidden="false"] [role="dialog"][aria-labelledby="service-labels-title"]');
    expect(labels.findAll("h3").map((item) => item.text())).toEqual(["Docker", "Swarm Deploy", "Custom"]);
    expect(labels.text()).toContain("com.docker.stack.namespace");
    expect(labels.text()).toContain("swarm-deploy.gitops");
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    await flushPromises();
    expect(wrapper.find('.modal[aria-hidden="false"] [role="dialog"][aria-labelledby="service-labels-title"]').exists()).toBe(false);
  });

  it("opens the Network details dialog with subnet, gateway and connected services", async () => {
    const wrapper = await mountPage();
    const buttons = wrapper.findAll("button.service-entity");
    await buttons.find((button) => button.text().includes("infra-overlay"))!.trigger("click");
    const dialog = wrapper.get('.modal[aria-hidden="false"] [role="dialog"][aria-labelledby="network-details-title"]');
    for (const value of ["10.0.4.0/24", "10.0.4.1", "infra_worker", "infra_api"]) {
      expect(dialog.text()).toContain(value);
    }
    expect(dialog.text()).not.toContain("infra_other");
  });

  it("opens the existing Secret details dialog", async () => {
    const wrapper = await mountPage();
    await wrapper.findAll("button.service-entity").find((button) => button.text().includes("api-token"))!.trigger("click");
    await flushPromises();
    expect(api.fetchSecretByName).toHaveBeenCalledExactlyOnceWith("api-token");
    expect(wrapper.findAll('.modal[aria-hidden="false"] [role="dialog"]').some((dialog) => dialog.isVisible() && dialog.text().includes("api-token"))).toBe(true);
  });

  it("links to the Config page with the service mount path", async () => {
    const wrapper = await mountPage();
    const link = wrapper.get("a.service-entity");
    expect(link.attributes("href")).toBe("/services/infra/api/configs/config-id?target=/etc/api.yaml");
    await link.trigger("click");
    await flushPromises();
    expect(wrapper.get('table[aria-label="Config details"]').text()).toContain("/etc/api.yaml");
    expect(wrapper.findAll('.modal[aria-hidden="false"] [role="dialog"]').some((dialog) => dialog.isVisible())).toBe(false);
  });

  it("opens real Deployment details and shows the original status vocabulary", async () => {
    const wrapper = await mountPage();
    const history = wrapper.get('table[aria-label="Deployment history"]');
    expect(history.findAll("th").map((item) => item.text())).toEqual(["Version", "Status", "Started", "Details"]);
    expect(history.text()).toContain("Superseded");
    expect(wrapper.get(".service-deployments").text()).toContain("Converged");
    expect(wrapper.get(".service-deployments").text()).not.toMatch(/Current|Git sync healthy/);
    expect(history.find(".app-status-badge-dot").exists()).toBe(false);
    await history.get("tbody tr").trigger("click");
    await flushPromises();
    expect(api.fetchDeployment).toHaveBeenCalledExactlyOnceWith("latest");
    expect(wrapper.findAll('.modal[aria-hidden="false"] [role="dialog"]').some((dialog) => dialog.isVisible() && dialog.text().includes("Succeeded"))).toBe(true);
    await history.get('button[aria-label="Open deployment v2.3.0 details"]').trigger("click");
    expect(api.fetchDeployment).toHaveBeenLastCalledWith("older");
  });

  it("keeps the metadata visible when the live service cannot be loaded", async () => {
    api.fetchSwarmServices.mockRejectedValueOnce(new Error("Swarm unavailable"));
    const wrapper = await mountPage();
    expect(wrapper.get(".service-heading").text()).toContain("Unknown");
    expect(wrapper.get(".service-metadata").text()).toContain("Application");
    expect(wrapper.get('[role="status"]').text()).toBe("Swarm unavailable");
  });
});
