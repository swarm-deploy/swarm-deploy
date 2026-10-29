import { mount } from "@vue/test-utils";
import { nextTick } from "vue";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { SwarmTaskDetails } from "../../api/types";
import TaskDetailsPanel from "./TaskDetailsPanel.vue";

const secret = "synthetic-password=значение";
const encodedSecret = btoa(String.fromCharCode(...new TextEncoder().encode(secret)));

function task(id = "task-one"): SwarmTaskDetails {
  return {
    id,
    service_id: "service-one",
    node_id: "node-one",
    slot: 1,
    desired_state: "running",
    current_state: "running",
    created_at: "2026-10-28T20:00:00Z",
    updated_at: "2026-10-28T20:01:00Z",
    container_id: "container-one",
    image: "example/api:v1",
    environment: [
      { name: "PLAIN", value: "left=right", encoded: false },
      { name: "EMPTY", value: "", encoded: false },
      { name: "SECRET", value: encodedSecret, encoded: true },
    ],
  };
}

describe("TaskDetailsPanel", () => {
  const writeText = vi.fn().mockResolvedValue(undefined);

  beforeEach(() => {
    vi.stubGlobal("isSecureContext", true);
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    writeText.mockClear();
  });

  afterEach(() => vi.unstubAllGlobals());

  it("renders the compact sections without a Task Details logs action or repeated slot field", () => {
    const wrapper = mount(TaskDetailsPanel, { props: { task: task(), loading: false, error: "" } });

    expect(wrapper.get(".realtime-task-panel-header").text()).toContain("Slot 1");
    expect(wrapper.find("#task-overview-title").exists()).toBe(false);
    expect(wrapper.find(".realtime-logs-button").exists()).toBe(false);
    expect(wrapper.get(".realtime-task-runtime").findAll(":scope > div")).toHaveLength(3);
    expect(wrapper.get(".realtime-task-runtime-state").classes()).not.toContain("app-status-badge--filled");
    expect(wrapper.get(".realtime-task-identifiers").text()).not.toContain("Slot");
  });

  it("does not present a running task's absent or default exit code as zero", async () => {
    const running = task();
    running.exit_code = 0;
    const wrapper = mount(TaskDetailsPanel, { props: { task: running, loading: false, error: "" } });

    expect(wrapper.get(".realtime-task-runtime > div:nth-child(3) dd").text()).toBe("—");

    await wrapper.setProps({ task: { ...running, current_state: "complete" } });
    expect(wrapper.get(".realtime-task-runtime > div:nth-child(3) dd").text()).toBe("0");

    await wrapper.setProps({ task: { ...running, current_state: "failed", exit_code: undefined } });
    expect(wrapper.get(".realtime-task-runtime > div:nth-child(3) dd").text()).toBe("—");
  });

  it.each([
    {
      reference: "registry.example.test:5000/team/api:v1.5.0",
      name: "registry.example.test:5000/team/api",
      tag: "v1.5.0",
      digest: "",
    },
    {
      reference: "registry.example.test:5000/team/api",
      name: "registry.example.test:5000/team/api",
      tag: "",
      digest: "",
    },
    {
      reference: "team/api@sha256:1234567890abcdef1234567890abcdef",
      name: "team/api",
      tag: "",
      digest: "sha256:12345678...0abcdef",
    },
    {
      reference: "team/api:stable@sha256:abcdef1234567890abcdef1234567890",
      name: "team/api",
      tag: "stable",
      digest: "sha256:abcdef12...4567890",
    },
  ])("parses image reference $reference without inventing missing parts", ({ reference, name, tag, digest }) => {
    const wrapper = mount(TaskDetailsPanel, {
      props: { task: { ...task(), image: reference }, loading: false, error: "" },
    });

    expect(wrapper.get(".realtime-task-image-primary code").text()).toBe(name);
    const tagNode = wrapper.find(".realtime-task-image-tag");
    const digestNode = wrapper.find(".realtime-task-image-digest");
    expect(tagNode.exists()).toBe(Boolean(tag));
    expect(digestNode.exists()).toBe(Boolean(digest));
    if (tag) expect(tagNode.text()).toBe(tag);
    if (digest) expect(digestNode.text()).toBe(digest);
    expect(wrapper.get(".realtime-task-image-primary").find(".realtime-detail-copy-button").exists()).toBe(true);
    expect(wrapper.find(".realtime-task-image-meta").exists()).toBe(Boolean(tag || digest));
  });

  it("copies complete image references and identifiers from icon buttons", async () => {
    const image = "registry.example.test:5000/very/long/repository/api:v1@sha256:abcdef1234567890";
    const id = "task-0123456789abcdefghijklmnopqrstuvwxyz";
    const wrapper = mount(TaskDetailsPanel, {
      props: { task: { ...task(id), image }, loading: false, error: "" },
    });

    expect(wrapper.get(".realtime-task-image-primary code").attributes("title")).toBe("registry.example.test:5000/very/long/repository/api");
    await wrapper.get('[aria-label="Copy full image reference"]').trigger("click");
    expect(writeText).toHaveBeenLastCalledWith(image);
    expect(wrapper.find('[aria-label="Image reference copied"] .realtime-detail-copy-check').exists()).toBe(true);

    expect(wrapper.get('.realtime-task-identifiers code[title^="task-"]').attributes("title")).toBe(id);
    await wrapper.get('[aria-label="Copy task id"]').trigger("click");
    expect(writeText).toHaveBeenLastCalledWith(id);
    expect(wrapper.find('[aria-label="Task ID copied"] .realtime-detail-copy-check').exists()).toBe(true);
    expect(wrapper.text()).not.toContain("Click to copy");
  });

  it("shows missing image and identifier values without copy actions", () => {
    const wrapper = mount(TaskDetailsPanel, {
      props: {
        task: { ...task(), image: "", service_id: "", node_id: "", container_id: "" },
        loading: false,
        error: "",
      },
    });

    expect(wrapper.find('[aria-label="Copy full image reference"]').exists()).toBe(false);
    expect(wrapper.find('[aria-label="Copy service id"]').exists()).toBe(false);
    expect(wrapper.find('[aria-label="Copy node id"]').exists()).toBe(false);
    expect(wrapper.find('[aria-label="Copy container id"]').exists()).toBe(false);
    expect(wrapper.findAll(".realtime-task-missing").length).toBeGreaterThanOrEqual(4);
  });

  it("shows plain and empty values while keeping encoded values out of the DOM by default", () => {
    const wrapper = mount(TaskDetailsPanel, { props: { task: task(), loading: false, error: "" } });

    expect(wrapper.text()).toContain("left=right");
    expect(wrapper.text()).toContain("(empty)");
    expect(wrapper.html()).not.toContain(secret);
    expect(wrapper.find('[aria-label="Show value for SECRET"]').exists()).toBe(true);
    expect(wrapper.get(".realtime-environment-count").text()).toBe("3");
  });

  it("decodes UTF-8 Base64 for reveal and copies the original decoded value", async () => {
    const wrapper = mount(TaskDetailsPanel, { props: { task: task(), loading: false, error: "" } });

    await wrapper.get('[aria-label="Show value for SECRET"]').trigger("click");
    expect(wrapper.text()).toContain(secret);
    expect(wrapper.find('[aria-label="Hide value for SECRET"]').exists()).toBe(true);

    await wrapper.get('[aria-label="Copy value for SECRET"]').trigger("click");
    expect(writeText).toHaveBeenCalledWith(secret);
    expect(wrapper.find('[aria-label="Value copied for SECRET"] .realtime-environment-copy-check').exists()).toBe(true);

    await wrapper.get('[aria-label="Hide value for SECRET"]').trigger("click");
    expect(wrapper.html()).not.toContain(secret);
    expect(wrapper.find('[aria-label="Show value for SECRET"]').exists()).toBe(true);
  });

  it("hides revealed sensitive values when switching tasks", async () => {
    const wrapper = mount(TaskDetailsPanel, { props: { task: task(), loading: false, error: "" } });
    await wrapper.get('[aria-label="Show value for SECRET"]').trigger("click");
    expect(wrapper.text()).toContain(secret);

    await wrapper.setProps({ task: task("task-two") });
    await nextTick();
    expect(wrapper.html()).not.toContain(secret);
    expect(wrapper.find('[aria-label="Show value for SECRET"]').exists()).toBe(true);
  });

  it("filters variables by name case-insensitively while keeping the total count", async () => {
    const wrapper = mount(TaskDetailsPanel, { props: { task: task(), loading: false, error: "" } });
    await wrapper.get<HTMLInputElement>('.realtime-env-search input').setValue("plain");
    expect(wrapper.findAll(".realtime-environment-row")).toHaveLength(1);
    expect(wrapper.text()).toContain("PLAIN");
    expect(wrapper.get(".realtime-environment-count").text()).toBe("3");
  });

  it("copies the variable value and does not offer a copy-name action", async () => {
    const wrapper = mount(TaskDetailsPanel, { props: { task: task(), loading: false, error: "" } });

    await wrapper.get('[aria-label="Copy value for PLAIN"]').trigger("click");

    expect(writeText).toHaveBeenCalledWith("left=right");
    expect(wrapper.find('[aria-label="Value copied for PLAIN"] .realtime-environment-copy-check').exists()).toBe(true);
    expect(wrapper.find('[aria-label^="Copy variable name"]').exists()).toBe(false);
  });

  it("shows an empty state when no variable names match", async () => {
    const wrapper = mount(TaskDetailsPanel, { props: { task: task(), loading: false, error: "" } });

    await wrapper.get<HTMLInputElement>('.realtime-env-search input').setValue("missing-variable");

    expect(wrapper.findAll(".realtime-environment-row")).toHaveLength(0);
    expect(wrapper.text()).toContain("No variables match the search.");
  });
});
