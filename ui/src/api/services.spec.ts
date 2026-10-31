import { afterEach, expect, it, vi } from "vitest";

import { fetchSwarmServices, scaleSwarmService } from "./services";

afterEach(() => vi.unstubAllGlobals());

it("scales by encoded Docker Service ID with an integer JSON body and accepts 204", async () => {
  const fetch = vi.fn().mockResolvedValue({ ok: true, status: 204 });
  vi.stubGlobal("fetch", fetch);
  await expect(scaleSwarmService("service/id", 0)).resolves.toBeUndefined();
  expect(fetch).toHaveBeenCalledExactlyOnceWith("/api/v1/swarm/services/service%2Fid/replicas", {
    method: "PUT", headers: { "Content-Type": "application/json" }, body: '{"replicas":0}',
  });
});

it("preserves the configured count separately from Docker task counts", async () => {
  const services = [{ id: "id", configured_replicas: 4, replicas: { running: 1, desired: 2 } }];
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({ services }) }));
  await expect(fetchSwarmServices()).resolves.toEqual({ services });
});

it("propagates meaningful API conflicts", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
    ok: false, status: 409, json: async () => ({ error_message: "service changed; refresh and retry" }),
  }));
  await expect(scaleSwarmService("id", 5)).rejects.toMatchObject({ status: 409, message: "service changed; refresh and retry" });
});
