import { mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";

import AppPanel from "./AppPanel.vue";

describe("AppPanel", () => {
  it("renders optional header and content slots", () => {
    const wrapper = mount(AppPanel, {
      props: {
        tag: "section",
        headerClass: "custom-header",
        contentClass: "custom-content",
      },
      slots: {
        header: "Panel header",
        default: "Panel content",
      },
    });

    expect(wrapper.element.tagName).toBe("SECTION");
    expect(wrapper.get("header").classes()).toContain("custom-header");
    expect(wrapper.get(".app-panel-content").classes()).toContain("custom-content");
  });

  it("omits optional regions when their slots are absent", () => {
    const wrapper = mount(AppPanel, { slots: { default: "Panel content" } });

    expect(wrapper.find("header").exists()).toBe(false);
    expect(wrapper.get(".app-panel-content").text()).toBe("Panel content");
  });
});
