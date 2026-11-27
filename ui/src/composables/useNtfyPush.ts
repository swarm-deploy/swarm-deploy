import { ref } from "vue";
import { t } from "../i18n";
import { getNtfyTopic, withWebBasePath } from "../runtimeConfig";

const enabled = ref(false);
const denied = ref(false);
const busy = ref(false);
const error = ref(false);

function supported(): boolean {
  return Boolean(getNtfyTopic() && window.isSecureContext && "serviceWorker" in navigator &&
    "PushManager" in window && "Notification" in window);
}

async function request(path: string, init?: RequestInit) {
  const response = await fetch(withWebBasePath(`/ntfy/v1/${path}`), {
    credentials: "same-origin", redirect: "error", ...init,
  });
  if (!response.ok) throw new Error(`ntfy returned ${response.status}`);
  return response.json();
}

async function sync(method: "POST" | "DELETE", subscription: PushSubscription) {
  const data = subscription.toJSON();
  const body = method === "POST"
    ? { endpoint: subscription.endpoint, ...data.keys, topics: [getNtfyTopic()] }
    : { endpoint: subscription.endpoint };
  const result = await request("webpush", {
    method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body),
  });
  if (!result.success) throw new Error("ntfy did not accept the subscription");
}

function appScope(): string {
  return new URL(withWebBasePath("/"), location.origin).href;
}

async function registration() {
  const scope = appScope();
  const existing = await navigator.serviceWorker.getRegistration(scope);
  if (existing?.scope !== scope) return undefined;
  const worker = existing.active ?? existing.waiting ?? existing.installing;
  if (worker && new URL(worker.scriptURL).pathname !== withWebBasePath("/ntfy-worker.js")) {
    throw new Error("Another worker owns this scope");
  }
  return existing;
}

async function registerWorker() {
  const script = new URL("ntfy-worker.js", appScope());
  script.searchParams.set("topic", getNtfyTopic());
  script.searchParams.set("renew", t("webPush.renew"));
  await navigator.serviceWorker.register(script, { scope: appScope() });
}

export async function refreshPushSubscription() {
  if (!supported() || busy.value) return;
  busy.value = true;
  error.value = false;
  try {
    denied.value = Notification.permission === "denied";
    const sub = await (await registration())?.pushManager.getSubscription();
    enabled.value = Boolean(sub);
    if (sub && !denied.value) {
      await registerWorker();
      await sync("POST", sub);
    }
  } catch {
    error.value = true;
  } finally {
    busy.value = false;
  }
}

async function subscribe() {
  // Ask inside the button gesture (required on iOS).
  const permission = await Notification.requestPermission();
  denied.value = permission === "denied";
  if (permission !== "granted") return;

  const config = await request("config");
  if (!config.enable_web_push || !config.web_push_public_key) throw new Error("Web Push is disabled");
  await registration();
  await registerWorker();
  const reg = await navigator.serviceWorker.ready;
  if (reg.scope !== appScope()) throw new Error("Unexpected service worker scope");

  const existing = await reg.pushManager.getSubscription();
  const sub = existing ?? await reg.pushManager.subscribe({
    userVisibleOnly: true, applicationServerKey: config.web_push_public_key,
  });
  try {
    await sync("POST", sub);
  } catch (cause) {
    if (!existing) await sub.unsubscribe();
    throw cause;
  }
  enabled.value = true;
}

async function toggle() {
  if (busy.value) return;
  busy.value = true;
  error.value = false;
  try {
    if (enabled.value) {
      const sub = await (await registration())?.pushManager.getSubscription();
      if (sub) {
        await sync("DELETE", sub);
        await sub.unsubscribe();
      }
      enabled.value = false;
    } else {
      await subscribe();
    }
  } catch {
    error.value = true;
  } finally {
    busy.value = false;
  }
}

export function useNtfyPush() {
  return { available: supported(), enabled, denied, busy, error, toggle };
}
