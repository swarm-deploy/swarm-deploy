import { mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { useSecretDetailsStore } from "../../stores/secretDetails";
import SecretDetailsModal from "./SecretDetailsModal.vue";

const push = vi.fn();

vi.mock("vue-router", () => ({
  useRouter: () => ({ push }),
}));

function mountModal(usedBy: Array<{ stack: string; service: string; target?: string }>) {
  const pinia = createPinia();
  setActivePinia(pinia);
  const store = useSecretDetailsStore();
  store.$patch({
    modalOpen: true,
    secret: {
      id: "secret-id",
      name: "db-password",
      version_id: 1,
      created_at: "2026-09-30T00:00:00Z",
      updated_at: "2026-09-30T00:00:00Z",
      used_by: usedBy,
    },
  });

  return { wrapper: mount(SecretDetailsModal, { global: { plugins: [pinia] } }), store };
}

describe("SecretDetailsModal", () => {
  beforeEach(() => {
    push.mockReset();
  });

  it("renders service usage and navigates to the service page", async () => {
    const { wrapper, store } = mountModal([
      { stack: "payments", service: "api", target: "/run/secrets/db-password" },
    ]);

    expect(wrapper.text()).toContain("Used by services");
    expect(wrapper.text()).toContain("payments / api");
    expect(wrapper.text()).toContain("/run/secrets/db-password");

    await wrapper.get(".secret-used-by-service").trigger("click");

    expect(push).toHaveBeenCalledWith({
      name: "service-details",
      params: { stack: "payments", service: "api" },
    });
    expect(store.modalOpen).toBe(false);
  });

  it("renders an explicit empty state for an unused secret", () => {
    const { wrapper } = mountModal([]);

    expect(wrapper.text()).toContain("Not used by any service");
    expect(wrapper.find(".secret-used-by-service").exists()).toBe(false);
  });
});
