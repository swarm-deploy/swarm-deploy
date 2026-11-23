import { flushPromises, mount } from "@vue/test-utils";
import { afterEach, describe, expect, it } from "vitest";
import { defineComponent, ref } from "vue";
import AppModal from "./AppModal.vue";

const wrappers: Array<{ unmount: () => void }> = [];
afterEach(() => { wrappers.forEach((wrapper) => wrapper.unmount()); wrappers.length = 0; document.body.innerHTML = ""; });

function modal(props = {}) {
  const wrapper = mount(AppModal, {
    props: { open: true, labelledby: "title", ...props },
    slots: { title: '<h2 id="title">Details</h2>', default: '<div class="modal-body"><input aria-label="Value" /><button>Save</button></div>' },
    attachTo: document.body,
  });
  wrappers.push(wrapper);
  return wrapper;
}

function key(key: string, shiftKey = false) {
  document.dispatchEvent(new KeyboardEvent("keydown", { key, shiftKey, bubbles: true, cancelable: true }));
}

describe("AppModal", () => {
  it("closes from the button, backdrop and Escape, while retaining dialog semantics", async () => {
    const wrapper = modal();
    await flushPromises();
    expect(wrapper.get('[role="dialog"]').attributes("aria-labelledby")).toBe("title");
    expect(wrapper.attributes("aria-hidden")).toBe("false");
    await wrapper.get(".modal-close").trigger("click");
    await wrapper.get(".modal-overlay").trigger("click");
    key("Escape");
    expect(wrapper.emitted("close")).toHaveLength(3);
    await wrapper.setProps({ open: false });
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false);
  });

  it("blocks every dismissal route during an operation", async () => {
    const wrapper = modal({ closeDisabled: true });
    await wrapper.get(".modal-overlay").trigger("click");
    key("Escape");
    expect(wrapper.get(".modal-close").attributes("disabled")).toBeDefined();
    expect(wrapper.emitted("close")).toBeUndefined();
    await wrapper.setProps({ closeDisabled: false });
    key("Escape");
    expect(wrapper.emitted("close")).toHaveLength(1);
  });

  it("focuses the requested input, wraps Tab and restores the opener", async () => {
    const opener = document.createElement("button");
    document.body.append(opener);
    opener.focus();
    const wrapper = modal({ initialFocus: "input" });
    await flushPromises();
    expect(document.activeElement).toBe(wrapper.get("input").element);
    wrapper.get<HTMLButtonElement>(".modal-close").element.focus();
    key("Tab", true);
    expect(document.activeElement).toBe(wrapper.get(".modal-body button").element);
    key("Tab");
    expect(document.activeElement).toBe(wrapper.get(".modal-close").element);
    await wrapper.setProps({ open: false });
    expect(document.activeElement).toBe(opener);
  });

  it("only dismisses the top dialog and restores focus to its parent", async () => {
    const host = defineComponent({
      components: { AppModal },
      setup: () => ({ parent: ref(true), child: ref(false) }),
      template: `<AppModal :open="parent" labelledby="parent" @close="parent = false">
        <template #title><h2 id="parent">Parent</h2></template>
        <button id="open-child" @click="child = true">Open child</button>
      </AppModal><AppModal :open="child" labelledby="child" @close="child = false">
        <template #title><h2 id="child">Child</h2></template>
      </AppModal>`,
    });
    const wrapper = mount(host, { attachTo: document.body });
    wrappers.push(wrapper);
    await flushPromises();
    const opener = wrapper.get<HTMLButtonElement>("#open-child");
    opener.element.focus();
    await opener.trigger("click");
    await flushPromises();
    expect(wrapper.findAll(".modal")[0].attributes("inert")).toBeDefined();
    key("Escape");
    await flushPromises();
    expect(wrapper.findAll('[role="dialog"]')).toHaveLength(1);
    expect(wrapper.get(".modal").attributes("inert")).toBeUndefined();
    expect(document.activeElement).toBe(opener.element);
    key("Escape");
    await flushPromises();
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false);
  });

  it("allows an inner popover to consume Escape without dismissing the modal", async () => {
    const wrapper = modal({ closeOnEscape: false });
    key("Escape");
    expect(wrapper.emitted("escape")).toHaveLength(1);
    expect(wrapper.emitted("close")).toBeUndefined();
  });
});
