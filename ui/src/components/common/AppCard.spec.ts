import { mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";

import AppCard from "./AppCard.vue";

describe("AppCard", () => {
  it("renders its slots in a semantic card container", () => {
    const wrapper = mount(AppCard, {
      props: { tag: "section" },
      slots: {
        header: "Card header",
        default: "Card content",
        footer: "Card footer",
      },
    });

    expect(wrapper.element.tagName).toBe("SECTION");
    expect(wrapper.classes()).toContain("app-card");
    expect(wrapper.text()).toBe("Card headerCard contentCard footer");
  });
});
