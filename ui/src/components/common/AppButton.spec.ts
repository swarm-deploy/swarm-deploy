import { mount, RouterLinkStub } from "@vue/test-utils";
import { describe, expect, it } from "vitest";

import AppButton from "./AppButton.vue";

describe("AppButton", () => {
  it("renders a primary button by default and emits native clicks", async () => {
    const wrapper = mount(AppButton, { slots: { default: "Sync now" } });

    expect(wrapper.element.tagName).toBe("BUTTON");
    expect(wrapper.attributes("type")).toBe("button");
    expect(wrapper.classes()).toEqual(expect.arrayContaining(["app-button", "app-button--primary"]));

    await wrapper.trigger("click");
    expect(wrapper.emitted("click")).toHaveLength(1);
  });

  it("supports secondary, danger, compact, submit, and disabled states", () => {
    const wrapper = mount(AppButton, {
      props: { variant: "danger", compact: true, type: "submit" },
      attrs: { disabled: true },
      slots: { default: "Remove" },
    });

    expect(wrapper.attributes("type")).toBe("submit");
    expect(wrapper.attributes()).toHaveProperty("disabled");
    expect(wrapper.classes()).toEqual(expect.arrayContaining([
      "app-button--danger",
      "app-button--compact",
    ]));
  });

  it("renders navigation actions as links", () => {
    const wrapper = mount(AppButton, {
      props: { to: "/alerts", variant: "secondary" },
      slots: { default: "View alerts" },
      global: { stubs: { RouterLink: RouterLinkStub } },
    });

    expect(wrapper.element.tagName).toBe("A");
    expect(wrapper.getComponent(RouterLinkStub).props("to")).toBe("/alerts");
    expect(wrapper.classes()).toContain("app-button--secondary");
  });
});
