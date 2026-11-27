import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createMemoryHistory, createRouter } from "vue-router";

import NetworkView from "./NetworkView.vue";

const api = vi.hoisted(() => ({
  fetchNetwork: vi.fn(),
  fetchSwarmServices: vi.fn(),
}));

vi.mock("../api/cluster", () => ({ fetchNetwork: api.fetchNetwork }));
vi.mock("../api/services", () => ({ fetchSwarmServices: api.fetchSwarmServices }));

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
  api.fetchSwarmServices.mockResolvedValue({
    services: [
      {
        id: "service-id", name: "api", full_name: "app_api", stack: "app", image: "api:v1",
        mode: "replicated", replicas: { running: 2, desired: 2 }, updated_at: "2026-11-27T18:00:00Z",
        endpoint_mode: "vip", network_targets: ["network-id"],
      },
      {
        id: "other-id", name: "other", full_name: "app_other", stack: "app", image: "other:v1",
        mode: "replicated", replicas: { running: 1, desired: 1 }, updated_at: "2026-11-27T18:00:00Z",
        endpoint_mode: "vip", network_targets: ["other-network"],
      },
    ],
  });
});

describe("NetworkView", () => {
  it("shows dense network metadata, addressing, services, and diagnostics", async () => {
    const wrapper = await mountPage();

    expect(api.fetchNetwork).toHaveBeenCalledExactlyOnceWith("network-id");
    expect(wrapper.get(".network-heading h2").text()).toBe("app_backend");
    expect(wrapper.get(".network-managed").attributes("data-tooltip"))
      .toBe("Managed by swarm-deploy GitOps through networks.yaml.");

    const metadata = wrapper.get('table[aria-label="Network metadata"]');
    for (const value of ["network-id", "app", "overlay", "swarm", "Enabled", "Yes", "No"]) {
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
    expect(services.text()).toContain("VIP");
    expect(services.text()).toContain("10.0.1.10");
    expect(services.text()).toContain("2/2");

    const diagnostics = wrapper.get(".network-diagnostics");
    expect(diagnostics.text()).toContain("VXLAN ID4097");
    expect(diagnostics.text()).toContain("MTU1450");
    expect(diagnostics.text()).toContain("router");
    expect(diagnostics.text()).toContain("example.com/ownerplatform");
    expect(diagnostics.text()).toContain("example.com/modeprivate");
    expect(diagnostics.text()).toContain("node-a192.0.2.1");
  });

  it("keeps network details available when connected services fail", async () => {
    api.fetchSwarmServices.mockRejectedValueOnce(new Error("Swarm unavailable"));
    const wrapper = await mountPage();

    expect(wrapper.get(".network-heading h2").text()).toBe("app_backend");
    expect(wrapper.get('[role="alert"]').text()).toContain("Swarm unavailable");
  });
});
