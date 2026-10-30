import { mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";
import { defineComponent, nextTick, ref } from "vue";

import PathInput from "./PathInput.vue";

function mountInput(initialValue = "") {
  return mount(defineComponent({
    components: { PathInput },
    setup() {
      const value = ref(initialValue);
      return { value };
    },
    template: '<PathInput v-model="value" />',
  }));
}

describe("PathInput", () => {
  it("completes segments when a slash is typed", async () => {
    const wrapper = mountInput();
    let input = wrapper.get<HTMLInputElement>('input[aria-label="Path"]');

    await input.setValue("production/");
    expect(wrapper.get(".path-input-segment").text()).toBe("production");
    expect(wrapper.findAll(".path-input-separator")).toHaveLength(1);
    input = wrapper.get<HTMLInputElement>('input[aria-label="Path"]');
    expect(input.element.value).toBe("");

    await input.setValue("demo/");
    expect(wrapper.findAll(".path-input-segment").map((segment) => segment.text())).toEqual([
      "production",
      "demo",
    ]);
    expect(wrapper.findAll(".path-input-separator")).toHaveLength(2);

    input = wrapper.get<HTMLInputElement>('input[aria-label="Path"]');
    await input.setValue("database");
    expect(input.element.value).toBe("database");
    expect(wrapper.getComponent(PathInput).emitted("update:modelValue")?.at(-1)).toEqual([
      "production/demo/database",
    ]);
  });

  it("segments a full path inserted by paste without changing its value", async () => {
    const wrapper = mountInput();
    const input = wrapper.get<HTMLInputElement>('input[aria-label="Path"]');
    input.element.value = "production/demo/database";
    await input.trigger("input", { inputType: "insertFromPaste" });

    expect(wrapper.findAll(".path-input-segment").map((segment) => segment.text())).toEqual([
      "production",
      "demo",
    ]);
    expect(wrapper.get<HTMLInputElement>('input[aria-label="Path"]').element.value).toBe("database");
    expect(wrapper.getComponent(PathInput).emitted("update:modelValue")?.at(-1)).toEqual([
      "production/demo/database",
    ]);
  });

  it("returns to the previous segment when Backspace is pressed on an empty segment", async () => {
    const wrapper = mountInput("production/");
    const input = wrapper.get<HTMLInputElement>('input[aria-label="Path"]');
    input.element.setSelectionRange(0, 0);
    await input.trigger("keydown", { key: "Backspace" });
    await nextTick();

    const previousSegment = wrapper.get<HTMLInputElement>('input[aria-label="Path"]');
    expect(previousSegment.element.value).toBe("production");
    expect(wrapper.find(".path-input-separator").exists()).toBe(false);

    await previousSegment.setValue("productio");
    expect(wrapper.getComponent(PathInput).emitted("update:modelValue")?.at(-1)).toEqual(["productio"]);
  });

  it("allows a previous segment to be selected and edited in place", async () => {
    const wrapper = mountInput("production/demo/database");
    await wrapper.findAll(".path-input-segment")[0].trigger("click");
    await nextTick();

    const input = wrapper.get<HTMLInputElement>('input[aria-label="Path"]');
    expect(input.element.value).toBe("production");
    expect(input.element.selectionStart).toBe(0);
    expect(input.element.selectionEnd).toBe("production".length);

    await input.setValue("staging");
    expect(wrapper.getComponent(PathInput).emitted("update:modelValue")?.at(-1)).toEqual([
      "staging/demo/database",
    ]);
  });
});
