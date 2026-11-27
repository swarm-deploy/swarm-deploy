import { flushPromises, mount } from "@vue/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createMemoryHistory, createRouter } from "vue-router";

import { i18n } from "../i18n";
import NetworkView from "./NetworkView.vue";

const api = vi.hoisted(() => ({
  fetchNetwork: vi.fn(),
  fetchServices: vi.fn(),
}));

vi.mock("../api/cluster", () => ({ fetchNetwork: api.fetchNetwork }));
vi.mock("../api/services", () => ({ fetchServices: api.fetchServices }));

const blank = { template: "<div />" };

async function mountPage() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: "/networks", name: "networks", component: blank },
      { path: "/networks/:network", name: "network-details", component: NetworkView },
      { path: "/stacks/:stack", name: "stack-details", component: blank },
      { path: "/services/:stack/:service", name: "service-details", component: blank },
    ],
  });
  await router.push("/networks/network-id");
  const wrapper = mount(NetworkView, { global: { plugins: [router] } });
  await flushPromises();
  return wrapper;
}

beforeEach(() => {
  vi.clearAllMocks();
  api.fetchNetwork.mockResolvedValue({
    id: "network-id",
    name: "app_backend",
    scope: "swarm",
    driver: "overlay",
    internal: false,
    attachable: true,
    ingress: false,
    managed: true,
    stack_name: "app",
    created_at: "2026-11-27T18:00:00Z",
    enable_ipv4: true,
    enable_ipv6: false,
    config_only: false,
    ipam_driver: "default",
    ipam: [{
      subnet: "10.0.1.0/24",
      gateway: "10.0.1.1",
      ip_range: "10.0.1.0/25",
      aux_addresses: { router: "10.0.1.2" },
    }],
    labels: { "example.com/owner": "platform" },
    options: {
      encrypted: "",
      "com.docker.network.driver.overlay.vxlanid_list": "4097",
      "com.docker.network.driver.mtu": "1450",
      "example.com/mode": "private",
    },
    services: [{ name: "app_api", vip: "10.0.1.10" }],
    peers: [{ name: "node-a", ip: "192.0.2.1" }],
  });
  api.fetchServices.mockResolvedValue({
    services: [
      {
        name: "api", stack: "app", image: "api:v1", image_version: "v1",
        sync_status: "Synced", type: "application", type_title: "Application",
      },
      {
        name: "other", stack: "app", image: "other:v1", image_version: "v1",
        sync_status: "Synced", type: "application", type_title: "Application",
      },
    ],
  });
});
afterEach(() => { i18n.global.locale.value = "en"; });

describe("NetworkView", () => {
  it("shows dense network metadata, addressing, and services reported by network inspect", async () => {
    const wrapper = await mountPage();

    expect(api.fetchNetwork).toHaveBeenCalledExactlyOnceWith("network-id");
    expect(wrapper.get(".network-heading h2").text()).toBe("app_backend");
    expect(wrapper.get(".network-managed").attributes("data-tooltip"))
      .toBe("Managed by swarm-deploy GitOps through networks.yaml.");

    const metadata = wrapper.get('table[aria-label="Network metadata"]');
    for (const value of ["network-id", "app", "overlay", "swarm", "VXLAN ID", "4097", "Enabled", "Yes", "No"]) {
      expect(metadata.text()).toContain(value);
    }

    const addressing = wrapper.get('table[aria-label="Addressing"]');
    for (const value of ["10.0.1.0/24", "10.0.1.1", "10.0.1.0/25"]) {
      expect(addressing.text()).toContain(value);
    }

    const services = wrapper.get('table[aria-label="Connected services"]');
    expect(services.text()).toContain("api");
    expect(services.text()).not.toContain("other");
    expect(services.get('a[href="/services/app/api"]').text()).toBe("api");
    expect(services.get('a[href="/stacks/app"]').text()).toBe("app");
    expect(services.text()).toContain("10.0.1.10");
    expect(services.text()).not.toContain("Replicas");
    expect(services.text()).not.toContain("Discovery");

    expect(wrapper.find(".network-diagnostics").exists()).toBe(false);
  });

  it("keeps network-inspect services available when service enrichment fails", async () => {
    api.fetchServices.mockRejectedValueOnce(new Error("Catalog unavailable"));
    const wrapper = await mountPage();

    expect(wrapper.get(".network-heading h2").text()).toBe("app_backend");
    expect(wrapper.get('[role="alert"]').text()).toContain("Catalog unavailable");
    expect(wrapper.get('table[aria-label="Connected services"]').text()).toContain("app_api");
    expect(wrapper.get('table[aria-label="Connected services"]').text()).toContain("10.0.1.10");
    expect(wrapper.find(".network-service-name strong").exists()).toBe(false);
  });

  it("keeps Scope and its value in English for the Russian locale", async () => {
    i18n.global.locale.value = "ru";
    const wrapper = await mountPage();
    const metadata = wrapper.get('table[aria-label="Метаданные сети"]');

    expect(metadata.findAll("th").map((item) => item.text())).toContain("Scope");
    expect(metadata.text()).toContain("swarm");
    expect(metadata.text()).not.toContain("Область действия");
  });
});
