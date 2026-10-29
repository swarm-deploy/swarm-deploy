import { mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";

import AppTabs from "./AppTabs.vue";

const tabs = [
  { value: "first", label: "First", id: "first-tab", panelId: "first-panel" },
  { value: "disabled", label: "Disabled", disabled: true },
  { value: "last", label: "Last", id: "last-tab", panelId: "last-panel" },
];

describe("AppTabs", () => {
  it("renders the active tab and emits selections", async () => {
    const wrapper = mount(AppTabs, {
      props: { modelValue: "first", tabs, label: "Example views" },
    });

    expect(wrapper.attributes("role")).toBe("tablist");
    expect(wrapper.attributes("aria-label")).toBe("Example views");
    expect(wrapper.get("#first-tab").attributes("aria-controls")).toBe("first-panel");
    expect(wrapper.get("#first-tab").attributes("aria-selected")).toBe("true");
    expect(wrapper.get("#last-tab").attributes("tabindex")).toBe("-1");

    await wrapper.get("#last-tab").trigger("click");
    expect(wrapper.emitted("update:modelValue")).toEqual([["last"]]);
  });

  it("supports keyboard navigation and skips disabled tabs", async () => {
    const wrapper = mount(AppTabs, {
      attachTo: document.body,
      props: { modelValue: "first", tabs, label: "Example views" },
    });

    await wrapper.get("#first-tab").trigger("keydown", { key: "ArrowRight" });
    expect(wrapper.emitted("update:modelValue")).toEqual([["last"]]);
    expect(document.activeElement).toBe(wrapper.get("#last-tab").element);

    await wrapper.get("#last-tab").trigger("keydown", { key: "Home" });
    expect(wrapper.emitted("update:modelValue")?.at(-1)).toEqual(["first"]);
    expect(document.activeElement).toBe(wrapper.get("#first-tab").element);
    wrapper.unmount();
  });
});
