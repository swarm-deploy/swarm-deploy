const servicesStatusEl = document.getElementById("services-status");
const servicesListEl = document.getElementById("services-list");

function escapeHtml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll("\"", "&quot;")
    .replaceAll("'", "&#39;");
}

function renderStatus(message) {
  servicesStatusEl.textContent = message;
}

function renderServices(services) {
  if (!Array.isArray(services) || services.length === 0) {
    servicesListEl.innerHTML = `
      <article class="service-card">
        <p class="meta">No services captured yet. Trigger a deploy to collect metadata.</p>
      </article>
    `;
    return;
  }

  servicesListEl.innerHTML = services
    .map((service) => {
      const serviceType = service.type || "application";
      return `
        <article class="service-card">
          <div class="service-card-header">
            <h3 class="service-name">${escapeHtml(service.name || "unknown")}</h3>
            <span class="service-type ${escapeHtml(serviceType)}">${escapeHtml(serviceType)}</span>
          </div>
          <p class="meta"><strong>stack:</strong> ${escapeHtml(service.stack || "n/a")}</p>
          <p class="meta"><strong>image:</strong> ${escapeHtml(service.image || "n/a")}</p>
          <p class="meta"><strong>description:</strong> ${escapeHtml(service.description || "n/a")}</p>
        </article>
      `;
    })
    .join("");
}

async function refresh() {
  renderStatus("Loading services...");
  try {
    const response = await fetch("/api/v1/services");
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}`);
    }

    const data = await response.json();
    const services = Array.isArray(data.services) ? data.services : [];
    renderStatus(`Total services: ${services.length}`);
    renderServices(services);
  } catch (err) {
    renderStatus(`Failed to load services: ${err.message}`);
    servicesListEl.innerHTML = "";
  }
}

refresh();
setInterval(refresh, 10000);
