import { mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";

import AppTable from "./AppTable.vue";

describe("AppTable", () => {
  it("keeps horizontal scrolling separate from the table layout wrapper", () => {
    const wrapper = mount(AppTable, {
      props: {
        minWidth: "880px",
        tableClass: "test-table",
        wrapClass: "test-wrap",
      },
      slots: {
        head: "<tr><th>Name</th></tr>",
        default: "<tr><td>network</td></tr>",
      },
    });

    expect(wrapper.classes()).toContain("app-table-wrap");
    expect(wrapper.classes()).toContain("test-wrap");
    expect(wrapper.get(".app-table-scroll").element.parentElement).toBe(wrapper.element);

    const table = wrapper.get("table");
    expect(table.classes()).toContain("test-table");
    expect(table.attributes("style")).toContain("min-width: 880px");
  });
});
