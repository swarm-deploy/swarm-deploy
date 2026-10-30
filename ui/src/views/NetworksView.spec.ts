import { flushPromises, mount } from "@vue/test-utils";
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
};
const infraNetwork = { ...appNetwork, id: "infra-network", name: "infra_default", stack_name: "infra" };

describe("NetworksView", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(fetchNetworks)
      .mockResolvedValueOnce({ networks: [infraNetwork, appNetwork] })
      .mockResolvedValue({ networks: [appNetwork] });
  });

  it("loads networks filtered by selected stacks", async () => {
    const wrapper = mount(NetworksView);
    await flushPromises();

    expect(fetchNetworks).toHaveBeenCalledWith({ stacks: [] });
    expect(wrapper.findAll(".networks-filter-icon")).toHaveLength(2);
    expect(wrapper.get(".networks-stack-filter summary").text()).toContain("All stacks");
    expect(wrapper.findAll(".app-multi-select-option").map((option) => option.text())).toEqual(["app", "infra"]);

    const appOption = wrapper.findAll<HTMLInputElement>(".app-multi-select-option input")
      .find((option) => option.element.value === "app");
    await appOption!.setValue(true);
    await flushPromises();

    expect(fetchNetworks).toHaveBeenLastCalledWith({ stacks: ["app"] });
    expect(wrapper.findAll("tbody tr")).toHaveLength(1);
    expect(wrapper.text()).toContain("app_default");
    expect(wrapper.text()).not.toContain("infra_default");
  });
});
