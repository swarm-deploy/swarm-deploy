<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { useRoute } from "vue-router";

import { fetchServiceDeployments, fetchServiceRealtime, fetchServiceStatus, openTaskLogsStream } from "../api/overview";
import { fetchServices } from "../api/services";
import type {
  ServiceDeploymentResponse,
  ServiceInfo,
  ServiceRealtimeTask,
  ServiceStatusResponse,
  TaskLogEvent,
} from "../api/types";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
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
const logsModalOpen = ref(false);
const logsTaskID = ref("");
const logs = ref<TaskLogEvent[]>([]);
const logsLoading = ref(false);
const logsError = ref("");
const logsEnded = ref(false);
const logsViewer = ref<HTMLElement | null>(null);
const logsAutoScroll = ref(true);
const showDockerLabels = ref(false);
const showSwarmDeployLabels = ref(false);
const secretDetailsStore = useSecretDetailsStore();
const overviewStore = useOverviewStore();

let logsStream: EventSource | null = null;

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
const swarmDeployServiceLabels = computed(() => sortedLabelEntries(serviceSpec.value?.labels?.swarm_deploy));
const serviceSecrets = computed(() => {
  const secrets = serviceSpec.value?.secrets;
  return Array.isArray(secrets) ? secrets : [];
});
const serviceRoutes = computed(() => {
  const routes = serviceInfo.value?.web_routes;
  return Array.isArray(routes) ? routes : [];
});
const serviceLinks = computed(() => {
  const links = serviceStatus.value?.links;
  return Array.isArray(links) ? links : [];
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
  return status;
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

function toggleSwarmDeployLabels(): void {
  showSwarmDeployLabels.value = !showSwarmDeployLabels.value;
}

function closeTaskLogsStream(): void {
  if (!logsStream) {
    return;
  }

  logsStream.close();
  logsStream = null;
}

function closeTaskLogsModal(): void {
  closeTaskLogsStream();
  logsModalOpen.value = false;
  logsLoading.value = false;
}

function isLogsViewerAtBottom(): boolean {
  const viewer = logsViewer.value;
  if (!viewer) {
    return true;
  }

  return viewer.scrollHeight - viewer.scrollTop - viewer.clientHeight <= 8;
}

function scrollLogsToBottom(): void {
  const viewer = logsViewer.value;
  if (!viewer) {
    return;
  }

  viewer.scrollTop = viewer.scrollHeight;
}

function handleLogsScroll(): void {
  logsAutoScroll.value = isLogsViewerAtBottom();
}

function appendTaskLog(entry: TaskLogEvent): void {
  const shouldScroll = logsAutoScroll.value && isLogsViewerAtBottom();
  logs.value.push(entry);

  if (shouldScroll) {
    void nextTick(scrollLogsToBottom);
  }
}

function openTaskLogs(taskID: string): void {
  const id = String(taskID || "").trim();
  if (!id) {
    return;
  }

  closeTaskLogsStream();
  logsModalOpen.value = true;
  logsTaskID.value = id;
  logs.value = [];
  logsError.value = "";
  logsEnded.value = false;
  logsLoading.value = true;
  logsAutoScroll.value = true;

  void nextTick(scrollLogsToBottom);

  const stream = openTaskLogsStream(id, { follow: true, tail: 200 });
  logsStream = stream;

  stream.addEventListener("log", (event) => {
    logsLoading.value = false;
    try {
      const payload = JSON.parse(event.data) as TaskLogEvent;
      appendTaskLog({
        timestamp: String(payload.timestamp || ""),
        stream: String(payload.stream || "stdout"),
        message: String(payload.message || ""),
      });
    } catch {
      appendTaskLog({
        timestamp: "",
        stream: "stdout",
        message: event.data,
      });
    }
  });

  stream.addEventListener("eof", () => {
    logsLoading.value = false;
    logsEnded.value = true;
    closeTaskLogsStream();
  });

  stream.onerror = () => {
    logsLoading.value = false;
    if (!logsEnded.value) {
      logsError.value = "Log stream interrupted";
    }
    closeTaskLogsStream();
  };
}

function handleEscape(event: KeyboardEvent): void {
  if (event.key === "Escape" && logsModalOpen.value) {
    closeTaskLogsModal();
  }
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
    closeTaskLogsModal();
    showDockerLabels.value = false;
    showSwarmDeployLabels.value = false;
    void loadServiceDetails();
    void loadServiceDeployments();
    void loadServiceRealtime();
  },
  { immediate: true },
);

onMounted(() => {
  document.addEventListener("keydown", handleEscape);
});

onUnmounted(() => {
  closeTaskLogsStream();
  document.removeEventListener("keydown", handleEscape);
});
</script>

<template>
  <section class="services-page service-details-page">
    <header class="services-header">
      <h2>{{ serviceTitle }}</h2>
    </header>

    <AppTableEmpty v-if="loading && !serviceStatus" message="Loading service details..." />

    <AppTableEmpty v-else-if="!serviceStatus">
      Failed to load service details: {{ loadingError || "unknown error" }}
    </AppTableEmpty>

    <div v-else class="service-details-layout">
      <div class="service-details-main">
        <article class="stack-card service-details-card">
          <h3 class="stack-title">Service</h3>
          <AppTable summary fixed aria-label="Service details">
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
                    class="event-details service-details-hidden-tags"
                  >
                    <li v-for="[key, value] in dockerServiceLabels" :key="key" class="event-detail">
                      <span class="event-detail-key">{{ key }}</span>
                      <code class="event-detail-value">{{ value }}</code>
                    </li>
                  </ul>
                </td>
              </tr>
              <tr v-if="swarmDeployServiceLabels.length > 0">
                <th scope="row">SwarmDeploy Labels</th>
                <td>
                  <ul class="event-details">
                    <li class="event-detail">
                      <button
                        type="button"
                        class="service-secret-badge status unknown"
                        :aria-expanded="showSwarmDeployLabels ? 'true' : 'false'"
                        @click="toggleSwarmDeployLabels"
                      >
                        {{ showSwarmDeployLabels ? "Hide" : "Show" }}
                      </button>
                    </li>
                  </ul>
                  <ul
                    v-if="showSwarmDeployLabels"
                    class="event-details service-details-hidden-tags"
                  >
                    <li v-for="[key, value] in swarmDeployServiceLabels" :key="key" class="event-detail">
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
          </AppTable>
          <p v-if="loadingError" class="meta">Warning: {{ loadingError }}</p>
        </article>

        <article class="stack-card service-realtime-card">
          <h3 class="stack-title">Realtime</h3>
          <AppTableEmpty v-if="realtimeLoading" message="Loading realtime..." />
          <AppTableEmpty v-else-if="realtimeError">Failed to load realtime: {{ realtimeError }}</AppTableEmpty>
          <AppTableEmpty v-else-if="realtime.length === 0" message="No tasks yet." />
          <AppTable v-else fixed min-width="820px" table-class="service-realtime-table" aria-label="Service realtime">
            <template #head>
              <tr>
                <th>ID</th>
                <th>Node Name</th>
                <th>Current State</th>
                <th>Created At</th>
                <th>Updated At</th>
                <th>Error</th>
                <th>Logs</th>
              </tr>
            </template>
              <tr v-for="task in realtime" :key="task.id">
                <td class="app-table-cell--center app-table-cell--narrow service-realtime-copy-cell">
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
                <td class="app-table-cell--center app-table-cell--narrow service-realtime-logs-cell">
                  <button
                    type="button"
                    class="service-copy-task-id-button service-task-logs-button"
                    :disabled="!task.id"
                    :aria-label="`Open logs for task ${task.id}`"
                    title="Open task logs"
                    @click="openTaskLogs(task.id)"
                  >
                    <svg viewBox="0 0 16 16" aria-hidden="true" focusable="false">
                      <path
                        d="M3 2.5A1.5 1.5 0 0 1 4.5 1h5.8c.4 0 .78.16 1.06.44l1.2 1.2c.28.28.44.66.44 1.06v9.8A1.5 1.5 0 0 1 11.5 15h-7A1.5 1.5 0 0 1 3 13.5v-11Zm1.5-.5a.5.5 0 0 0-.5.5v11a.5.5 0 0 0 .5.5h7a.5.5 0 0 0 .5-.5V4H10.5A1.5 1.5 0 0 1 9 2.5V2H4.5ZM10 2.2v.3a.5.5 0 0 0 .5.5h1.3L10 2.2ZM5.5 6a.5.5 0 0 1 .5-.5h4a.5.5 0 0 1 0 1H6a.5.5 0 0 1-.5-.5Zm0 2.25a.5.5 0 0 1 .5-.5h4a.5.5 0 0 1 0 1H6a.5.5 0 0 1-.5-.5Zm0 2.25A.5.5 0 0 1 6 10h2.5a.5.5 0 0 1 0 1H6a.5.5 0 0 1-.5-.5Z"
                        fill="currentColor"
                      />
                    </svg>
                  </button>
                </td>
              </tr>
          </AppTable>
        </article>
      </div>

      <div class="service-details-side">
        <article v-if="serviceLinks.length > 0" class="stack-card service-links-card">
          <h3 class="stack-title">Links</h3>
          <ul class="service-links-list">
            <li v-for="link in serviceLinks" :key="`${link.type}-${link.url}`">
              <a
                :href="link.url"
                class="service-link"
                target="_blank"
                rel="noopener noreferrer"
                :title="link.url"
              >
                <span class="service-link-icon" aria-hidden="true">
                  <svg viewBox="0 0 16 16" focusable="false">
                    <path
                      d="M6.4 4.2a.75.75 0 0 1 0 1.06L3.66 8a2.05 2.05 0 0 0 2.9 2.9l2.1-2.1a.75.75 0 0 1 1.06 1.06l-2.1 2.1a3.55 3.55 0 1 1-5.02-5.02l2.74-2.74a.75.75 0 0 1 1.06 0Zm3.04.9-2.1 2.1a.75.75 0 1 0 1.06 1.06l2.1-2.1a2.05 2.05 0 1 1 2.9 2.9l-2.74 2.74a.75.75 0 1 0 1.06 1.06l2.74-2.74A3.55 3.55 0 1 0 9.44 5.1Z"
                      fill="currentColor"
                    />
                  </svg>
                </span>
                <span class="service-link-label">{{ link.type }}</span>
                <svg class="service-link-open-icon" viewBox="0 0 16 16" aria-hidden="true" focusable="false">
                  <path
                    d="M9.25 2.75A.75.75 0 0 1 10 2h3.25a.75.75 0 0 1 .75.75V6a.75.75 0 0 1-1.5 0V4.56L8.03 9.03a.75.75 0 0 1-1.06-1.06l4.47-4.47H10a.75.75 0 0 1-.75-.75ZM3.5 4A1.5 1.5 0 0 0 2 5.5v7A1.5 1.5 0 0 0 3.5 14h7a1.5 1.5 0 0 0 1.5-1.5V9.75a.75.75 0 0 0-1.5 0v2.75h-7v-7h2.75a.75.75 0 0 0 0-1.5H3.5Z"
                    fill="currentColor"
                  />
                </svg>
              </a>
            </li>
          </ul>
        </article>

        <article class="stack-card service-resources-card">
          <h3 class="stack-title">Resources</h3>
          <AppTable summary aria-label="Service resources">
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
          </AppTable>
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

    <div class="modal" :class="{ hidden: !logsModalOpen }" aria-hidden="true">
      <div class="modal-overlay" @click="closeTaskLogsModal" />
      <div class="modal-card task-logs-modal-card" role="dialog" aria-modal="true" aria-labelledby="task-logs-title">
        <div class="modal-header">
          <h2 id="task-logs-title">Task logs</h2>
          <button class="modal-close" type="button" aria-label="Close modal" @click="closeTaskLogsModal">x</button>
        </div>
        <div class="modal-body">
          <p class="meta">task: <code>{{ logsTaskID || "n/a" }}</code></p>
          <p v-if="logsLoading && logs.length === 0" class="meta">Loading logs...</p>
          <p v-if="logsError" class="meta task-logs-error">{{ logsError }}</p>
          <div ref="logsViewer" class="task-logs-viewer" aria-live="polite" @scroll="handleLogsScroll">
            <p v-if="logs.length === 0 && !logsLoading" class="meta">No log lines.</p>
            <ol v-else class="task-logs-list">
              <li v-for="(entry, index) in logs" :key="`${entry.timestamp}-${index}`" class="task-log-line">
                <time class="task-log-time">{{ entry.timestamp || "no timestamp" }}</time>
                <span class="task-log-stream" :class="`task-log-stream-${entry.stream}`">{{ entry.stream }}</span>
                <code class="task-log-message">{{ entry.message }}</code>
              </li>
            </ol>
          </div>
          <p v-if="logsEnded" class="meta">End of logs.</p>
        </div>
      </div>
    </div>
  </section>
</template>
