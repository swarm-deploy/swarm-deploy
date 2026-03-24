const nodesStatusEl = document.getElementById("nodes-status");
const nodesListEl = document.getElementById("nodes-list");
const assistantChat = window.createAssistantChat();

function escapeHtml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll("\"", "&quot;")
    .replaceAll("'", "&#39;");
}

function renderStatus(message) {
  nodesStatusEl.textContent = message;
}

function renderNodes(nodes) {
  if (!Array.isArray(nodes) || nodes.length === 0) {
    nodesListEl.innerHTML = `
      <article class="node-card">
        <p class="meta">No nodes detected yet.</p>
      </article>
    `;
    return;
  }

  nodesListEl.innerHTML = nodes
    .map((node) => {
      return `
        <article class="node-card">
          <div class="node-card-header">
            <h3 class="node-name">${escapeHtml(node.hostname || "unknown")}</h3>
            <span class="status ${(node.status || "unknown").toLowerCase()}">${escapeHtml(node.status || "unknown")}</span>
          </div>
          <p class="meta"><strong>id:</strong> ${escapeHtml(node.id || "n/a")}</p>
          <p class="meta"><strong>availability:</strong> ${escapeHtml(node.availability || "n/a")}</p>
          <p class="meta"><strong>manager status:</strong> ${escapeHtml(node.manager_status || "n/a")}</p>
          <p class="meta"><strong>engine version:</strong> ${escapeHtml(node.engine_version || "n/a")}</p>
          <p class="meta"><strong>addr:</strong> ${escapeHtml(node.addr || "n/a")}</p>
        </article>
      `;
    })
    .join("");
}

async function refreshNodes() {
  renderStatus("Loading nodes...");
  try {
    const response = await fetch("/api/v1/nodes");
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}`);
    }

    const data = await response.json();
    const nodes = Array.isArray(data.nodes) ? data.nodes : [];
    renderStatus(`Total nodes: ${nodes.length}`);
    renderNodes(nodes);
  } catch (err) {
    renderStatus(`Failed to load nodes: ${err.message}`);
    nodesListEl.innerHTML = "";
  }
}

assistantChat.setEnabled(true);

async function refreshAll() {
  await Promise.all([refreshNodes()]);
}

refreshAll();
setInterval(refreshAll, 10000);
