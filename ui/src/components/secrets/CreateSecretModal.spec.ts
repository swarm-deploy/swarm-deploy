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
    await wrapper.get('input[type="text"]').setValue("app-token");
    await wrapper.get('textarea[aria-label="Secret value"]').setValue("line one\nline two");
    const description = wrapper.findAll(".create-secret-field").find((field) => field.text().includes("Description"));
    await description!.get("input").setValue("Application token");
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
    await selectCloudMode(wrapper);

    expect(wrapper.text()).toContain("Key");
    expect(wrapper.text()).not.toContain("Description optional");

    await wrapper.get("select").setValue("platform/cloudru-secrets");
    expect(wrapper.text()).not.toContain("Key *");
    expect(wrapper.text()).toContain("Description optional");
  });

  it("shows an unavailable state without disabling Docker Swarm mode", async () => {
    const wrapper = mountModal([{ ...vault, available: false }]);
    await selectCloudMode(wrapper);
    expect(wrapper.text()).toContain("No available, controllable Vault or Cloud.ru Secret Manager");
    expect(wrapper.get<HTMLButtonElement>('button[type="submit"]').element.disabled).toBe(true);

    await wrapper.findAll('[role="tab"]')[0].trigger("click");
    expect(wrapper.text()).toContain("Name");
  });

  it("does not synchronize when external creation fails", async () => {
    vi.mocked(createExternalSecret).mockRejectedValue(new Error("duplicate secret"));
    const wrapper = mountModal();
    await selectCloudMode(wrapper);
    await wrapper.findAll('input[type="text"]')[0].setValue("production/database");
    await wrapper.findAll('input[type="text"]')[1].setValue("password");
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
    await wrapper.findAll('input[type="text"]')[0].setValue("production/database");
    await wrapper.findAll('input[type="text"]')[1].setValue("password");
    await wrapper.get("textarea").setValue("literal-value");
    await wrapper.get("form").trigger("submit");
    await flushPromises();

    expect(createExternalSecret).toHaveBeenCalledWith("security", "vault-secrets", {
      path: "production/database",
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
    await wrapper.findAll('input[type="text"]')[0].setValue("production/database");
    await wrapper.findAll('input[type="text"]')[1].setValue("password");
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
});
