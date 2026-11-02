import { flushPromises, mount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { createExternalSecret, createSecret, fetchDSNSuggestions, syncSecretManager } from "../../api/secrets";
import type { SecretManagerInfo } from "../../api/types";
import { copyText } from "../../utils/clipboard";
import CreateSecretModal from "./CreateSecretModal.vue";

vi.mock("../../api/secrets", () => ({
  createSecret: vi.fn(),
  fetchDSNSuggestions: vi.fn(),
  createExternalSecret: vi.fn(),
  syncSecretManager: vi.fn(),
}));
vi.mock("../../utils/clipboard", () => ({ copyText: vi.fn() }));

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

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((promiseResolve) => {
    resolve = promiseResolve;
  });
  return { promise, resolve };
}

async function selectCloudMode(wrapper: ReturnType<typeof mountModal>) {
  await wrapper.findAll('[role="tab"]')[1].trigger("click");
}

describe("CreateSecretModal", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    vi.mocked(fetchDSNSuggestions).mockResolvedValue({ suggestions: [
      { service_id: "pg", service_name: "infra_postgres", stack: "infra", engine: "postgresql" },
      { service_id: "my", service_name: "infra_mysql", stack: "infra", engine: "mysql" },
      { service_id: "unknown", service_name: "custom_database", stack: "", engine: null },
    ] });
    vi.mocked(createSecret).mockResolvedValue({ id: "secret-id", name: "app-token" });
    vi.mocked(createExternalSecret).mockResolvedValue({
      path: "normalized/path",
      manager_url: "https://vault.example.com/ui/vault/secrets/kv/show/normalized/path",
    });
    vi.mocked(syncSecretManager).mockResolvedValue({
      created: 1,
      updated: 0,
      removed: 0,
      unchanged: 0,
      secret: { id: "swarm-secret-id", name: "actual-swarm-secret-name" },
    });
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
    expect(wrapper.get(".path-input-icon").attributes("title")).toBe("Path");

    await wrapper.get("select").setValue("platform/cloudru-secrets");
    expect(wrapper.find('input[aria-label="Key"]').exists()).toBe(false);
    expect(wrapper.find('input[aria-label="Description"]').exists()).toBe(true);
    expect(wrapper.get(".path-input-icon").attributes("title")).toBe("Key");
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
    expect(wrapper.find("form").exists()).toBe(true);
    expect(wrapper.find(".create-secret-result").exists()).toBe(false);
  });

  it("shows the provider result after external creation and synchronization", async () => {
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
    expect(syncSecretManager).toHaveBeenCalledWith("security", "vault-secrets", {
      path: "normalized/path",
      key: "password",
    });
    expect(wrapper.emitted("created")).toBeUndefined();
    expect(wrapper.text()).toContain("Secret created successfully");
    expect(wrapper.text()).toContain("The secret was created in HashiCorp Vault and synchronized to Docker Swarm.");
    expect(wrapper.text()).toContain("Docker Swarm secret");
    expect(wrapper.text()).toContain("actual-swarm-secret-name");
    expect(wrapper.text()).toContain("Completed");
    expect(wrapper.text()).not.toContain("literal-value");
    expect(wrapper.get(".create-secret-result-link").attributes()).toMatchObject({
      href: "https://vault.example.com/ui/vault/secrets/kv/show/normalized/path",
      target: "_blank",
      rel: "noopener noreferrer",
    });

    const copyButton = wrapper.get(".create-secret-result-copy");
    expect(copyButton.attributes("type")).toBe("button");
    expect(copyButton.attributes("aria-label")).toBe("Copy Docker Swarm secret name");
    await copyButton.trigger("click");
    await flushPromises();
    expect(copyText).toHaveBeenCalledWith("actual-swarm-secret-name");
    expect(copyButton.attributes("title")).toBe("Copied");

    await wrapper.get(".create-secret-result-actions button").trigger("click");
    expect(wrapper.emitted("created")?.[0]).toEqual([]);
  });

  it("retries only Sync after partial success", async () => {
    vi.mocked(syncSecretManager)
      .mockRejectedValueOnce(new Error("sync already in progress"))
      .mockResolvedValueOnce({
        created: 1,
        updated: 0,
        removed: 0,
        unchanged: 0,
        secret: { id: "retried-id", name: "retried-swarm-secret" },
      });
    const wrapper = mountModal();
    await selectCloudMode(wrapper);
    await wrapper.get('input[aria-label="Path"]').setValue("production/database");
    await wrapper.get('input[aria-label="Key"]').setValue("password");
    await wrapper.get("textarea").setValue("literal-value");
    await wrapper.get("form").trigger("submit");
    await flushPromises();

    expect(wrapper.text()).toContain("Secret created, but synchronization failed");
    expect(wrapper.text()).toContain("The secret was created in HashiCorp Vault, but could not be synchronized to Docker Swarm.");
    expect(wrapper.text()).toContain("External secret path");
    expect(wrapper.text()).toContain("normalized/path/password");
    expect(wrapper.text()).toContain("Failed to synchronize the secret to Docker Swarm.");
    expect(wrapper.text()).not.toContain("sync already in progress");
    expect(wrapper.get(".create-secret-result-link").attributes("href")).toBe(
      "https://vault.example.com/ui/vault/secrets/kv/show/normalized/path",
    );
    const retryButton = wrapper.findAll("button").find((button) => button.text() === "Retry Sync");
    expect(retryButton).toBeDefined();

    await retryButton!.trigger("click");
    await flushPromises();
    expect(createExternalSecret).toHaveBeenCalledOnce();
    expect(syncSecretManager).toHaveBeenCalledTimes(2);
    expect(wrapper.text()).toContain("Secret created successfully");
    expect(wrapper.text()).toContain("Docker Swarm secret");
    expect(wrapper.text()).toContain("retried-swarm-secret");
    expect(wrapper.text()).not.toContain("External secret path");
    expect(wrapper.text()).toContain("Completed");
  });

  it("shows the real create and synchronization stages without duplicate submission", async () => {
    const create = deferred<{ path: string; manager_url: string }>();
    const sync = deferred<{
      created: number;
      updated: number;
      removed: number;
      unchanged: number;
      secret: { id: string; name: string };
    }>();
    vi.mocked(createExternalSecret).mockReturnValue(create.promise);
    vi.mocked(syncSecretManager).mockReturnValue(sync.promise);
    const wrapper = mountModal();
    await wrapper.get('input[aria-label="Path"]').setValue("production/database");
    await wrapper.get('input[aria-label="Key"]').setValue("password");
    await wrapper.get("textarea").setValue("literal-value");
    await wrapper.get("form").trigger("submit");

    expect(wrapper.text()).toContain("Creating secret…");
    expect(wrapper.get<HTMLButtonElement>('button[type="submit"]').element.disabled).toBe(true);
    await wrapper.get("form").trigger("submit");
    expect(createExternalSecret).toHaveBeenCalledOnce();

    create.resolve({
      path: "production/database",
      manager_url: "https://vault.example.com/ui/vault/secrets/kv/show/production/database",
    });
    await flushPromises();
    expect(wrapper.text()).toContain("Synchronizing to Docker Swarm…");
    expect(wrapper.find("form").exists()).toBe(false);

    sync.resolve({
      created: 1,
      updated: 0,
      removed: 0,
      unchanged: 0,
      secret: { id: "staged-id", name: "staged-secret" },
    });
    await flushPromises();
    expect(wrapper.text()).toContain("Completed");
  });

  it.each([
    { name: "missing", managerUrl: undefined },
    { name: "unsafe", managerUrl: "javascript:alert(1)" },
  ])("omits a $name manager URL without fabricating a fallback link", async ({ managerUrl }) => {
    vi.mocked(createExternalSecret).mockResolvedValue({
      path: "normalized/path",
      ...(managerUrl ? { manager_url: managerUrl } : {}),
    });
    const wrapper = mountModal();
    await wrapper.get('input[aria-label="Path"]').setValue("production/database");
    await wrapper.get('input[aria-label="Key"]').setValue("password");
    await wrapper.get("textarea").setValue("literal-value");
    await wrapper.get("form").trigger("submit");
    await flushPromises();

    expect(wrapper.text()).toContain("Secret created successfully");
    expect(wrapper.find(".create-secret-result-link").exists()).toBe(false);
  });

  it("does not guess a Docker Swarm name when synchronization cannot identify one", async () => {
    vi.mocked(syncSecretManager).mockResolvedValue({ created: 1, updated: 0, removed: 0, unchanged: 0 });
    const wrapper = mountModal();
    await wrapper.get('input[aria-label="Path"]').setValue("production/database");
    await wrapper.get('input[aria-label="Key"]').setValue("password");
    await wrapper.get("textarea").setValue("literal-value");
    await wrapper.get("form").trigger("submit");
    await flushPromises();

    expect(wrapper.text()).toContain("External secret path");
    expect(wrapper.text()).toContain("normalized/path/password");
    expect(wrapper.text()).toContain("Docker Swarm resource could not be identified.");
    expect(wrapper.find(".create-secret-result-copy").exists()).toBe(false);
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

  async function openDSN(wrapper: ReturnType<typeof mountModal>) {
    await wrapper.findAll(".create-secret-mode-toggle button")[2].trigger("click");
    await flushPromises();
  }

  it("loads database suggestions lazily and generates PostgreSQL and MySQL DSNs reactively", async () => {
    const wrapper = mountModal();
    expect(fetchDSNSuggestions).not.toHaveBeenCalled();
    await openDSN(wrapper);
    expect(fetchDSNSuggestions).toHaveBeenCalledOnce();
    expect(wrapper.get('select[aria-label="Database service"]').findAll("option")).toHaveLength(4);
    await wrapper.get('select[aria-label="Database service"]').setValue("pg");
    const final = () => wrapper.get<HTMLInputElement>('input[aria-label="Secret value (DSN)"]').element.value;
    expect(final()).toBe("postgresql://app:@infra_postgres:5432/app");
    await wrapper.get('input[aria-label="Database name"]').setValue("orders");
    await wrapper.get('input[aria-label="User"]').setValue("reader");
    await wrapper.get('input[aria-label="Password"]').setValue("example");
    expect(final()).toBe("postgresql://reader:example@infra_postgres:5432/orders");
    await wrapper.get('select[aria-label="Database service"]').setValue("my");
    expect(final()).toBe("mysql://reader:example@infra_mysql:3306/orders");
  });

  it("percent-encodes credentials and database names and saves exactly the displayed value", async () => {
    const wrapper = mountModal();
    await wrapper.findAll('[role="tab"]')[0].trigger("click");
    await wrapper.get('input[aria-label="Name"]').setValue("app-dsn");
    await openDSN(wrapper);
    await wrapper.get('select[aria-label="Database service"]').setValue("pg");
    await wrapper.get('input[aria-label="Database name"]').setValue("sales/北 ?#%");
    await wrapper.get('input[aria-label="User"]').setValue("app@team");
    await wrapper.get('input[aria-label="Password"]').setValue("P@ss:/?#%!'()*");
    const expected = "postgresql://app%40team:P%40ss%3A%2F%3F%23%25%21%27%28%29%2A@infra_postgres:5432/sales%2F%E5%8C%97%20%3F%23%25";
    expect(wrapper.get<HTMLInputElement>('input[aria-label="Secret value (DSN)"]').element.value).toBe(expected);
    await wrapper.get("form").trigger("submit");
    await flushPromises();
    expect(createSecret).toHaveBeenCalledWith({ name: "app-dsn", value: expected });
  });

  it("preserves a manual DSN until explicitly regenerating, including across value modes", async () => {
    const wrapper = mountModal();
    await openDSN(wrapper);
    await wrapper.get('select[aria-label="Database service"]').setValue("pg");
    const input = () => wrapper.get<HTMLInputElement>('input[aria-label="Secret value (DSN)"]');
    const manual = "postgresql://custom:example@infra_postgres:5432/manual?sslmode=require";
    await input().setValue(manual);
    await wrapper.get('input[aria-label="Database name"]').setValue("changed");
    await wrapper.get('input[aria-label="Password"]').setValue("changed");
    await wrapper.get('select[aria-label="Database service"]').setValue("my");
    expect(input().element.value).toBe(manual);
    await wrapper.findAll(".create-secret-mode-toggle button")[0].trigger("click");
    expect(wrapper.get<HTMLTextAreaElement>("textarea").element.value).toBe(manual);
    await openDSN(wrapper);
    expect(input().element.value).toBe(manual);
    expect(fetchDSNSuggestions).toHaveBeenCalledOnce();
    await wrapper.get(".create-secret-dsn-value-label button").trigger("click");
    expect(input().element.value).toBe("mysql://app:changed@infra_mysql:3306/changed");
    await wrapper.get('input[aria-label="User"]').setValue("reactive");
    expect(input().element.value).toContain("mysql://reactive:changed@");
  });

  it("saves the exact manually edited DSN through Cloud Secrets", async () => {
    const wrapper = mountModal();
    await wrapper.get('input[aria-label="Path"]').setValue("production/db");
    await wrapper.get('input[aria-label="Key"]').setValue("dsn");
    await openDSN(wrapper);
    const manual = "mysql://example:example@infra_mysql:3306/app?charset=utf8mb4";
    await wrapper.get('input[aria-label="Secret value (DSN)"]').setValue(manual);
    await wrapper.get("form").trigger("submit");
    await flushPromises();
    expect(createExternalSecret).toHaveBeenCalledWith("security", "vault-secrets", { path: "production/db", key: "dsn", value: manual });
  });

  it("uses the existing Reveal action for both DSN and password", async () => {
    const wrapper = mountModal();
    await openDSN(wrapper);
    const password = wrapper.get('input[aria-label="Password"]');
    const final = wrapper.get('input[aria-label="Secret value (DSN)"]');
    expect(password.attributes("type")).toBe("password");
    expect(final.attributes("type")).toBe("password");
    await wrapper.get(".create-secret-small-action").trigger("click");
    expect(password.attributes("type")).toBe("text");
    expect(final.attributes("type")).toBe("text");
    await wrapper.get(".create-secret-small-action").trigger("click");
    expect(final.attributes("type")).toBe("password");
  });

  it("clears a generated value for an unknown engine and permits manual entry", async () => {
    const wrapper = mountModal();
    await openDSN(wrapper);
    await wrapper.get('select[aria-label="Database service"]').setValue("pg");
    await wrapper.get('select[aria-label="Database service"]').setValue("unknown");
    const final = wrapper.get<HTMLInputElement>('input[aria-label="Secret value (DSN)"]');
    expect(final.element.value).toBe("");
    expect(wrapper.text()).toContain("Engine could not be identified");
    await final.setValue("custom://manual");
    await wrapper.get('input[aria-label="User"]').setValue("new");
    expect(final.element.value).toBe("custom://manual");
  });

  it("preserves existing Text/JSON content when entering DSN mode", async () => {
    const wrapper = mountModal();
    const original = '{"dsn":"manual"}';
    await wrapper.get("textarea").setValue(original);
    await wrapper.findAll(".create-secret-mode-toggle button")[1].trigger("click");
    await openDSN(wrapper);
    await wrapper.get('select[aria-label="Database service"]').setValue("pg");
    expect(wrapper.get<HTMLInputElement>('input[aria-label="Secret value (DSN)"]').element.value).toBe(original);
  });

  it("shows loading, empty, and retry states while retaining manual input", async () => {
    const response = deferred<{ suggestions: [] }>();
    vi.mocked(fetchDSNSuggestions).mockReturnValueOnce(response.promise);
    const wrapper = mountModal();
    await openDSN(wrapper);
    expect(wrapper.get<HTMLSelectElement>('select[aria-label="Database service"]').element.disabled).toBe(true);
    expect(wrapper.text()).toContain("Loading database services");
    await wrapper.get('input[aria-label="Secret value (DSN)"]').setValue("manual://value");
    response.resolve({ suggestions: [] });
    await flushPromises();
    expect(wrapper.text()).toContain("No database services found");
    await wrapper.setProps({ open: false });
    await wrapper.setProps({ open: true });
    vi.mocked(fetchDSNSuggestions).mockRejectedValueOnce(new Error("upstream details"));
    await openDSN(wrapper);
    expect(wrapper.text()).toContain("Unable to load database services");
    expect(wrapper.text()).not.toContain("upstream details");
    await wrapper.get(".create-secret-dsn-action").trigger("click");
    await flushPromises();
    expect(wrapper.findAll('select[aria-label="Database service"] option')).toHaveLength(4);
  });

});
