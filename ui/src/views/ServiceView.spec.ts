import { createPinia } from "pinia";
import { flushPromises, mount } from "@vue/test-utils";
import { nextTick } from "vue";
import { beforeEach, describe, expect, it, vi } from "vitest";

import ServiceView from "./ServiceView.vue";
import { useOverviewStore } from "../stores/overview";

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
    global: {
      plugins: [createPinia()],
      stubs: { RouterLink: { name: "RouterLink", props: ["to"], template: "<a><slot /></a>" } },
    },
  });
  await flushPromises();
  return wrapper;
}

class FakeEventSource {
  private listeners = new Map<string, Array<(event: MessageEvent<string>) => void>>();
  onerror: ((event: Event) => void) | null = null;
  closed = false;

  addEventListener(type: string, listener: EventListenerOrEventListenerObject): void {
    const callback = listener as (event: MessageEvent<string>) => void;
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), callback]);
  }

  emit(type: string, payload: unknown): void {
    const data = typeof payload === "string" ? payload : JSON.stringify(payload);
    for (const listener of this.listeners.get(type) ?? []) {
      listener(new MessageEvent(type, { data }));
    }
  }

  close(): void {
    this.closed = true;
  }
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

  it("parses and filters task logs through backend streams", async () => {
    const rawStream = new FakeEventSource();
    const parsedStream = new FakeEventSource();
    const filteredStream = new FakeEventSource();
    apiMocks.openTaskLogsStream
      .mockReturnValueOnce(rawStream as unknown as EventSource)
      .mockReturnValueOnce(parsedStream as unknown as EventSource)
      .mockReturnValueOnce(filteredStream as unknown as EventSource);

    const wrapper = await mountServiceView();
    await wrapper.get(".service-task-logs-button").trigger("click");

    expect(apiMocks.openTaskLogsStream).toHaveBeenLastCalledWith("task-id", {
      follow: true,
      tail: 200,
      parse: false,
      levels: undefined,
    });
    expect(wrapper.find(".task-log-level-filters").exists()).toBe(false);

    rawStream.emit("log", {
      timestamp: "2026-10-19T18:00:00Z",
      stream: "stderr",
      message: "raw line",
    });
    rawStream.emit("log", {
      timestamp: "2026-10-19T18:00:00.123456Z",
      stream: "stdout",
      message: "health check passed",
    });
    await nextTick();
    expect(wrapper.findAll(".task-log-message").map((message) => message.text())).toEqual([
      "raw line",
      "health check passed",
    ]);
    expect(wrapper.findAll(".task-log-time")[1].text()).toBe("2026-10-19 18:00:00.123");
    expect(wrapper.find(".task-log-stream").exists()).toBe(false);

    const search = wrapper.get<HTMLInputElement>('.task-logs-search input[type="search"]');
    const streamCallsBeforeSearch = apiMocks.openTaskLogsStream.mock.calls.length;
    await search.setValue("health");
    expect(wrapper.findAll(".task-log-message").map((message) => message.text())).toEqual(["health check passed"]);
    expect(apiMocks.openTaskLogsStream).toHaveBeenCalledTimes(streamCallsBeforeSearch);
    await search.setValue("");

    await wrapper.get(".task-logs-parse-toggle").trigger("click");
    expect(rawStream.closed).toBe(true);
    expect(apiMocks.openTaskLogsStream).toHaveBeenLastCalledWith("task-id", {
      follow: true,
      tail: 200,
      parse: true,
      levels: ["TRACE", "DEBUG", "INFO", "WARN", "ERROR", "FATAL"],
    });

    parsedStream.emit("counts", { counts: { INFO: 2, ERROR: 1 } });
    parsedStream.emit("log", {
      timestamp: "2026-10-19T18:00:01Z",
      stream: "stderr",
      level: "ERROR",
      message: "request failed",
      attributes: { request_id: "req-1", context: { tenant: "acme" } },
      raw_message: `level=error msg="request failed" request_id=req-1`,
    });
    parsedStream.emit("log", {
      timestamp: "2026-10-19T18:00:02Z",
      stream: "stdout",
      level: "INFO",
      message: "worker ready",
      attributes: { worker: 12 },
      raw_message: "raw-only-token worker=12",
    });
    await nextTick();

    const filters = wrapper.findAll(".task-log-level-filter");
    expect(filters).toHaveLength(6);
    expect(filters.find((filter) => filter.text().startsWith("All"))?.text()).toBe("All3");
    expect(filters.find((filter) => filter.text().startsWith("Info"))?.text()).toBe("Info2");
    expect(filters.find((filter) => filter.text().startsWith("Error"))?.text()).toBe("Error1");
    expect(wrapper.get(".task-log-level").text()).toBe("ERROR");
    expect(wrapper.get(".task-log-message").text()).toBe("request failed");
    expect(wrapper.find(".task-log-stream").exists()).toBe(false);
    const firstDetails = wrapper.get(".task-log-details");
    await firstDetails.get("summary").trigger("click");
    expect(firstDetails.attributes("open")).toBeDefined();
    expect(firstDetails.findAll(".task-log-attribute").map((attribute) => attribute.text())).toEqual([
      "request_idreq-1",
      'context{"tenant":"acme"}',
    ]);
    expect(firstDetails.get(".task-log-detail-section pre").text()).toContain("level=error");
    expect(firstDetails.get(".task-log-copy").attributes("aria-label")).toBe("Copy raw log 1");

    await search.setValue("raw-only-token");
    expect(wrapper.findAll(".task-log-message").map((message) => message.text())).toEqual(["worker ready"]);
    await search.setValue("acme");
    expect(wrapper.findAll(".task-log-message").map((message) => message.text())).toEqual(["request failed"]);
    await search.setValue("");

    const infoFilter = filters.find((filter) => filter.text().startsWith("Info"));
    expect(infoFilter).toBeDefined();
    await infoFilter!.trigger("click");
    expect(parsedStream.closed).toBe(true);
    expect(apiMocks.openTaskLogsStream).toHaveBeenLastCalledWith("task-id", {
      follow: true,
      tail: 200,
      parse: true,
      levels: ["TRACE", "DEBUG", "WARN", "ERROR", "FATAL"],
    });
  });

  it("shows compact deployment rows and opens details by ID", async () => {
    apiMocks.fetchServiceDeployments.mockResolvedValue({ deployments: [
      { id: "deployment-1", created_at: "2026-10-10T10:00:00Z", status: "succeeded",
        image: "registry.example.com/api:v2", image_version: "v2", commit: "abc123456" },
      { id: "deployment-2", created_at: "2026-10-09T10:00:00Z", status: "failed",
        image: "registry.example.com/api:v2", image_version: "v2", commit: "def123456" },
      { id: "deployment-3", created_at: "2026-10-08T10:00:00Z", status: "running",
        image: "registry.example.com/api:v2", image_version: "v2" },
      { id: "deployment-4", created_at: "2026-10-07T10:00:00Z", status: "interrupted",
        image: "registry.example.com/api:v2", image_version: "v2" },
    ] });
    const wrapper = await mountServiceView();
    expect(wrapper.findAll(".service-panel")).toHaveLength(4);
    expect(wrapper.getComponent({ name: "RouterLink" }).props("to")).toEqual({
      name: "deployments",
      query: { stack_name: "infra", service_name: "api" },
    });
    const rows = wrapper.findAll(".service-deployment-item");
    expect(rows).toHaveLength(4);
    expect(rows[0].text()).toContain("succeeded");
    expect(rows.every((row) => row.text().includes("Image v2"))).toBe(true);
    expect(rows[0].get(".service-deployment-image").attributes("title")).toBe("registry.example.com/api:v2");
    expect(rows[0].text()).not.toContain("infra/api");
    expect(rows[0].get("time").attributes("title")).toBeTruthy();
    expect(rows[0].get(".overview-deployment-result").classes()).toContain("overview-deployment-result--success");
    expect(rows[1].get(".overview-deployment-result").classes()).toContain("overview-deployment-result--failed");
    expect(rows[2].get(".overview-deployment-result").classes()).toContain("overview-deployment-result--running");
    expect(rows[3].get(".overview-deployment-result").classes()).toContain("overview-deployment-result--interrupted");

    const store = useOverviewStore();
    const openDeployment = vi.spyOn(store, "openDeploymentDetailsModal").mockResolvedValue(undefined);
    const openCommit = vi.spyOn(store, "openCommitDetailsModal").mockResolvedValue(undefined);
    const detailsButton = rows[0].get(".overview-deployment-details");
    expect(detailsButton.element.tagName).toBe("BUTTON");
    await detailsButton.trigger("click");
    expect(openDeployment).toHaveBeenCalledWith("deployment-1");
    await rows[0].get(".overview-summary-sha-badge").trigger("click");
    expect(openCommit).toHaveBeenCalledWith("abc123456");
    expect(openDeployment).toHaveBeenCalledTimes(1);
  });
});
