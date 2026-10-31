import { flushPromises, mount } from "@vue/test-utils";
import { createMemoryHistory, createRouter } from "vue-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchRecommendations } from "../api/overview";
import type { Recommendation } from "../api/types";
import RecommendationsView from "./RecommendationsView.vue";

vi.mock("../api/overview", () => ({ fetchRecommendations: vi.fn() }));
const recommendation = (stack: string): Recommendation => ({
  severity: "medium", type: "service.image.latest", subject: { stack, service: "api" },
  source: { file: "app.yaml", digest: "sha256:abc", commit: "abcdef" },
  title: `Pin ${stack} image`, recommendation: `Pin ${stack} image`, created_at: "2026-10-10T10:00:00Z",
});
async function render(path = "/recommendations?stack_name=app&keep=value") {
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: "/recommendations", component: RecommendationsView },
  ] });
  await router.push(path);
  const wrapper = mount(RecommendationsView, { global: { plugins: [router] } });
  await flushPromises();
  return { wrapper, router };
}

describe("Recommendation stack links", () => {
  beforeEach(() => {
    vi.mocked(fetchRecommendations).mockReset();
    vi.mocked(fetchRecommendations).mockResolvedValue({ recommendations: [recommendation("app"), recommendation("infra")] });
  });
  it("applies the URL stack filter and keeps dropdown and URL in sync", async () => {
    const { wrapper, router } = await render();
    expect(wrapper.get(".recommendations-filter-dropdown summary").text()).toContain("app");
    expect(wrapper.text()).toContain("Pin app image");
    expect(wrapper.text()).not.toContain("Pin infra image");
    await wrapper.findAll(".recommendations-search-filter-menu button")
      .find((button) => button.text() === "infra")!
      .trigger("click");
    await flushPromises();
    expect(router.currentRoute.value.query).toEqual({ stack_name: "infra", keep: "value" });
    expect(wrapper.text()).toContain("Pin infra image");
    expect(wrapper.text()).not.toContain("Pin app image");
    await router.push("/recommendations?stack_name=app");
    await flushPromises();
    expect(wrapper.get(".recommendations-filter-dropdown summary").text()).toContain("app");
    await wrapper.findAll(".recommendations-search-filter-menu button")[0].trigger("click");
    await flushPromises();
    expect(router.currentRoute.value.query.stack_name).toBeUndefined();
    expect(wrapper.text()).toContain("Pin infra image");
    wrapper.unmount();
  });
  it("preserves a linked stack with no recommendations instead of showing unrelated items", async () => {
    const { wrapper, router } = await render("/recommendations?stack_name=empty");
    expect(wrapper.get(".recommendations-filter-dropdown summary").text()).toContain("empty");
    expect(wrapper.text()).toContain("No recommendations match the selected filters.");
    expect(wrapper.find("tbody").exists()).toBe(false);
    expect(router.currentRoute.value.query.stack_name).toBe("empty");
    wrapper.unmount();
  });
  it("renders icon-led filters and supports searchable stack options", async () => {
    const { wrapper } = await render("/recommendations");
    const controls = wrapper.findAll(".recommendations-filter-bar summary, .recommendations-filter-bar select");

    expect(controls.map((control) => control.attributes("aria-label"))).toEqual([
      "Filter recommendations by stack",
      "Filter recommendations by severity",
    ]);
    expect(wrapper.findAll(".recommendations-filter-icon")).toHaveLength(2);
    expect(wrapper.find(".recommendations-filter-field > span").exists()).toBe(false);
    expect(wrapper.get(".recommendations-mobile-filter-toggle").text()).toBe("Filters (0)");

    await wrapper.get<HTMLInputElement>('.recommendations-search-filter-menu input[type="search"]').setValue("infra");
    expect(wrapper.findAll(".recommendations-search-filter-menu button").map((button) => button.text())).toEqual([
      "All stacks",
      "infra",
    ]);
    wrapper.unmount();
  });
  it("closes the stack filter when clicking outside", async () => {
    const { wrapper } = await render();
    const stackFilter = wrapper.get<HTMLDetailsElement>(".recommendations-filter-dropdown");
    stackFilter.element.open = true;

    document.body.dispatchEvent(new MouseEvent("click", { bubbles: true }));

    expect(stackFilter.element.open).toBe(false);
    wrapper.unmount();
  });
});
