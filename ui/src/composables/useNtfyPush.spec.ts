import { afterEach, expect, it, vi } from "vitest";
import { refreshPushSubscription, useNtfyPush } from "./useNtfyPush";

afterEach(() => {
  vi.unstubAllGlobals();
  document.getElementById("swarm-deploy-runtime-config")?.remove();
});

it("registers with ntfy, refreshes a live subscription and removes it on opt-out under a nested base path", async () => {
  const config = document.createElement("script");
  config.id = "swarm-deploy-runtime-config";
  config.type = "application/json";
  config.textContent = JSON.stringify({ basePath: "/ops/swarm", ntfyTopic: "deploys" });
  document.head.append(config);
  const keys = { auth: "auth", p256dh: "key" };
  const sub = { endpoint: "https://push.example/sub", toJSON: () => ({ keys }), unsubscribe: vi.fn() };
  const pushManager = { getSubscription: vi.fn(), subscribe: vi.fn().mockResolvedValue(sub) };
  const scope = new URL("/ops/swarm/", location.origin).href;
  const reg = { scope, pushManager, active: { scriptURL: scope + "ntfy-worker.js" } };
  const sw = { getRegistration: vi.fn().mockResolvedValue(reg), register: vi.fn(), ready: Promise.resolve(reg) };
  const fetcher = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ success: true }) });
  vi.stubGlobal("isSecureContext", true);
  vi.stubGlobal("PushManager", class {});
  vi.stubGlobal("Notification", { permission: "default", requestPermission: vi.fn().mockResolvedValue("granted") });
  vi.stubGlobal("navigator", { serviceWorker: sw });
  vi.stubGlobal("fetch", fetcher);

  await refreshPushSubscription();
  expect(fetcher).not.toHaveBeenCalled();
  const push = useNtfyPush();
  fetcher.mockResolvedValueOnce({ ok: true, json: async () => ({ enable_web_push: true, web_push_public_key: "vapid" }) });
  await push.toggle();
  expect(sw.register.mock.calls[0][0].pathname).toBe("/ops/swarm/ntfy-worker.js");
  expect(pushManager.subscribe).toHaveBeenCalledWith({ userVisibleOnly: true, applicationServerKey: "vapid" });
  expect(fetcher).toHaveBeenLastCalledWith("/ops/swarm/ntfy/v1/webpush", expect.objectContaining({
    method: "POST", body: JSON.stringify({ endpoint: sub.endpoint, ...keys, topics: ["deploys"] }),
  }));
  expect(push.enabled.value).toBe(true);

  pushManager.getSubscription.mockResolvedValue(sub);
  await refreshPushSubscription();
  expect(fetcher.mock.calls.at(-1)?.[1].method).toBe("POST");
  await push.toggle();
  expect(fetcher.mock.calls.at(-1)?.[1].method).toBe("DELETE");
  expect(sub.unsubscribe).toHaveBeenCalledOnce();
  expect(push.enabled.value).toBe(false);
});

it("does not contact ntfy when the feature is unconfigured", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  expect(useNtfyPush().available).toBe(false);
  await refreshPushSubscription();
  expect(fetcher).not.toHaveBeenCalled();
});
