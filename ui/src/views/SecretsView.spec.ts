import { createPinia } from "pinia";
import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { createExternalSecret, fetchSecretManagers, fetchSecrets, syncSecretManager } from "../api/secrets";
import type { SecretInfo } from "../api/types";
import SecretsView from "./SecretsView.vue";

vi.mock("../api/secrets", () => ({
  fetchSecrets: vi.fn(),
  fetchSecretManagers: vi.fn(),
  createSecret: vi.fn(),
  createExternalSecret: vi.fn(),
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
    vi.clearAllMocks();
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

  it("refreshes after acknowledging a Cloud Secrets result without a page-wide success alert", async () => {
    vi.mocked(fetchSecretManagers).mockResolvedValue({
      secret_managers: [{
        stack: "security",
        service: "vault-secrets",
        kind: "cloud-secrets",
        controllable: true,
        available: true,
        provider: { name: "HashiCorp Vault" },
      }],
    });
    vi.mocked(createExternalSecret).mockResolvedValue({ path: "normalized/path" });
    vi.mocked(syncSecretManager).mockResolvedValue({ created: 1, updated: 0, removed: 0, unchanged: 0 });
    const wrapper = mount(SecretsView, { global: { plugins: [createPinia()] } });
    await flushPromises();

    await wrapper.get(".secrets-create-button").trigger("click");
    await wrapper.get('input[aria-label="Path"]').setValue("production/database");
    await wrapper.get('input[aria-label="Key"]').setValue("password");
    await wrapper.get('textarea[aria-label="Value"]').setValue("literal-value");
    await wrapper.get(".create-secret-form").trigger("submit");
    await flushPromises();
    await wrapper.get(".create-secret-result-actions button").trigger("click");
    await flushPromises();

    expect(wrapper.find(".create-secret-modal").exists()).toBe(false);
    expect(wrapper.find(".secrets-create-feedback").exists()).toBe(false);
    expect(fetchSecrets).toHaveBeenCalledTimes(2);
  });
});
