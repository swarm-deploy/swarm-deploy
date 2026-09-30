import { mount } from "@vue/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { SecretManagerInfo } from "../../api/types";
import SecretManagerCard from "./SecretManagerCard.vue";

function manager(overrides: Partial<SecretManagerInfo> = {}): SecretManagerInfo {
  return {
    stack: "infra",
    service: "cloud-secrets",
    kind: "cloud-secrets",
    controllable: true,
    available: true,
    provider: {
      name: "Cloud.ru Secret Manager",
      links: {
        doc: "https://cloud.ru/docs/secret-manager/",
        manager: "https://console.cloud.ru/spa/secret-manager/list?projectId=project-id",
      },
    },
    last_sync_at: "2026-09-30T00:58:00Z",
    next_sync_at: "2026-09-30T01:03:00Z",
    ...overrides,
  };
}

describe("SecretManagerCard", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-09-30T01:00:00Z"));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders the compact Healthy resource card", () => {
    const wrapper = mount(SecretManagerCard, {
      props: { manager: manager(), managedCount: 12 },
    });

    expect(wrapper.get(".secret-manager-card-title").text()).toBe("Cloud.ru Secret Manager");
    expect(wrapper.text()).toContain("Provides externally managed secrets");
    expect(wrapper.text()).toContain("12 managed secrets");
    expect(wrapper.get(".secret-manager-sync-time-desktop").text()).toBe("Last synced 2 min ago");
    expect(wrapper.get(".secret-manager-sync-time-mobile").text()).toBe("2 min ago");
    expect(wrapper.get(".secret-manager-next-sync-time .secret-manager-sync-time-desktop").text()).toBe("Next sync in 3 min");
    expect(wrapper.get(".secret-manager-next-sync-time .secret-manager-sync-time-mobile").text()).toBe("in 3 min");
    expect(wrapper.get(".secret-manager-badge--healthy").text()).toBe("Healthy");
    expect(wrapper.get(".secret-manager-docs").attributes()).toMatchObject({
      href: "https://cloud.ru/docs/secret-manager/",
      target: "_blank",
      rel: "noopener noreferrer",
    });
    expect(wrapper.get(".secret-manager-manage").attributes()).toMatchObject({
      href: "https://console.cloud.ru/spa/secret-manager/list?projectId=project-id",
      target: "_blank",
      rel: "noopener noreferrer",
    });
    expect(wrapper.text()).not.toContain("Some secrets are managed by");
  });

  it("omits missing optional metadata without rendering undefined", () => {
    const wrapper = mount(SecretManagerCard, {
      props: {
        manager: manager({ provider: undefined, last_sync_at: undefined, next_sync_at: undefined, version: undefined }),
        managedCount: 0,
      },
    });

    expect(wrapper.get(".secret-manager-card-title").text()).toBe("cloud-secrets Secret Manager");
    expect(wrapper.text()).toContain("Never synced");
    expect(wrapper.text()).toContain("0 managed secrets");
    expect(wrapper.text()).not.toContain("undefined");
    expect(wrapper.find(".secret-manager-docs").exists()).toBe(false);
    expect(wrapper.find(".secret-manager-manage").exists()).toBe(false);
    expect(wrapper.find(".secret-manager-message").exists()).toBe(false);
    expect(wrapper.find(".secret-manager-next-sync-time").exists()).toBe(false);
  });

  it.each([
    {
      links: { doc: "https://cloud.ru/docs/secret-manager/" },
      hasDocs: true,
      hasManage: false,
    },
    {
      links: { manager: "https://console.cloud.ru/secret-manager" },
      hasDocs: false,
      hasManage: true,
    },
    {
      links: undefined,
      hasDocs: false,
      hasManage: false,
    },
  ])("renders only available provider links", ({ links, hasDocs, hasManage }) => {
    const wrapper = mount(SecretManagerCard, {
      props: {
        manager: manager({ provider: { name: "Cloud.ru Secret Manager", links } }),
        managedCount: 12,
      },
    });

    expect(wrapper.find(".secret-manager-docs").exists()).toBe(hasDocs);
    expect(wrapper.find(".secret-manager-manage").exists()).toBe(hasManage);
  });

  it("renders and disables the Syncing state", () => {
    const wrapper = mount(SecretManagerCard, {
      props: { manager: manager(), managedCount: 12, syncing: true },
    });

    expect(wrapper.get(".secret-manager-badge--syncing").text()).toBe("Syncing…");
    expect(wrapper.get<HTMLButtonElement>(".secret-manager-sync").element.disabled).toBe(true);
    expect(wrapper.get(".secret-manager-sync").text()).toBe("Syncing…");
    expect(wrapper.findAll(".secret-manager-spinner")).toHaveLength(2);
  });

  it.each([
    {
      label: "Error",
      props: { manager: manager(), managedCount: 12, syncError: "Sync failed" },
      badgeClass: ".secret-manager-badge--error",
      message: "Sync failed",
      disabled: false,
    },
    {
      label: "Unavailable",
      props: {
        manager: manager({ available: false, error: "Controller is unavailable" }),
        managedCount: 12,
        syncError: "",
      },
      badgeClass: ".secret-manager-badge--unavailable",
      message: "Controller is unavailable",
      disabled: true,
    },
  ])("renders the $label state", ({ label, props, badgeClass, message, disabled }) => {
    const wrapper = mount(SecretManagerCard, { props });

    expect(wrapper.get(badgeClass).text()).toBe(label);
    expect(wrapper.get(".secret-manager-message").text()).toBe(message);
    expect(wrapper.get<HTMLButtonElement>(".secret-manager-sync").element.disabled).toBe(disabled);
  });

  it("provides separate compact mobile metadata without changing the content hierarchy", () => {
    const wrapper = mount(SecretManagerCard, {
      props: { manager: manager(), managedCount: 1 },
    });

    expect(wrapper.find(".secret-manager-card-header").exists()).toBe(true);
    expect(wrapper.find(".secret-manager-card-footer").exists()).toBe(true);
    expect(wrapper.get(".secret-manager-sync-time-desktop").text()).toBe("Last synced 2 min ago");
    expect(wrapper.get(".secret-manager-sync-time-mobile").text()).toBe("2 min ago");
    expect(wrapper.text()).toContain("1 managed secret");
  });
});
