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
