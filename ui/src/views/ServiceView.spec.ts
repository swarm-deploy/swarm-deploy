import { createPinia } from "pinia";
import { flushPromises, mount } from "@vue/test-utils";
import { nextTick } from "vue";
import { beforeEach, describe, expect, it, vi } from "vitest";

import ServiceView from "./ServiceView.vue";

const apiMocks = vi.hoisted(() => ({
  fetchServiceDeployments: vi.fn(),
  fetchServiceRealtime: vi.fn(),
  fetchServiceStatus: vi.fn(),
  fetchServices: vi.fn(),
  openTaskLogsStream: vi.fn(),
}));

vi.mock("vue-router", () => ({
  useRoute: () => ({ params: { stack: "infra", service: "api" } }),
}));

vi.mock("../api/overview", () => ({
  fetchServiceDeployments: apiMocks.fetchServiceDeployments,
  fetchServiceRealtime: apiMocks.fetchServiceRealtime,
  fetchServiceStatus: apiMocks.fetchServiceStatus,
  openTaskLogsStream: apiMocks.openTaskLogsStream,
}));

vi.mock("../api/services", () => ({
  fetchServices: apiMocks.fetchServices,
}));

async function mountServiceView() {
  const wrapper = mount(ServiceView, {
    global: { plugins: [createPinia()] },
  });
  await flushPromises();
  return wrapper;
}

describe("ServiceView", () => {
  beforeEach(() => {
    apiMocks.fetchServices.mockResolvedValue({
      services: [{
        name: "api",
        stack: "infra",
        sync_status: "Synced",
        type: "application",
        type_title: "Application",
        image: "registry.example.com/infra/api:latest",
        image_version: "latest",
        web_routes: [],
      }],
    });
    apiMocks.fetchServiceStatus.mockResolvedValue({
      stack: "infra",
      service: "api",
      links: [],
      spec: {
        image: "registry.example.com/infra/api:latest",
        mode: "replicated",
        replicas: 1,
        requested_ram_bytes: 0,
        requested_cpu_nano: 0,
        limit_ram_bytes: 0,
        limit_cpu_nano: 0,
        labels: {
          custom: {
            "team.owner": "platform",
            "prometheus.port": "8080",
            "com.docker.extra": "generated",
          },
          docker: { "com.docker.stack.namespace": "infra" },
          swarm_deploy: { "org.swarm-deploy.service.type": "application" },
        },
        secrets: [],
        network: [],
      },
    });
    apiMocks.fetchServiceDeployments.mockResolvedValue({ deployments: [] });
    apiMocks.fetchServiceRealtime.mockResolvedValue({
      tasks: [{
        id: "task-id",
        node: "node-id",
        node_name: "manager-1",
        current_state: "running",
        created_at: "2026-10-04T18:00:00Z",
        updated_at: "2026-10-04T18:01:00Z",
        error: "",
      }],
    });
  });

  it("shows only custom labels inline and groups every label in the modal", async () => {
    const wrapper = await mountServiceView();

    expect(wrapper.findAll(".service-label-chip").map((item) => item.text())).toEqual([
      "team.owner=platform",
    ]);
    expect(wrapper.get(".service-labels-show-all").text()).toBe("Show all (5)");

    await wrapper.get(".service-labels-show-all").trigger("click");

    const modal = wrapper.get(".service-labels-modal-card");
    expect(modal.findAll(".service-labels-group h3").map((item) => item.text())).toEqual([
      "Custom",
      "Docker",
      "Swarm Deploy",
      "Other",
    ]);
    expect(modal.text()).toContain("com.docker.extra");
    expect(modal.text()).toContain("com.docker.stack.namespace");
    expect(modal.text()).toContain("org.swarm-deploy.service.type");
    expect(modal.text()).toContain("prometheus.port");

    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    await nextTick();
    expect(wrapper.get(".service-labels-modal-card").element.parentElement?.classList.contains("hidden")).toBe(true);
  });

  it("does not show No labels when only system labels are available", async () => {
    apiMocks.fetchServiceStatus.mockResolvedValue({
      stack: "infra",
      service: "api",
      links: [],
      spec: {
        image: "registry.example.com/infra/api:latest",
        mode: "replicated",
        replicas: 1,
        requested_ram_bytes: 0,
        requested_cpu_nano: 0,
        limit_ram_bytes: 0,
        limit_cpu_nano: 0,
        labels: {
          docker: { "com.docker.stack.namespace": "infra" },
          swarm_deploy: { "org.swarm-deploy.service.type": "application" },
        },
      },
    });

    const wrapper = await mountServiceView();

    expect(wrapper.findAll(".service-label-chip")).toHaveLength(0);
    expect(wrapper.get(".service-labels-show-all").text()).toBe("Show all (2)");
    expect(wrapper.get(".service-labels-row").text()).not.toContain("No labels.");
  });

  it("renders a compact mobile task view without the copy action", async () => {
    const wrapper = await mountServiceView();
    const mobileTask = wrapper.get(".service-realtime-mobile-item");

    expect(mobileTask.text()).toContain("manager-1");
    expect(mobileTask.text()).toContain("running");
    expect(mobileTask.text()).toContain("Updated");
    expect(mobileTask.text()).not.toContain("Created");
    expect(mobileTask.find("dl").exists()).toBe(false);
    expect(mobileTask.get(".service-realtime-mobile-logs").text()).toBe("Logs");
    expect(mobileTask.find(".service-copy-task-id-button").exists()).toBe(false);
    expect(wrapper.find(".service-realtime-desktop .service-copy-task-id-button").exists()).toBe(true);
  });
});
