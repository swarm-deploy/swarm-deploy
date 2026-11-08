import { mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";
import AppSelect from "./AppSelect.vue";

const options = [
  { value: "app", label: "app", description: "Production" },
  { value: "infra", label: "infra", description: "Platform" },
];

describe("AppSelect", () => {
  it("selects a numeric option and returns focus to the trigger", async () => {
    const wrapper = mount(AppSelect, {
      attachTo: document.body,
      props: {
        modelValue: 0,
        options: [{ value: 0, label: "All time" }, { value: 15, label: "Last 15 min" }],
        label: "Time range",
      },
    });
    const details = wrapper.get<HTMLDetailsElement>("details");
    const trigger = wrapper.get("summary");
    expect(trigger.text()).toBe("All time");

    await trigger.trigger("keydown", { key: "ArrowDown" });
    const [allTime, last15] = wrapper.findAll("button.app-select-option");
    expect(details.element.open).toBe(true);
    expect(allTime.attributes("aria-pressed")).toBe("true");
    expect(document.activeElement).toBe(allTime.element);
    await allTime.trigger("keydown", { key: "ArrowDown" });
    expect(document.activeElement).toBe(last15.element);
    await last15.trigger("click");

    expect(wrapper.emitted("update:modelValue")).toEqual([[15]]);
    expect(details.element.open).toBe(false);
    expect(document.activeElement).toBe(trigger.element);
    await wrapper.setProps({ modelValue: 15 });
    expect(trigger.text()).toBe("Last 15 min");
    wrapper.unmount();
  });

  it("selects multiple options and clears the selection", async () => {
    const wrapper = mount(AppSelect, {
      props: { multiple: true, modelValue: [], options, label: "Filter stacks", placeholder: "All stacks", heading: "Stacks" },
    });
    const details = wrapper.get<HTMLDetailsElement>("details");
    details.element.open = true;

    expect(wrapper.get("summary").text()).toContain("All stacks");
    await wrapper.findAll('.app-select-option input')[0].setValue(true);
    expect(wrapper.emitted("update:modelValue")?.[0]).toEqual([["app"]]);
    expect(details.element.open).toBe(true);

    await wrapper.setProps({ modelValue: ["app", "infra"] });
    expect(wrapper.get("summary").text()).toContain("app, infra");
    await wrapper.get(".app-select-header button").trigger("click");
    expect(wrapper.emitted("update:modelValue")?.at(-1)).toEqual([[]]);
    wrapper.unmount();
  });

  it("searches options and closes when clicking outside", async () => {
    const wrapper = mount(AppSelect, {
      attachTo: document.body,
      props: {
        multiple: true, modelValue: [], options, label: "Filter stacks", placeholder: "All stacks", heading: "Stacks",
        searchable: true, searchLabel: "Search stacks",
      },
    });
    const details = wrapper.get<HTMLDetailsElement>("details");
    details.element.open = true;
    await details.trigger("toggle");
    await wrapper.get('input[type="search"]').setValue("plat");
    expect(wrapper.findAll(".app-select-option").map((option) => option.text())).toEqual(["infraPlatform"]);

    document.body.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    expect(details.element.open).toBe(false);
    wrapper.unmount();
  });

  it("supports a descriptive menu without changing selection behavior", () => {
    const wrapper = mount(AppSelect, {
      props: {
        multiple: true, modelValue: [], options, label: "Filter event types", placeholder: "All types", heading: "Event types",
        descriptive: true,
      },
    });

    expect(wrapper.classes()).toContain("app-select--descriptive");
    expect(wrapper.get(".app-select-option small").text()).toBe("Production");
    wrapper.unmount();
  });
});
