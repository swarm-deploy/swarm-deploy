import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createMemoryHistory, createRouter } from "vue-router";

import { useUIStore } from "../../stores/ui";
import MobileNavigation from "./MobileNavigation.vue";

vi.hoisted(() => {
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: vi.fn().mockReturnValue({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }),
  });
});

function testRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: "/overview", component: { template: "<div />" } },
      { path: "/realtime", component: { template: "<div />" } },
      { path: "/events", component: { template: "<div />" } },
      { path: "/graph", component: { template: "<div />" } },
      { path: "/cluster", component: { template: "<div />" } },
      { path: "/networks", component: { template: "<div />" } },
      { path: "/secrets", component: { template: "<div />" } },
    ],
  });
}

function dispatchPointer(
  element: Element,
  type: string,
  clientY: number,
): void {
  const event = new MouseEvent(type, {
    bubbles: true,
    cancelable: true,
    button: 0,
    clientY,
  });
  Object.defineProperties(event, {
    isPrimary: { value: true },
    pointerId: { value: 1 },
  });
  element.dispatchEvent(event);
}

async function mountMoreNavigation() {
  const router = testRouter();
  await router.push("/overview");
  const wrapper = mount(MobileNavigation, {
    attachTo: document.body,
    props: { assistantEnabled: true },
    global: { plugins: [router] },
  });
  await wrapper.get('button[aria-controls="mobile-more-sheet"]').trigger("click");
  return wrapper;
}

const installButton = 'button[aria-label="Install app"]';

function dispatchInstallPrompt() {
  const prompt = vi.fn().mockResolvedValue({ outcome: "dismissed" });
  const event = new Event("beforeinstallprompt", { cancelable: true });
  window.dispatchEvent(Object.assign(event, { prompt }));
  return prompt;
}

describe("MobileNavigation", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  afterEach(() => {
    Reflect.deleteProperty(navigator, "standalone");
  });

  it("offers the native app prompt only when the browser provides it", async () => {
    const wrapper = await mountMoreNavigation();
    const github = wrapper.get<HTMLAnchorElement>('a[aria-label="swarm-deploy on GitHub"]');
    expect(github.attributes("href")).toBe("https://github.com/swarm-deploy/swarm-deploy");
    expect(wrapper.find(installButton).exists()).toBe(false);

    const prompt = dispatchInstallPrompt();
    await wrapper.vm.$nextTick();
    const install = wrapper.get<HTMLButtonElement>(installButton);
    install.element.focus();
    await install.trigger("click");

    expect(prompt).toHaveBeenCalledOnce();
    expect(wrapper.find(installButton).exists()).toBe(false);
    expect(document.activeElement).toBe(github.element);

    dispatchInstallPrompt();
    await wrapper.vm.$nextTick();
    expect(wrapper.find(installButton).exists()).toBe(true);
    window.dispatchEvent(new Event("appinstalled"));
    await wrapper.vm.$nextTick();
    expect(wrapper.find(installButton).exists()).toBe(false);
    wrapper.unmount();
  });

  it("hides installation in standalone display mode", async () => {
    const display = Object.assign(new EventTarget(), { matches: true });
    vi.mocked(window.matchMedia).mockReturnValueOnce(display as unknown as MediaQueryList);
    const wrapper = await mountMoreNavigation();
    dispatchInstallPrompt();
    await wrapper.vm.$nextTick();

    expect(wrapper.find(installButton).exists()).toBe(false);
    wrapper.unmount();
  });

  it.each([false, true])("handles Safari when navigator.standalone is %s", async (standalone) => {
    Object.defineProperty(navigator, "standalone", { configurable: true, value: standalone });
    const wrapper = await mountMoreNavigation();

    expect(wrapper.find(installButton).exists()).toBe(!standalone);
    if (!standalone) {
      await wrapper.get(installButton).trigger("click");
      expect(wrapper.get(installButton).attributes("aria-expanded")).toBe("true");
      expect(wrapper.get("#mobile-install-help").text()).toContain("Add to Home Screen");
      expect(wrapper.get("#mobile-install-help").isVisible()).toBe(true);
    }

    wrapper.unmount();
  });

  it("opens More and navigates to a secondary destination", async () => {
    const router = testRouter();
    await router.push("/overview");
    await router.isReady();
    const wrapper = mount(MobileNavigation, {
      attachTo: document.body,
      props: { assistantEnabled: true },
      global: { plugins: [router] },
    });

    expect(wrapper.find(".mobile-more-sheet").exists()).toBe(false);
    await wrapper.get('button[aria-controls="mobile-more-sheet"]').trigger("click");

    expect(wrapper.get(".mobile-more-sheet").attributes("role")).toBe("dialog");
    expect(wrapper.find(".mobile-more-handle").exists()).toBe(true);
    expect(wrapper.text()).toContain("Graph");
    expect(wrapper.text()).toContain("Cluster");
    expect(wrapper.text()).toContain("Networks");
    expect(wrapper.text()).toContain("Secrets");

    await wrapper.get('.mobile-more-link[href="/cluster"]').trigger("click");
    await flushPromises();

    expect(router.currentRoute.value.path).toBe("/cluster");
    expect(wrapper.find(".mobile-more-sheet").exists()).toBe(false);
    wrapper.unmount();
  });

  it("keeps primary destinations available while More is open", async () => {
    const router = testRouter();
    await router.push("/overview");
    await router.isReady();
    const wrapper = mount(MobileNavigation, {
      attachTo: document.body,
      props: { assistantEnabled: true },
      global: { plugins: [router] },
    });

    await wrapper.get('button[aria-controls="mobile-more-sheet"]').trigger("click");

    const navigation = wrapper.get(".mobile-navigation");
    expect(navigation.attributes("inert")).toBeUndefined();
    expect(wrapper.get(".mobile-more-sheet").attributes("aria-modal")).toBeUndefined();

    await navigation.get('a[href="/realtime"]').trigger("click");
    await flushPromises();

    expect(router.currentRoute.value.path).toBe("/realtime");
    expect(wrapper.find(".mobile-more-sheet").exists()).toBe(false);
    wrapper.unmount();
  });

  it("closes More when its handle is dragged down", async () => {
    const router = testRouter();
    await router.push("/overview");
    const wrapper = mount(MobileNavigation, {
      attachTo: document.body,
      props: { assistantEnabled: true },
      global: { plugins: [router] },
    });

    await wrapper.get('button[aria-controls="mobile-more-sheet"]').trigger("click");
    const dragZone = wrapper.get(".mobile-more-drag-zone");
    dispatchPointer(dragZone.element, "pointerdown", 100);
    dispatchPointer(dragZone.element, "pointermove", 190);
    await wrapper.vm.$nextTick();

    expect(wrapper.get(".mobile-more-sheet").attributes("style")).toContain("90px");
    dispatchPointer(dragZone.element, "pointerup", 190);
    await wrapper.vm.$nextTick();

    expect(wrapper.find(".mobile-more-sheet").exists()).toBe(false);
    wrapper.unmount();
  });

  it("returns More to its position after a short slow drag", async () => {
    const router = testRouter();
    await router.push("/overview");
    const wrapper = mount(MobileNavigation, {
      attachTo: document.body,
      props: { assistantEnabled: true },
      global: { plugins: [router] },
    });
    await wrapper.get('button[aria-controls="mobile-more-sheet"]').trigger("click");
    const dragZone = wrapper.get(".mobile-more-drag-zone");
    let currentTime = 0;
    const now = vi.spyOn(Date, "now");
    now.mockImplementation(() => currentTime);
    dispatchPointer(dragZone.element, "pointerdown", 100);
    currentTime = 200;
    dispatchPointer(dragZone.element, "pointermove", 130);
    dispatchPointer(dragZone.element, "pointerup", 130);
    await wrapper.vm.$nextTick();

    expect(wrapper.find(".mobile-more-sheet").exists()).toBe(true);
    expect(wrapper.get(".mobile-more-sheet").attributes("style")).toContain("0px");
    now.mockRestore();
    wrapper.unmount();
  });

  it("changes the theme from the More sheet", async () => {
    const router = testRouter();
    await router.push("/overview");
    const wrapper = mount(MobileNavigation, {
      attachTo: document.body,
      props: { assistantEnabled: true },
      global: { plugins: [router] },
    });
    const uiStore = useUIStore();

    await wrapper.get('button[aria-controls="mobile-more-sheet"]').trigger("click");
    await wrapper.get('button[aria-controls="mobile-theme-options"]').trigger("click");
    await wrapper.findAll(".mobile-theme-option")[2].trigger("click");

    expect(uiStore.themeMode).toBe("dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
    wrapper.unmount();
  });

  it("uses the existing assistant launcher", async () => {
    const router = testRouter();
    await router.push("/overview");
    const wrapper = mount(MobileNavigation, {
      props: { assistantEnabled: true },
      global: { plugins: [router] },
    });

    await wrapper.findAll(".mobile-navigation-item")[3].trigger("click");

    expect(wrapper.emitted("toggleAssistant")).toHaveLength(1);
    wrapper.unmount();
  });
});
