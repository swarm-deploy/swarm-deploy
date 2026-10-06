import { createPinia } from "pinia";
import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { fetchSecretManagers, fetchSecrets } from "../api/secrets";
import type { SecretInfo } from "../api/types";
import SecretsView from "./SecretsView.vue";

vi.mock("../api/secrets", () => ({
  fetchSecrets: vi.fn(),
  fetchSecretManagers: vi.fn(),
  syncSecretManager: vi.fn(),
}));

const secrets: SecretInfo[] = [
  {
    id: "alpha",
    name: "Alpha",
    description: "Zebra",
    version_id: 1,
    created_at: "2026-01-03T00:00:00Z",
    external: { path: "/middle", version_id: "10" },
  },
  {
    id: "bravo",
    name: "Bravo",
    description: "Apple",
    version_id: 1,
    created_at: "2026-01-01T00:00:00Z",
    external: { path: "/zulu", version_id: "2" },
  },
  {
    id: "charlie",
    name: "Charlie",
    description: "Middle",
    version_id: 1,
    created_at: "2026-01-02T00:00:00Z",
    external: { path: "/alpha", version_id: "1" },
  },
];

function renderedNames(wrapper: ReturnType<typeof mount>): string[] {
  return wrapper.findAll("tbody tr td:first-child").map((cell) => cell.text());
}

describe("SecretsView sorting", () => {
  beforeEach(() => {
    vi.mocked(fetchSecrets).mockResolvedValue({ secrets });
    vi.mocked(fetchSecretManagers).mockResolvedValue({ secret_managers: [] });
  });

  it.each([
    { label: "Name", ascending: ["Alpha", "Bravo", "Charlie"], descending: ["Charlie", "Bravo", "Alpha"] },
    { label: "Description", ascending: ["Bravo", "Charlie", "Alpha"], descending: ["Alpha", "Charlie", "Bravo"] },
    { label: "Date Added", ascending: ["Bravo", "Charlie", "Alpha"], descending: ["Alpha", "Charlie", "Bravo"] },
    { label: "External Path", ascending: ["Charlie", "Alpha", "Bravo"], descending: ["Bravo", "Alpha", "Charlie"] },
    { label: "External Version ID", ascending: ["Charlie", "Bravo", "Alpha"], descending: ["Alpha", "Bravo", "Charlie"] },
  ])("sorts the $label column in both directions", async ({ label, ascending, descending }) => {
    const wrapper = mount(SecretsView, { global: { plugins: [createPinia()] } });
    await flushPromises();

    const button = wrapper.findAll("thead button").find((item) => item.text().startsWith(label));
    expect(button).toBeDefined();

    if (label === "Name") {
      expect(renderedNames(wrapper)).toEqual(ascending);
      await button!.trigger("click");
      expect(renderedNames(wrapper)).toEqual(descending);
      expect(button!.element.closest("th")?.getAttribute("aria-sort")).toBe("descending");
      return;
    }

    await button!.trigger("click");
    expect(renderedNames(wrapper)).toEqual(ascending);
    expect(button!.element.closest("th")?.getAttribute("aria-sort")).toBe("ascending");

    await button!.trigger("click");
    expect(renderedNames(wrapper)).toEqual(descending);
    expect(button!.element.closest("th")?.getAttribute("aria-sort")).toBe("descending");
  });
});
