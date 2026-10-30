import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { createExternalSecret, createSecret, syncSecretManager } from "../../api/secrets";
import type { SecretManagerInfo } from "../../api/types";
import CreateSecretModal from "./CreateSecretModal.vue";

vi.mock("../../api/secrets", () => ({
  createSecret: vi.fn(),
  createExternalSecret: vi.fn(),
  syncSecretManager: vi.fn(),
}));

const vault: SecretManagerInfo = {
  stack: "security",
  service: "vault-secrets",
  kind: "cloud-secrets",
  controllable: true,
  available: true,
  provider: { name: "HashiCorp Vault" },
};
const cloudru: SecretManagerInfo = {
  stack: "platform",
  service: "cloudru-secrets",
  kind: "cloud-secrets",
  controllable: true,
  available: true,
  provider: { name: "Cloud.ru Secret Manager" },
};

function mountModal(managers: SecretManagerInfo[] = [vault, cloudru]) {
  return mount(CreateSecretModal, { props: { open: true, managers } });
}

async function selectCloudMode(wrapper: ReturnType<typeof mountModal>) {
  await wrapper.findAll('[role="tab"]')[1].trigger("click");
}

describe("CreateSecretModal", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    vi.mocked(createSecret).mockResolvedValue({ id: "secret-id", name: "app-token" });
    vi.mocked(createExternalSecret).mockResolvedValue({ path: "normalized/path" });
    vi.mocked(syncSecretManager).mockResolvedValue({ created: 1, updated: 0, removed: 0, unchanged: 0 });
  });

  it("creates a Docker Swarm secret with the exact multiline payload", async () => {
    const wrapper = mountModal();
    await wrapper.findAll('[role="tab"]')[0].trigger("click");
    await wrapper.get('input[aria-label="Name"]').setValue("app-token");
    await wrapper.get('textarea[aria-label="Value"]').setValue("line one\nline two");
    await wrapper.get('input[aria-label="Description"]').setValue("Application token");
    await wrapper.get("form").trigger("submit");
    await flushPromises();

    expect(createSecret).toHaveBeenCalledWith({
      name: "app-token",
      value: "line one\nline two",
      description: "Application token",
    });
    expect(wrapper.emitted("created")?.[0]).toEqual(["Docker secret app-token created."]);
  });

  it("shows provider-specific Vault and Cloud.ru fields", async () => {
    const wrapper = mountModal();

    expect(wrapper.get('[role="tab"][aria-selected="true"]').text()).toBe("Cloud Secrets");

    expect(wrapper.find('input[aria-label="Key"]').exists()).toBe(true);
    expect(wrapper.find('input[aria-label="Description"]').exists()).toBe(false);

    await wrapper.get("select").setValue("platform/cloudru-secrets");
    expect(wrapper.find('input[aria-label="Key"]').exists()).toBe(false);
    expect(wrapper.find('input[aria-label="Description"]').exists()).toBe(true);
  });

  it("shows an unavailable state without disabling Docker Swarm mode", async () => {
    const wrapper = mountModal([{ ...vault, available: false }]);
    await selectCloudMode(wrapper);
    expect(wrapper.text()).toContain("No available, controllable Vault or Cloud.ru Secret Manager");
    expect(wrapper.get<HTMLButtonElement>('button[type="submit"]').element.disabled).toBe(true);

    await wrapper.findAll('[role="tab"]')[0].trigger("click");
    expect(wrapper.find('input[aria-label="Name"]').exists()).toBe(true);
  });

  it("does not synchronize when external creation fails", async () => {
    vi.mocked(createExternalSecret).mockRejectedValue(new Error("duplicate secret"));
    const wrapper = mountModal();
    await selectCloudMode(wrapper);
    await wrapper.get('input[aria-label="Path"]').setValue("production/database");
    await wrapper.get('input[aria-label="Key"]').setValue("password");
    await wrapper.get("textarea").setValue("value");
    await wrapper.get("form").trigger("submit");
    await flushPromises();

    expect(createExternalSecret).toHaveBeenCalledOnce();
    expect(syncSecretManager).not.toHaveBeenCalled();
    expect(wrapper.text()).toContain("duplicate secret");
  });

  it("creates externally and then synchronizes", async () => {
    const wrapper = mountModal();
    await selectCloudMode(wrapper);
    await wrapper.get('input[aria-label="Path"]').setValue("production/demo/database");
    await wrapper.get('input[aria-label="Key"]').setValue("password");
    await wrapper.get("textarea").setValue("literal-value");
    await wrapper.get("form").trigger("submit");
    await flushPromises();

    expect(createExternalSecret).toHaveBeenCalledWith("security", "vault-secrets", {
      path: "production/demo/database",
      key: "password",
      value: "literal-value",
    });
    expect(syncSecretManager).toHaveBeenCalledWith("security", "vault-secrets");
    expect(wrapper.emitted("created")?.[0]).toEqual([
      "Secret created in the external provider and synchronization completed.",
    ]);
  });

  it("retries only Sync after partial success", async () => {
    vi.mocked(syncSecretManager)
      .mockRejectedValueOnce(new Error("sync already in progress"))
      .mockResolvedValueOnce({ created: 1, updated: 0, removed: 0, unchanged: 0 });
    const wrapper = mountModal();
    await selectCloudMode(wrapper);
    await wrapper.get('input[aria-label="Path"]').setValue("production/database");
    await wrapper.get('input[aria-label="Key"]').setValue("password");
    await wrapper.get("textarea").setValue("literal-value");
    await wrapper.get("form").trigger("submit");
    await flushPromises();

    expect(wrapper.text()).toContain("Secret created in the external provider, but synchronization with Docker Swarm failed.");
    expect(wrapper.text()).toContain("normalized/path");
    expect(wrapper.get('button[type="submit"]').text()).toBe("Retry Sync");

    await wrapper.get("form").trigger("submit");
    await flushPromises();
    expect(createExternalSecret).toHaveBeenCalledOnce();
    expect(syncSecretManager).toHaveBeenCalledTimes(2);
  });

  it("preserves content across Text and JSON modes, validates, and formats only on request", async () => {
    const wrapper = mountModal();
    let textarea = wrapper.get<HTMLTextAreaElement>("textarea");
    const original = '{"nested":{"enabled":true}}';
    await textarea.setValue(original);
    await wrapper.findAll(".create-secret-mode-toggle button")[1].trigger("click");
    textarea = wrapper.get<HTMLTextAreaElement>("textarea");
    expect(textarea.element.value).toBe(original);

    await textarea.setValue('{"broken":');
    expect(wrapper.text()).toContain("Invalid JSON");
    expect(wrapper.get<HTMLButtonElement>('button[type="submit"]').element.disabled).toBe(true);

    await textarea.setValue(original);
    expect(textarea.element.value).toBe(original);
    await wrapper.findAll(".create-secret-small-action")[0].trigger("click");
    expect(textarea.element.value).toBe('{\n  "nested": {\n    "enabled": true\n  }\n}');
  });

  it("uses accessible names and empty-state placeholders without visible field labels", () => {
    const wrapper = mountModal();

    expect(wrapper.get('select[aria-label="Secret Manager"]').attributes("required")).toBeDefined();
    expect(wrapper.get('input[aria-label="Path"]').attributes("placeholder")).toBe("Path to secret");
    expect(wrapper.get('input[aria-label="Key"]').attributes("placeholder")).toBe("Secret key");
    expect(wrapper.get('textarea[aria-label="Value"]').attributes("required")).toBeDefined();
    expect(wrapper.find(".create-secret-field").exists()).toBe(false);
  });

  it("places the optional Description control before Value", async () => {
    const wrapper = mountModal();
    await wrapper.get("select").setValue("platform/cloudru-secrets");

    const description = wrapper.get('input[aria-label="Description"]');
    const valueEditor = wrapper.get('textarea[aria-label="Value"]');
    expect(description.attributes("placeholder")).toBe("Description (optional)");
    expect(description.attributes("required")).toBeUndefined();
    expect(description.element.compareDocumentPosition(valueEditor.element) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
});
