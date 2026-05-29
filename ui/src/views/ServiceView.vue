<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRoute } from "vue-router";

import { fetchServiceDeployments, fetchServiceRealtime, fetchServiceStatus } from "../api/overview";
import { fetchServices } from "../api/services";
import type {
  ServiceDeploymentResponse,
  ServiceInfo,
  ServiceRealtimeTask,
  ServiceStatusResponse,
} from "../api/types";
import { useOverviewStore } from "../stores/overview";
import { useSecretDetailsStore } from "../stores/secretDetails";
import { formatBytes, formatDate, formatNanoCPU, shortCommitHash } from "../utils/format";

const route = useRoute();

const loading = ref(false);
const loadingError = ref("");
const serviceInfo = ref<ServiceInfo | null>(null);
const serviceStatus = ref<ServiceStatusResponse | null>(null);
const serviceDeployments = ref<ServiceDeploymentResponse[]>([]);
const deploymentsLoading = ref(false);
const deploymentsError = ref("");
const realtimeTasks = ref<ServiceRealtimeTask[]>([]);
const realtimeLoading = ref(false);
const realtimeError = ref("");
const showDockerLabels = ref(false);
const secretDetailsStore = useSecretDetailsStore();
const overviewStore = useOverviewStore();

const stackName = computed(() => String(route.params.stack ?? "").trim());
const serviceName = computed(() => String(route.params.service ?? "").trim());

const serviceTitle = computed(() => `${stackName.value}/${serviceName.value}`);
const serviceSpec = computed(() => serviceStatus.value?.spec ?? null);
function sortedLabelEntries(labels: Record<string, string> | undefined): Array<[string, string]> {
  if (!labels || typeof labels !== "object") {
    return [];
  }

  return Object.entries(labels).sort(([left], [right]) => left.localeCompare(right));
}
const customServiceLabels = computed(() => sortedLabelEntries(serviceSpec.value?.labels?.custom));
const dockerServiceLabels = computed(() => sortedLabelEntries(serviceSpec.value?.labels?.docker));
const serviceSecrets = computed(() => {
  const secrets = serviceSpec.value?.secrets;
  return Array.isArray(secrets) ? secrets : [];
});
const serviceRoutes = computed(() => {
  const routes = serviceInfo.value?.web_routes;
  return Array.isArray(routes) ? routes : [];
});
const serviceNetworkNames = computed(() => {
  const networks = serviceSpec.value?.network;
  if (!Array.isArray(networks)) {
    return [];
  }

  const names = networks
    .map((network) => {
      const target = String(network.target ?? "").trim();
      if (!target) {
        return "";
      }

      // Network target can be a raw Docker ID; hide it because UI should only show names.
      if (/^[a-f0-9]{24,}$/i.test(target)) {
        return "";
      }

      return target;
    })
    .filter((name): name is string => name.length > 0);

  return Array.from(new Set(names)).sort((left, right) => left.localeCompare(right));
});
const deployments = computed(() => {
  const items = serviceDeployments.value;
  return Array.isArray(items) ? items : [];
});

const realtime = computed(() => {
  const items = realtimeTasks.value;
  return Array.isArray(items) ? items : [];
});

function deploymentStatusClass(status: ServiceDeploymentResponse["status"]): string {
  if (status === "success") {
    return "success";
  }
  if (status === "failed") {
    return "failed";
  }
  return "unknown";
}

function deploymentKey(item: ServiceDeploymentResponse, index: number): string {
  return `${item.created_at}-${item.status}-${item.image_version}-${index}`;
}

async function copyTaskID(taskID: string): Promise<void> {
  const id = String(taskID || "").trim();
  if (!id) {
    return;
  }

  if (window.isSecureContext && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(id);
      return;
    } catch {
      // Fall back to legacy clipboard API when browser blocks navigator.clipboard.
    }
  }

  const textarea = document.createElement("textarea");
  textarea.value = id;
  textarea.setAttribute("readonly", "true");
  textarea.style.position = "fixed";
  textarea.style.left = "-9999px";
  document.body.appendChild(textarea);
  textarea.focus();
  textarea.select();
  document.execCommand("copy");
  document.body.removeChild(textarea);
}

async function openCommitDetails(commitHash: string | undefined): Promise<void> {
  const hash = String(commitHash || "").trim();
  if (!hash) {
    return;
  }

  await overviewStore.openCommitDetailsModal(hash);
}

function openSecretDetails(secretName: string): void {
  void secretDetailsStore.openSecretDetails(secretName);
}

function toggleDockerLabels(): void {
  showDockerLabels.value = !showDockerLabels.value;
}

async function loadServiceDetails() {
  if (!stackName.value || !serviceName.value) {
    loadingError.value = "Invalid service route parameters";
    serviceInfo.value = null;
    serviceStatus.value = null;
    return;
  }

  loading.value = true;
  loadingError.value = "";

  try {
    const [servicesResult, statusResult] = await Promise.allSettled([
      fetchServices(),
      fetchServiceStatus(stackName.value, serviceName.value),
    ]);
    const errors: string[] = [];

    if (servicesResult.status === "fulfilled") {
      const services = Array.isArray(servicesResult.value.services) ? servicesResult.value.services : [];
      serviceInfo.value =
        services.find((item) => item.stack === stackName.value && item.name === serviceName.value) ?? null;
      if (!serviceInfo.value) {
        errors.push(`Service metadata not found for ${stackName.value}/${serviceName.value}`);
      }
    } else {
      serviceInfo.value = null;
      errors.push(servicesResult.reason instanceof Error ? servicesResult.reason.message : "Failed to load services");
    }

    if (statusResult.status === "fulfilled") {
      serviceStatus.value = statusResult.value;
    } else {
      serviceStatus.value = null;
      errors.push(
        statusResult.reason instanceof Error ? statusResult.reason.message : "Failed to load service status",
      );
    }

    if (errors.length > 0) {
      loadingError.value = errors.join("; ");
    }
  } catch (error) {
    serviceInfo.value = null;
    serviceStatus.value = null;
    serviceDeployments.value = [];
    loadingError.value = error instanceof Error ? error.message : "Failed to load service details";
  } finally {
    loading.value = false;
  }
}


async function loadServiceRealtime() {
  if (!stackName.value || !serviceName.value) {
    realtimeTasks.value = [];
    realtimeError.value = "Invalid service route parameters";
    return;
  }

  realtimeLoading.value = true;
  realtimeError.value = "";
  try {
    const response = await fetchServiceRealtime(stackName.value, serviceName.value);
    realtimeTasks.value = Array.isArray(response.tasks) ? response.tasks : [];
  } catch (error) {
    realtimeTasks.value = [];
    realtimeError.value = error instanceof Error ? error.message : "Failed to load service realtime";
  } finally {
    realtimeLoading.value = false;
  }
}

async function loadServiceDeployments() {
  if (!stackName.value || !serviceName.value) {
    serviceDeployments.value = [];
    deploymentsError.value = "Invalid service route parameters";
    return;
  }

  deploymentsLoading.value = true;
  deploymentsError.value = "";
  try {
    const response = await fetchServiceDeployments(stackName.value, serviceName.value);
    serviceDeployments.value = Array.isArray(response.deployments) ? response.deployments : [];
  } catch (error) {
    serviceDeployments.value = [];
    deploymentsError.value = error instanceof Error ? error.message : "Failed to load service deployments";
  } finally {
    deploymentsLoading.value = false;
  }
}

watch(
  [stackName, serviceName],
  () => {
    showDockerLabels.value = false;
    void loadServiceDetails();
    void loadServiceDeployments();
    void loadServiceRealtime();
  },
  { immediate: true },
);
</script>

<template>
  <section class="services-page">
    <header class="services-header">
      <h2>{{ serviceTitle }}</h2>
    </header>

    <div v-if="loading && !serviceStatus" class="services-empty">
      <p class="meta">Loading service details...</p>
    </div>

    <div v-else-if="!serviceStatus" class="services-empty">
      <p class="meta">Failed to load service details: {{ loadingError || "unknown error" }}</p>
    </div>

    <div v-else class="service-details-layout">
      <div class="service-details-main">
        <article class="stack-card service-details-card">
          <h3 class="stack-title">Service</h3>
          <table class="service-status-summary-table" aria-label="Service details">
            <tbody>
              <tr>
                <th scope="row">Name</th>
                <td>{{ serviceInfo?.name || serviceName }}</td>
              </tr>
              <tr>
                <th scope="row">Stack</th>
                <td>{{ serviceInfo?.stack || stackName }}</td>
              </tr>
              <tr>
                <th scope="row">Type</th>
                <td>{{ serviceInfo?.type_title || serviceInfo?.type || "n/a" }}</td>
              </tr>
              <tr>
                <th scope="row">Description</th>
                <td>{{ serviceInfo?.description || "n/a" }}</td>
              </tr>
              <tr>
                <th scope="row">Image</th>
                <td>{{ serviceInfo?.image || serviceSpec?.image || "n/a" }}</td>
              </tr>
              <tr v-if="serviceInfo && serviceInfo.repository_url">
                <th scope="row">Repository</th>
                <td>
                  <a
                    :href="serviceInfo.repository_url"
                    class="assistant-md-link"
                    target="_blank"
                    rel="noopener noreferrer"
                  >
                    {{ serviceInfo.repository_url }}
                  </a>
                </td>
              </tr>
              <tr>
                <th scope="row">Web Routes</th>
                <td>
                  <ul v-if="serviceRoutes.length > 0" class="service-details-tags">
                    <li v-for="routeItem in serviceRoutes" :key="`${routeItem.domain}-${routeItem.address}-${routeItem.port}`">
                      {{ routeItem.domain }} ({{ routeItem.address }}:{{ routeItem.port }})
                    </li>
                  </ul>
                  <span v-else>n/a</span>
                </td>
              </tr>
              <tr>
                <th scope="row">Labels</th>
                <td>
                  <ul v-if="customServiceLabels.length > 0" class="event-details">
                    <li v-for="[key, value] in customServiceLabels" :key="key" class="event-detail">
                      <span class="event-detail-key">{{ key }}</span>
                      <code class="event-detail-value">{{ value }}</code>
                    </li>
                  </ul>
                  <span v-else class="meta">No labels.</span>
                </td>
              </tr>
              <tr v-if="dockerServiceLabels.length > 0">
                <th scope="row">Docker Labels</th>
                <td>
                  <ul class="event-details">
                    <li class="event-detail">
                      <button
                        type="button"
                        class="service-secret-badge status unknown"
                        :aria-expanded="showDockerLabels ? 'true' : 'false'"
                        @click="toggleDockerLabels"
                      >
                        {{ showDockerLabels ? "Hide" : "Show" }}
                      </button>
                    </li>
                  </ul>
                  <ul
                    v-if="showDockerLabels"
                    class="event-details service-details-docker-tags"
                  >
                    <li v-for="[key, value] in dockerServiceLabels" :key="key" class="event-detail">
                      <span class="event-detail-key">{{ key }}</span>
                      <code class="event-detail-value">{{ value }}</code>
                    </li>
                  </ul>
                </td>
              </tr>
              <tr>
                <th scope="row">Networks</th>
                <td>
                  <ul v-if="serviceNetworkNames.length > 0" class="service-details-tags">
                    <li v-for="networkName in serviceNetworkNames" :key="networkName">
                      {{ networkName }}
                    </li>
                  </ul>
                  <span v-else>n/a</span>
                </td>
              </tr>
              <tr>
                <th scope="row">Secrets</th>
                <td>
                  <ul v-if="serviceSecrets.length > 0" class="service-details-tags service-secrets-list">
                    <li v-for="secret in serviceSecrets" :key="`${secret.secret_name}-${secret.secret_id}`">
                      <button
                        type="button"
                        class="service-secret-badge status unknown"
                        :disabled="!secret.secret_name"
                        @click="secret.secret_name && openSecretDetails(secret.secret_name)"
                      >
                        {{ secret.secret_name || "n/a" }}
                      </button>
                    </li>
                  </ul>
                  <span v-else>n/a</span>
                </td>
              </tr>
            </tbody>
          </table>
          <p v-if="loadingError" class="meta">Warning: {{ loadingError }}</p>
        </article>

        <article class="stack-card service-realtime-card">
          <h3 class="stack-title">Realtime</h3>
          <p v-if="realtimeLoading" class="meta">Loading realtime...</p>
          <p v-else-if="realtimeError" class="meta">Failed to load realtime: {{ realtimeError }}</p>
          <p v-else-if="realtime.length === 0" class="meta">No tasks yet.</p>
          <table v-else class="service-status-summary-table service-realtime-table" aria-label="Service realtime">
            <thead>
              <tr><th>ID</th><th>Node Name</th><th>Current State</th><th>Created At</th><th>Updated At</th><th>Error</th></tr>
            </thead>
            <tbody>
              <tr v-for="task in realtime" :key="task.id">
                <td class="service-realtime-copy-cell">
                  <button
                    type="button"
                    class="service-copy-task-id-button"
                    :disabled="!task.id"
                    :aria-label="`Copy task ID ${task.id}`"
                    title="Copy task ID"
                    @click="copyTaskID(task.id)"
                  >
                    <svg viewBox="0 0 16 16" aria-hidden="true" focusable="false">
                      <path
                        d="M5 1.5A1.5 1.5 0 0 0 3.5 3v7A1.5 1.5 0 0 0 5 11.5h7A1.5 1.5 0 0 0 13.5 10V3A1.5 1.5 0 0 0 12 1.5H5Zm0 1h7a.5.5 0 0 1 .5.5v7a.5.5 0 0 1-.5.5H5a.5.5 0 0 1-.5-.5V3a.5.5 0 0 1 .5-.5Z"
                        fill="currentColor"
                      />
                      <path
                        d="M2 4.5a.5.5 0 0 1 .5.5V12A1.5 1.5 0 0 0 4 13.5h7a.5.5 0 0 1 0 1H4A2.5 2.5 0 0 1 1.5 12V5a.5.5 0 0 1 .5-.5Z"
                        fill="currentColor"
                      />
                    </svg>
                  </button>
                </td>
                <td><code>{{ task.node_name || task.node || 'n/a' }}</code></td>
                <td>{{ task.current_state || 'n/a' }}</td>
                <td>{{ formatDate(task.created_at) }}</td>
                <td>{{ formatDate(task.updated_at) }}</td>
                <td>{{ task.error || 'n/a' }}</td>
              </tr>
            </tbody>
          </table>
        </article>
      </div>

      <div class="service-details-side">
        <article class="stack-card service-resources-card">
          <h3 class="stack-title">Resources</h3>
          <table class="service-status-summary-table" aria-label="Service resources">
            <tbody>
              <tr>
                <th scope="row">Deploy mode</th>
                <td>{{ serviceSpec?.mode || "n/a" }}</td>
              </tr>
              <tr>
                <th scope="row">Requested / limited RAM</th>
                <td>{{ serviceSpec?.requested_ram_bytes ? formatBytes(serviceSpec?.requested_ram_bytes) : 0 }} / {{ serviceSpec?.limit_ram_bytes ? formatBytes(serviceSpec?.limit_ram_bytes) : '∞' }}</td>
              </tr>
              <tr>
                <th scope="row">Requested / limited CPU</th>
                <td>{{ serviceSpec?.requested_cpu_nano ? formatNanoCPU(serviceSpec?.requested_cpu_nano) : 0 }} / {{ serviceSpec?.limit_cpu_nano? formatNanoCPU(serviceSpec?.limit_cpu_nano) : '∞' }}</td>
              </tr>
            </tbody>
          </table>
        </article>



        <article class="stack-card service-deployments-card">
          <h3 class="stack-title">Latest deployments</h3>
          <p v-if="deploymentsLoading" class="meta">Loading deployments...</p>
          <p v-else-if="deploymentsError" class="meta">Failed to load deployments: {{ deploymentsError }}</p>
          <p v-else-if="deployments.length === 0" class="meta">No deployments yet.</p>
          <ul v-else class="service-deployments-list">
            <li v-for="(deployment, index) in deployments" :key="deploymentKey(deployment, index)" class="service-deployment-item">
              <div class="service-deployment-head">
                <span class="status" :class="deploymentStatusClass(deployment.status)">
                  {{ deployment.status }}
                </span>
                <span class="meta">{{ formatDate(deployment.created_at) }}</span>
              </div>
              <p class="meta">image version: {{ deployment.image_version || "n/a" }}</p>
              <p class="meta">
                commit:
                <button
                  v-if="deployment.commit"
                  type="button"
                  class="stack-commit-badge status unknown"
                  @click="openCommitDetails(deployment.commit)"
                >
                  {{ shortCommitHash(deployment.commit) }}
                </button>
                <span v-else> n/a</span>
              </p>
            </li>
          </ul>
        </article>
      </div>
    </div>
  </section>
</template>
