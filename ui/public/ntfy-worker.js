// The registered scope is the installed app's base path, including its trailing slash.
function appURL(target) {
  const scope = new URL(self.registration.scope);
  const fallback = new URL("events", scope).href;
  if (!target || typeof target !== "string") return fallback;
  try {
    const url = new URL(target, scope);
    if (url.origin !== scope.origin || !url.pathname.startsWith(scope.pathname) || url.username || url.password) return fallback;
    return url.href;
  } catch {
    return fallback;
  }
}

self.addEventListener("push", (event) => {
  if (!event.data) return;
  const payload = event.data.json();
  const msg = payload.message;
  const icon = new URL("icons/icon-192.png", self.registration.scope).href;
  if (payload.event === "subscription_expiring") {
    const title = new URL(self.location.href).searchParams.get("renew");
    event.waitUntil(self.registration.showNotification(title, {
      icon, data: appURL(),
    }));
    return;
  }
  if (payload.event !== "message" || msg?.event !== "message") return;
  let body = msg.message;
  let url = msg.click;
  // ntfy templates can render the message body, including a per-event deep link.
  try {
    const content = JSON.parse(body);
    if (typeof content?.body === "string") {
      body = content.body;
      url = content.url ?? url;
    }
  } catch { /* Plain ntfy messages use the native click field. */ }
  event.waitUntil(self.registration.showNotification(msg.title || msg.topic, {
    body, icon, tag: msg.id, data: appURL(url),
  }));
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  event.waitUntil(clients.openWindow(appURL(event.notification.data)));
});

self.addEventListener("pushsubscriptionchange", (event) => {
  event.waitUntil(renewSubscription(event));
});

async function renewSubscription(event) {
  const topic = new URL(self.location.href).searchParams.get("topic");
  const api = new URL("ntfy/v1/webpush", self.registration.scope);
  const old = event.oldSubscription;
  const sub = event.newSubscription ?? (old && await self.registration.pushManager.subscribe(old.options));
  if (!sub || !topic) return;
  const options = { credentials: "same-origin", redirect: "error", headers: { "Content-Type": "application/json" } };
  const response = await fetch(api, { ...options, method: "POST",
    body: JSON.stringify({ endpoint: sub.endpoint, ...sub.toJSON().keys, topics: [topic] }),
  });
  if (response.ok && (await response.json()).success && old && old.endpoint !== sub.endpoint) {
    await fetch(api, { ...options, method: "DELETE", body: JSON.stringify({ endpoint: old.endpoint }) });
  }
}
