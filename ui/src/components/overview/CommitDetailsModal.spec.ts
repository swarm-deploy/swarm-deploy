import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useOverviewStore } from "../../stores/overview";
import CommitDetailsModal from "./CommitDetailsModal.vue";

const assistantStore = vi.hoisted(() => ({
  enabled: true,
  pending: false,
  newChat: vi.fn(),
  sendMessage: vi.fn(async () => undefined),
}));
const uiStore = vi.hoisted(() => ({ openAssistantDrawer: vi.fn() }));

vi.mock("../../stores/assistant", () => ({ useAssistantStore: () => assistantStore }));
vi.mock("../../stores/ui", () => ({ useUIStore: () => uiStore }));

function openCommitDetails() {
  const store = useOverviewStore();
  store.commitDetailsModalOpen = true;
  store.commitDetailsHash = "abcdef012345";
  store.commitDetailsData = {
    full_hash: "abcdef012345",
    author: "Developer",
    message: "Keep the explanation action compact",
    date: "2026-10-10T10:00:00Z",
    changed_files: ["ui/src/components/overview/CommitDetailsModal.vue"],
  };
  return store;
}

describe("Commit Details modal", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    assistantStore.enabled = true;
    assistantStore.pending = false;
    assistantStore.newChat.mockClear();
    assistantStore.sendMessage.mockClear();
    uiStore.openAssistantDrawer.mockClear();
  });

  it("shows an icon-only explanation action immediately before close", () => {
    openCommitDetails();
    const wrapper = mount(CommitDetailsModal);
    const actions = wrapper.findAll(".modal-header button");
    const explain = actions[0];

    expect(actions.map((button) => button.attributes("aria-label"))).toEqual(["Explain commit", "Close modal"]);
    expect(explain.attributes("title")).toBe("Explain commit");
    expect(explain.text()).toBe("");
    expect(explain.get("svg").attributes("aria-hidden")).toBe("true");
    expect(wrapper.text()).not.toContain("Explain with AI");
    wrapper.unmount();
  });

  it("keeps the action keyboard-focusable and reflects its disabled state", async () => {
    const store = openCommitDetails();
    const wrapper = mount(CommitDetailsModal, { attachTo: document.body });
    const explain = wrapper.get<HTMLButtonElement>('[aria-label="Explain commit"]');

    explain.element.focus();
    expect(document.activeElement).toBe(explain.element);

    assistantStore.enabled = false;
    store.commitDetailsData = { ...store.commitDetailsData! };
    await wrapper.vm.$nextTick();
    expect(explain.element.disabled).toBe(true);
    wrapper.unmount();
  });

  it("preserves the existing AI explanation behavior", async () => {
    const store = openCommitDetails();
    const wrapper = mount(CommitDetailsModal);

    await wrapper.get('[aria-label="Explain commit"]').trigger("click");
    await flushPromises();

    expect(store.commitDetailsModalOpen).toBe(false);
    expect(assistantStore.newChat).toHaveBeenCalledOnce();
    expect(uiStore.openAssistantDrawer).toHaveBeenCalledOnce();
    expect(assistantStore.sendMessage).toHaveBeenCalledWith("Explain changes in git commit: abcdef012345");
    expect(assistantStore.newChat.mock.invocationCallOrder[0]).toBeLessThan(
      assistantStore.sendMessage.mock.invocationCallOrder[0],
    );
    wrapper.unmount();
  });
});
