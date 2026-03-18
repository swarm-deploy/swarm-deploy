const stacksEl = document.getElementById("stacks");
const syncStatusEl = document.getElementById("sync-status");
const syncNowBtn = document.getElementById("sync-now");

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
                  <li>
                    <strong>${service.name || "unknown"}</strong><br />
                    <span>${service.image || "unknown image"} (${service.image_version || "unknown"})</span>
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

refresh();
setInterval(refresh, 10000);

