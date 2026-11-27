import { flushPromises, mount, RouterLinkStub } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { fetchNetworks } from "../api/cluster";
import NetworksView from "./NetworksView.vue";

vi.mock("../api/cluster", () => ({ fetchNetworks: vi.fn() }));

const appNetwork = {
  id: "app-network",
  name: "app_default",
  scope: "swarm",
  driver: "overlay",
  internal: false,
  attachable: true,
  ingress: false,
  managed: true,
  stack_name: "app",
  labels: { "com.example.owner": "platform" },
  options: {
    "com.docker.network.driver.overlay.vxlanid_list": "4097",
    "com.example.encrypted": "true",
  },
};
const infraNetwork = {
  ...appNetwork,
  id: "infra-network",
  name: "infra_default",
  stack_name: "infra",
  labels: undefined,
  options: { "com.docker.network.driver.overlay.vxlanid_list": "4098" },
};

describe("NetworksView", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(fetchNetworks)
      .mockResolvedValueOnce({ networks: [infraNetwork, appNetwork] })
      .mockResolvedValue({ networks: [appNetwork] });
  });

  it("loads networks filtered by selected stacks", async () => {
    const wrapper = mount(NetworksView, { global: { stubs: { RouterLink: RouterLinkStub } } });
    await flushPromises();

    expect(fetchNetworks).toHaveBeenCalledWith({ stacks: [] });
    expect(wrapper.findAll(".networks-filter-icon")).toHaveLength(2);
    expect(wrapper.get(".networks-stack-filter summary").text()).toContain("All stacks");
    expect(wrapper.findAll(".app-select-option").map((option) => option.text())).toEqual(["app", "infra"]);

    const appOption = wrapper.findAll<HTMLInputElement>(".app-select-option input")
      .find((option) => option.element.value === "app");
    await appOption!.setValue(true);
    await flushPromises();

    expect(fetchNetworks).toHaveBeenLastCalledWith({ stacks: ["app"] });
    expect(wrapper.findAll("tbody tr")).toHaveLength(1);
    expect(wrapper.text()).toContain("app_default");
    expect(wrapper.text()).not.toContain("infra_default");
    expect(wrapper.get("tbody tr").classes()).toContain("app-table-row--clickable");
    expect(wrapper.findAll(".networks-row-link")).toHaveLength(2);
    const stackLinks = wrapper.findAllComponents(RouterLinkStub);
    expect(stackLinks).toHaveLength(4);
    expect(stackLinks.every((link) => link.classes().includes("service-inline-link"))).toBe(true);
    expect(stackLinks.map((link) => link.props("to"))).toEqual([
      { name: "network-details", params: { network: "app-network" } },
      { name: "stack-details", params: { stack: "app" } },
      { name: "network-details", params: { network: "app-network" } },
      { name: "stack-details", params: { stack: "app" } },
    ]);
  });

  it("renders a complete compact network summary for mobile layouts", async () => {
    const wrapper = mount(NetworksView, { global: { stubs: { RouterLink: RouterLinkStub } } });
    await flushPromises();

    const summaries = wrapper.findAll(".networks-mobile-summary");
    expect(summaries).toHaveLength(2);
    expect(summaries[0].text()).toContain("app_default");
    expect(summaries[0].text()).toContain("app");
    expect(summaries[0].text()).toContain("overlay");
    expect(summaries[0].text()).toContain("swarm");
    expect(summaries[0].text()).not.toContain("VXLAN");
    expect(summaries[0].findAll(".networks-mobile-flags dt").map((item) => item.text())).toEqual([
      "Attachable:", "Internal:", "Ingress:", "Managed ⓘ:",
    ]);
    expect(summaries[0].findAll(".networks-mobile-flags dd").map((item) => item.text())).toEqual([
      "true", "false", "false", "true",
    ]);
    expect(summaries[0].text()).toContain("com.example.owner=platform");
    expect(summaries[0].text()).toContain("com.example.encrypted=true");
    expect(summaries[0].get(".networks-managed-label").attributes("data-tooltip"))
      .toBe("Managed by swarm-deploy GitOps through networks.yaml.");
    expect(summaries[1].findAll(".networks-mobile-details dt").map((item) => item.text())).not.toContain("Labels:");
  });
});
