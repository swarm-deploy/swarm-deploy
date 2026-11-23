import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { fetchNodes } from "../api/cluster";
import type { NodeInfo } from "../api/types";
import ClusterView from "./ClusterView.vue";

vi.mock("../api/cluster", () => ({
  fetchNodes: vi.fn(),
  addNodeLabel: vi.fn(),
  updateNodeLabel: vi.fn(),
  deleteNodeLabel: vi.fn(),
}));

function node(partial: Partial<NodeInfo>): NodeInfo {
  return {
    id: "old-worker",
    hostname: "worker-1",
    status: "down",
    availability: "active",
    manager_status: "worker",
    engine_version: "29.8.1",
    addr: "192.0.2.10",
    cpu_nano: 2_000_000_000,
    memory_bytes: 4_000_000_000,
    ...partial,
  };
}

async function render(nodes: NodeInfo[]) {
  vi.mocked(fetchNodes).mockResolvedValue({ nodes });
  const wrapper = mount(ClusterView);
  await flushPromises();
  return wrapper;
}

describe("ClusterView stale worker hint", () => {
  beforeEach(() => {
    vi.mocked(fetchNodes).mockReset();
  });

  it("shows an informational hint only on a Down worker that shares a hostname with a different Ready worker", async () => {
    const wrapper = await render([
      node({ id: "old-worker", hostname: "worker-1", status: "down" }),
      node({ id: "new-worker", hostname: "worker-1", status: "ready" }),
    ]);

    const hints = wrapper.findAll(".node-stale-hint");
    expect(hints).toHaveLength(1);
    expect(hints[0].text()).toContain("Possible stale worker entry");
    expect(hints[0].text()).toContain("docker node ls");
    expect(hints[0].element.closest(".node-card")?.querySelector(".node-state-text")?.textContent).toBe("down");
    expect(wrapper.findAll(".node-card")).toHaveLength(2);
    wrapper.unmount();
  });

  it("does not suggest stale nodes for missing, disconnected, or manager replacements", async () => {
    const wrapper = await render([
      node({ id: "alone", hostname: "alone", status: "down" }),
      node({ id: "old-down", hostname: "both-down", status: "down" }),
      node({ id: "new-down", hostname: "both-down", status: "down" }),
      node({ id: "old-manager", hostname: "manager-1", status: "down", manager_status: "unreachable" }),
      node({ id: "ready-manager", hostname: "manager-1", status: "ready", manager_status: "leader" }),
      node({ id: "old-worker-2", hostname: "with-manager", status: "down" }),
      node({ id: "ready-manager-2", hostname: "with-manager", status: "ready", manager_status: "reachable" }),
      node({ id: "unknown", hostname: "unknown-status", status: "unknown" }),
      node({ id: "ready-unknown", hostname: "unknown-status", status: "ready" }),
    ]);

    expect(wrapper.findAll(".node-stale-hint")).toHaveLength(0);
    wrapper.unmount();
  });

  it("ignores empty hostnames and matches hostnames case-insensitively", async () => {
    const wrapper = await render([
      node({ id: "old", hostname: "WORKER-2", status: "down" }),
      node({ id: "ready", hostname: "worker-2", status: "ready" }),
      node({ id: "empty-old", hostname: "", status: "down" }),
      node({ id: "empty-ready", hostname: "", status: "ready" }),
    ]);

    expect(wrapper.findAll(".node-stale-hint")).toHaveLength(1);
    wrapper.unmount();
  });
});
