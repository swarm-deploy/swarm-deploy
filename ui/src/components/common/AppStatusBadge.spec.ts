import { mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";

import AppStatusBadge from "./AppStatusBadge.vue";

describe("AppStatusBadge", () => {
  it("applies the requested visual variant and optional fill", () => {
    const wrapper = mount(AppStatusBadge, {
      props: { variant: "warning", filled: true },
      slots: { default: "Out of sync" },
    });

    expect(wrapper.text()).toBe("Out of sync");
    expect(wrapper.classes()).toEqual(expect.arrayContaining([
      "app-status-badge",
      "app-status-badge--warning",
      "app-status-badge--filled",
    ]));
  });

  it("renders an aria-hidden indicator only when requested", () => {
    const wrapper = mount(AppStatusBadge, {
      props: { dot: true },
      slots: { default: "Unknown" },
    });

    expect(wrapper.get(".app-status-badge-dot").attributes("aria-hidden")).toBe("true");
    expect(wrapper.text()).toBe("Unknown");
  });
});
