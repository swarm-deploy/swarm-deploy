import { config, flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createMemoryHistory, createRouter } from "vue-router";
import { i18n } from "../../i18n";
import { useOverviewStore } from "../../stores/overview";
import CommitDetailsModal from "./CommitDetailsModal.vue";

const assistantStore = vi.hoisted(() => ({
  enabled: true,
  pending: false,
  sendMessage: vi.fn(async () => undefined),
}));

vi.mock("../../stores/assistant", () => ({ useAssistantStore: () => assistantStore }));

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
  let router: ReturnType<typeof createRouter>;
  beforeEach(async () => {
    router = createRouter({ history: createMemoryHistory(), routes: [{ path: "/", component: { template: "<div />" } }] });
    await router.push("/?assistant=previous&tab=services#commit");
    config.global.plugins = [router];
    setActivePinia(createPinia());
    assistantStore.enabled = true;
    assistantStore.pending = false;
    assistantStore.sendMessage.mockClear();
  });

  afterEach(() => {
    config.global.plugins = [];
    i18n.global.locale.value = "en";
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

  it.each([
    { locale: "en", label: "Explain commit", prompt: "Explain changes in git commit: abcdef012345" },
    { locale: "ru", label: "Объяснить коммит", prompt: "Объясни изменения в коммите Git: abcdef012345" },
  ] as const)("opens an assistant chat with the commit prompt in $locale", async ({ locale, label, prompt }) => {
    i18n.global.locale.value = locale;
    const store = openCommitDetails();
    const wrapper = mount(CommitDetailsModal);

    await wrapper.get(`[aria-label="${label}"]`).trigger("click");
    await flushPromises();

    expect(store.commitDetailsModalOpen).toBe(false);
    expect(router.currentRoute.value.fullPath).toBe("/?assistant=new&tab=services#commit");
    expect(assistantStore.sendMessage).toHaveBeenCalledWith(prompt);
    wrapper.unmount();
  });
});
