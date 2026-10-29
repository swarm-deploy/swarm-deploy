import { mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";
import AppMultiSelect from "./AppMultiSelect.vue";

const options = [
  { value: "app", label: "app", description: "Production" },
  { value: "infra", label: "infra", description: "Platform" },
];

describe("AppMultiSelect", () => {
  it("selects multiple options and clears the selection", async () => {
    const wrapper = mount(AppMultiSelect, {
      props: { modelValue: [], options, label: "Filter stacks", placeholder: "All stacks", heading: "Stacks" },
    });

    expect(wrapper.get("summary").text()).toContain("All stacks");
    await wrapper.findAll('.app-multi-select-option input')[0].setValue(true);
    expect(wrapper.emitted("update:modelValue")?.[0]).toEqual([["app"]]);

    await wrapper.setProps({ modelValue: ["app", "infra"] });
    expect(wrapper.get("summary").text()).toContain("app, infra");
    await wrapper.get(".app-multi-select-header button").trigger("click");
    expect(wrapper.emitted("update:modelValue")?.at(-1)).toEqual([[]]);
  });

  it("searches options and closes when clicking outside", async () => {
    const wrapper = mount(AppMultiSelect, {
      attachTo: document.body,
      props: {
        modelValue: [], options, label: "Filter stacks", placeholder: "All stacks", heading: "Stacks",
        searchable: true, searchLabel: "Search stacks",
      },
    });
    const details = wrapper.get<HTMLDetailsElement>("details");
    details.element.open = true;
    await details.trigger("toggle");
    await wrapper.get('input[type="search"]').setValue("plat");
    expect(wrapper.findAll(".app-multi-select-option").map((option) => option.text())).toEqual(["infraPlatform"]);

    document.body.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    expect(details.element.open).toBe(false);
    wrapper.unmount();
  });

  it("supports a descriptive menu without changing selection behavior", () => {
    const wrapper = mount(AppMultiSelect, {
      props: {
        modelValue: [], options, label: "Filter event types", placeholder: "All types", heading: "Event types",
        descriptive: true,
      },
    });

    expect(wrapper.classes()).toContain("app-multi-select--descriptive");
    expect(wrapper.get(".app-multi-select-option small").text()).toBe("Production");
  });
});
