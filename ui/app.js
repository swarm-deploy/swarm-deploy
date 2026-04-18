const stacksEl = document.getElementById("stacks");
const syncStatusEl = document.getElementById("sync-status");
const syncNowBtn = document.getElementById("sync-now");
const showEventsBtn = document.getElementById("show-events");
const serviceStatusModalEl = document.getElementById("service-status-modal");
const serviceStatusBodyEl = document.getElementById("service-status-body");
const serviceStatusTitleEl = document.getElementById("service-status-title");
const serviceStatusCloseBtn = document.getElementById("service-status-close");
const eventHistoryModalEl = document.getElementById("event-history-modal");
const eventHistoryBodyEl = document.getElementById("event-history-body");
const eventHistoryCloseBtn = document.getElementById("event-history-close");
const assistantChat = window.createAssistantChat();
const eventDetailsPriority = ["stack", "commit", "destination", "channel", "event_type", "error"];

function fmtDate(raw) {
  if (!raw) {
    return "n/a";
  }
  const d = new Date(raw);
  if (Number.isNaN(d.valueOf())) {
    return raw;
  }
  return d.toLocaleString();
}

function fmtBytes(value) {
  if (value === undefined || value === null || Number.isNaN(Number(value))) {
    return "n/a";
  }
  const bytes = Number(value);
  const units = ["B", "KB", "MB", "GB", "TB"];
  let idx = 0;
  let amount = bytes;
  while (amount >= 1024 && idx < units.length - 1) {
    amount /= 1024;
    idx += 1;
  }
  return `${amount.toFixed(idx === 0 ? 0 : 2)} ${units[idx]}`;
}

function escapeHtml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll("\"", "&quot;")
    .replaceAll("'", "&#39;");
}

function sortEventDetails(detailPairs) {
  const weightByKey = Object.fromEntries(eventDetailsPriority.map((key, idx) => [key, idx]));
  return detailPairs.sort(([leftKey], [rightKey]) => {
    const leftWeight = weightByKey[leftKey] ?? Number.MAX_SAFE_INTEGER;
    const rightWeight = weightByKey[rightKey] ?? Number.MAX_SAFE_INTEGER;
    if (leftWeight !== rightWeight) {
      return leftWeight - rightWeight;
    }
    return leftKey.localeCompare(rightKey);
  });
}

function renderEventDetails(event) {
  const details = event.details && typeof event.details === "object" ? event.details : {};
  const detailPairs = sortEventDetails(Object.entries(details));
  if (detailPairs.length === 0) {
    return `<p class="meta">details: n/a</p>`;
  }

  return `
    <ul class="event-details">
      ${detailPairs
        .map(([key, value]) => {
          const isError = key === "error";
          return `
            <li class="event-detail ${isError ? "event-detail-error" : ""}">
              <span class="event-detail-key">${escapeHtml(key)}</span>
              <code class="event-detail-value">${escapeHtml(value)}</code>
            </li>
          `;
        })
        .join("")}
    </ul>
  `;
}

function showServiceStatusModal() {
  serviceStatusModalEl.classList.remove("hidden");
  serviceStatusModalEl.setAttribute("aria-hidden", "false");
}

function hideServiceStatusModal() {
  serviceStatusModalEl.classList.add("hidden");
  serviceStatusModalEl.setAttribute("aria-hidden", "true");
}

function renderServiceStatusLoading(stackName, serviceName) {
  serviceStatusTitleEl.textContent = `${stackName} / ${serviceName}`;
  serviceStatusBodyEl.innerHTML = `<p class="meta">Loading service status...</p>`;
}

function renderServiceStatusError(message) {
  serviceStatusBodyEl.innerHTML = `<p class="meta">Failed to load service status: ${message}</p>`;
}

function renderServiceStatusLabels(labels) {
  const entries = labels && typeof labels === "object" ? Object.entries(labels) : [];
  if (entries.length === 0) {
    return `<p class="meta">No labels.</p>`;
  }

  const sortedEntries = entries.slice().sort(([left], [right]) => left.localeCompare(right));
  return `
    <ul class="event-details">
      ${sortedEntries
        .map(
          ([key, value]) =>
            `<li class="event-detail"><span class="event-detail-key">${escapeHtml(key)}</span><code class="event-detail-value">${escapeHtml(value)}</code></li>`,
        )
        .join("")}
    </ul>
  `;
}

function renderServiceStatusSecrets(secrets) {
  const rows = Array.isArray(secrets) ? secrets : [];
  if (rows.length === 0) {
    return `<p class="meta">No secrets.</p>`;
  }

  return `
    <ul class="event-details">
      ${rows
        .map((secret) => {
          const meta = [];
          if (secret.secret_id) {
            meta.push(`id=${secret.secret_id}`);
          }
          if (secret.target) {
            meta.push(`target=${secret.target}`);
          }

          return `
            <li class="event-detail">
              <span class="event-detail-key">${escapeHtml(secret.secret_name || "unknown")}</span>
              <code class="event-detail-value">${escapeHtml(meta.join(", ") || "-")}</code>
            </li>
          `;
        })
        .join("")}
    </ul>
  `;
}

function renderServiceStatusNetworks(networks) {
  const rows = Array.isArray(networks) ? networks : [];
  if (rows.length === 0) {
    return `<p class="meta">No network attachments.</p>`;
  }

  return `
    <ul class="event-details">
      ${rows
        .map((network) => {
          const aliases = Array.isArray(network.aliases) && network.aliases.length > 0 ? network.aliases.join(", ") : "-";
          return `
            <li class="event-detail">
              <span class="event-detail-key">${escapeHtml(network.target || "unknown")}</span>
              <code class="event-detail-value">${escapeHtml(`aliases=${aliases}`)}</code>
            </li>
          `;
        })
        .join("")}
    </ul>
  `;
}

function renderServiceStatus(data) {
  const spec = data && typeof data.spec === "object" ? data.spec : {};

  serviceStatusTitleEl.textContent = `${data.stack} / ${data.service}`;
  serviceStatusBodyEl.innerHTML = `
    <div class="service-metrics">
      <p><strong>Image:</strong> ${escapeHtml(spec.image || "n/a")}</p>
      <p><strong>Deploy Mode:</strong> ${escapeHtml(spec.mode || "n/a")}</p>
      <p><strong>Replicas:</strong> ${Number.isNaN(Number(spec.replicas)) ? "n/a" : Number(spec.replicas)}</p>
      <p><strong>Requested RAM:</strong> ${fmtBytes(spec.requested_ram_bytes)}</p>
      <p><strong>Requested CPU:</strong> ${spec.requested_cpu_nano || 0} nano-CPUs</p>
      <p><strong>RAM Limit:</strong> ${fmtBytes(spec.limit_ram_bytes)}</p>
      <p><strong>CPU Limit:</strong> ${spec.limit_cpu_nano || 0} nano-CPUs</p>
      <p><strong>Labels</strong></p>
      ${renderServiceStatusLabels(spec.labels)}
      <p><strong>Secrets</strong></p>
      ${renderServiceStatusSecrets(spec.secrets)}
      <p><strong>Network</strong></p>
      ${renderServiceStatusNetworks(spec.network)}
    </div>
  `;
}

function showEventHistoryModal() {
  eventHistoryModalEl.classList.remove("hidden");
  eventHistoryModalEl.setAttribute("aria-hidden", "false");
}

function hideEventHistoryModal() {
  eventHistoryModalEl.classList.add("hidden");
  eventHistoryModalEl.setAttribute("aria-hidden", "true");
}

function renderEventHistoryLoading() {
  eventHistoryBodyEl.innerHTML = `<p class="meta">Loading event history...</p>`;
}

function renderEventHistoryError(message) {
  eventHistoryBodyEl.innerHTML = `<p class="meta">Failed to load event history: ${message}</p>`;
}

function renderEventHistory(events) {
  if (!Array.isArray(events) || events.length === 0) {
    eventHistoryBodyEl.innerHTML = `<p class="meta">No events yet.</p>`;
    return;
  }

  eventHistoryBodyEl.innerHTML = `
    <div class="event-list">
      ${events
        .slice()
        .reverse()
        .map(
          (event) => {
            return `
            <article class="event-item">
              <p><strong>${escapeHtml(event.type || "unknown")}</strong> - ${escapeHtml(fmtDate(event.created_at))}</p>
              <p class="meta">${escapeHtml(event.message || "No details")}</p>
              ${renderEventDetails(event)}
            </article>
          `;
          },
        )
        .join("")}
    </div>
  `;
}

async function openEventHistoryModal() {
  showEventHistoryModal();
  renderEventHistoryLoading();

  try {
    const response = await fetch("/api/v1/events");
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}`);
    }
    const data = await response.json();
    renderEventHistory(data.events);
  } catch (err) {
    renderEventHistoryError(err.message);
  }
}

async function openServiceStatusModal(stackName, serviceName) {
  showServiceStatusModal();
  renderServiceStatusLoading(stackName, serviceName);

  try {
    const response = await fetch(
      `/api/v1/stacks/${encodeURIComponent(stackName)}/services/${encodeURIComponent(serviceName)}/status`,
    );
    if (!response.ok) {
      let message = `HTTP ${response.status}`;
      try {
        const payload = await response.json();
        if (payload.error_message) {
          message = payload.error_message;
        }
      } catch (error) {
        // Keep fallback message from status code.
      }
      throw new Error(message);
    }
    const data = await response.json();
    renderServiceStatus(data);
  } catch (err) {
    renderServiceStatusError(err.message);
  }
}

function renderStacks(stacks) {
  if (!Array.isArray(stacks) || stacks.length === 0) {
    stacksEl.innerHTML = `<article class="stack-card"><p class="meta">No stacks configured.</p></article>`;
    return;
  }

  stacksEl.innerHTML = stacks
    .map((stack, index) => {
      const status = (stack.last_status || "unknown").toLowerCase();
      const services = Array.isArray(stack.services) ? stack.services : [];
      const servicesMarkup =
        services.length === 0
          ? "<li>No services captured yet.</li>"
          : services
              .map(
                (service) => `
                  <li class="service-item">
                    <div>
                      <strong>${service.name || "unknown"}</strong><br />
                      <span>${service.image || "unknown image"} (${service.image_version || "unknown"})</span>
                    </div>
                    <button
                      type="button"
                      class="service-status-btn"
                      data-stack="${stack.name || ""}"
                      data-service="${service.name || ""}"
                    >
                      Status
                    </button>
                  </li>
                `,
              )
              .join("");

      return `
        <article class="stack-card" style="animation-delay:${Math.min(index * 80, 520)}ms">
          <h3 class="stack-title">${stack.name}</h3>
          <span class="status ${status}">${status}</span>
          <p class="meta">compose: ${stack.compose_file}</p>
          <p class="meta">last deploy: ${fmtDate(stack.last_deploy_at)}</p>
          <p class="meta">commit: ${stack.last_commit || "n/a"}</p>
          ${stack.last_error ? `<p class="meta">error: ${stack.last_error}</p>` : ""}
          <ul class="services">${servicesMarkup}</ul>
        </article>
      `;
    })
    .join("");
}

function renderSync(syncInfo) {
  if (!syncInfo) {
    syncStatusEl.textContent = "Sync status is unavailable.";
    return;
  }
  syncStatusEl.textContent =
    `Last sync: ${fmtDate(syncInfo.last_sync_at)} | ` +
    `reason: ${syncInfo.last_sync_reason || "n/a"} | ` +
    `result: ${syncInfo.last_sync_result || "n/a"} | ` +
    `revision: ${syncInfo.git_revision || "n/a"}` +
    (syncInfo.last_sync_error ? ` | error: ${syncInfo.last_sync_error}` : "");
}

async function refresh() {
  try {
    const response = await fetch("/api/v1/stacks");
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}`);
    }
    const data = await response.json();
    renderSync(data.sync);
    renderStacks(data.stacks);
  } catch (err) {
    syncStatusEl.textContent = `Failed to load state: ${err.message}`;
  }
}

async function triggerManualSync() {
  syncNowBtn.disabled = true;
  try {
    const response = await fetch("/api/v1/sync", { method: "POST" });
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}`);
    }
    await refresh();
  } catch (err) {
    syncStatusEl.textContent = `Failed to trigger sync: ${err.message}`;
  } finally {
    syncNowBtn.disabled = false;
  }
}

syncNowBtn.addEventListener("click", triggerManualSync);
showEventsBtn.addEventListener("click", openEventHistoryModal);
stacksEl.addEventListener("click", (event) => {
  const target = event.target;
  if (!(target instanceof HTMLElement)) {
    return;
  }
  if (!target.classList.contains("service-status-btn")) {
    return;
  }
  const stackName = target.dataset.stack;
  const serviceName = target.dataset.service;
  if (!stackName || !serviceName) {
    return;
  }
  openServiceStatusModal(stackName, serviceName);
});
serviceStatusCloseBtn.addEventListener("click", hideServiceStatusModal);
serviceStatusModalEl.addEventListener("click", (event) => {
  const target = event.target;
  if (target instanceof HTMLElement && target.dataset.closeModal === "true") {
    hideServiceStatusModal();
  }
});
eventHistoryCloseBtn.addEventListener("click", hideEventHistoryModal);
eventHistoryModalEl.addEventListener("click", (event) => {
  const target = event.target;
  if (target instanceof HTMLElement && target.dataset.closeEventHistory === "true") {
    hideEventHistoryModal();
  }
});
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && !serviceStatusModalEl.classList.contains("hidden")) {
    hideServiceStatusModal();
    return;
  }
  if (event.key === "Escape" && !eventHistoryModalEl.classList.contains("hidden")) {
    hideEventHistoryModal();
  }
});

assistantChat.setEnabled(true);
refresh();
setInterval(refresh, 10000);

