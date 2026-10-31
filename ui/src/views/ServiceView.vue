<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { useRoute } from "vue-router";

import { fetchServiceDeployments, fetchServiceRealtime, fetchServiceStatus } from "../api/overview";
import { fetchServices, fetchSwarmTask } from "../api/services";
import type {
  ServiceDeploymentResponse,
  ServiceInfo,
  ServiceRealtimeTask,
  ServiceStatusResponse,
  SwarmTaskDetails,
} from "../api/types";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import AppPanel from "../components/common/AppPanel.vue";
import AppStatusBadge from "../components/common/AppStatusBadge.vue";
import DeploymentIcon from "../components/overview/DeploymentIcon.vue";
import RecentDeploymentsPanel from "../components/overview/RecentDeploymentsPanel.vue";
import TaskDetailsPanel from "../components/services/TaskDetailsPanel.vue";
import TaskLogsModal from "../components/services/TaskLogsModal.vue";
import { useSecretDetailsStore } from "../stores/secretDetails";
import { copyText } from "../utils/clipboard";
import { formatBytes, formatDate, formatNanoCPU } from "../utils/format";

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
const selectedTaskID = ref("");
const selectedTask = ref<SwarmTaskDetails | null>(null);
const taskDetailsLoading = ref(false);
const taskDetailsError = ref("");
const logsModalOpen = ref(false);
const logsTaskID = ref("");
const labelsModalOpen = ref(false);
const secretDetailsStore = useSecretDetailsStore();
let taskDetailsRequest = 0;

const stackName = computed(() => String(route.params.stack ?? "").trim());
const serviceName = computed(() => String(route.params.service ?? "").trim());
const swarmServiceName = computed(() => `${stackName.value}_${serviceName.value}`);

const serviceTitle = computed(() => `${stackName.value}/${serviceName.value}`);
const serviceSpec = computed(() => serviceStatus.value?.spec ?? null);
const serviceImage = computed(() => serviceInfo.value?.image || serviceSpec.value?.image || "");
function sortedLabelEntries(labels: Record<string, string> | undefined): Array<[string, string]> {
  if (!labels || typeof labels !== "object") {
    return [];
  }

  return Object.entries(labels).sort(([left], [right]) => left.localeCompare(right));
}
function mergeLabelEntries(...groups: Array<Array<[string, string]>>): Array<[string, string]> {
  return Array.from(new Map(groups.flat()).entries())
    .sort(([left], [right]) => left.localeCompare(right));
}
const customServiceLabels = computed(() => sortedLabelEntries(serviceSpec.value?.labels?.custom));
const dockerServiceLabels = computed(() => sortedLabelEntries(serviceSpec.value?.labels?.docker));
const swarmDeployServiceLabels = computed(() => sortedLabelEntries(serviceSpec.value?.labels?.swarm_deploy));
const inlineServiceLabels = computed(() =>
  customServiceLabels.value.filter(([key]) => !isDockerLabel(key) && !isSwarmDeployLabel(key) && !isOtherSystemLabel(key)),
);
const modalDockerServiceLabels = computed(() => mergeLabelEntries(
  dockerServiceLabels.value,
  customServiceLabels.value.filter(([key]) => isDockerLabel(key)),
));
const modalSwarmDeployServiceLabels = computed(() => mergeLabelEntries(
  swarmDeployServiceLabels.value,
  customServiceLabels.value.filter(([key]) => isSwarmDeployLabel(key)),
));
const otherServiceLabels = computed(() =>
  customServiceLabels.value.filter(([key]) => isOtherSystemLabel(key)),
);
const serviceLabelGroups = computed(() => [
  { name: "Custom", labels: inlineServiceLabels.value },
  { name: "Docker", labels: modalDockerServiceLabels.value },
  { name: "Swarm Deploy", labels: modalSwarmDeployServiceLabels.value },
  { name: "Other", labels: otherServiceLabels.value },
].filter((group) => group.labels.length > 0));
const serviceLabelCount = computed(() =>
  serviceLabelGroups.value.reduce((total, group) => total + group.labels.length, 0),
);
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
const recentDeployments = computed(() => deployments.value.slice(0, 4).map((deployment) => ({
  id: deployment.id,
  status: deployment.status,
  startedAt: deployment.created_at,
  commit: deployment.commit,
  summary: deployment.message || `Image ${deployment.image_version || "n/a"}`,
})));

const realtime = computed(() => {
  const items = realtimeTasks.value;
  return Array.isArray(items) ? items : [];
});

function isDockerLabel(key: string): boolean {
  return key.startsWith("com.docker.");
}

function isSwarmDeployLabel(key: string): boolean {
  return key.startsWith("org.swarm-deploy.");
}

function isOtherSystemLabel(key: string): boolean {
  return key === "prometheus.port";
}

function realtimeStateClass(state: string): string {
  const normalized = String(state || "").trim().toLowerCase();
  if (normalized === "running" || normalized === "complete" || normalized === "completed") {
    return "service-realtime-mobile-state--healthy";
  }
  if (normalized === "failed" || normalized === "rejected" || normalized === "shutdown") {
    return "service-realtime-mobile-state--error";
  }
  return "service-realtime-mobile-state--unknown";
}

function realtimeTableStateClass(state: string): string {
  const normalized = String(state || "").trim().toLowerCase();
  if (normalized === "running" || normalized === "complete" || normalized === "completed") {
    return "success";
  }
  if (normalized === "failed" || normalized === "rejected" || normalized === "shutdown") {
    return "failed";
  }
  return "unknown";
}

function realtimeTableStateVariant(state: string): "success" | "error" | "neutral" {
  const normalized = String(state || "").trim().toLowerCase();
  if (normalized === "running" || normalized === "complete" || normalized === "completed") {
    return "success";
  }
  if (normalized === "failed" || normalized === "rejected" || normalized === "shutdown") {
    return "error";
  }
  return "neutral";
}

async function copyTaskID(taskID: string): Promise<void> {
  const id = String(taskID || "").trim();
  if (!id) {
    return;
  }

  await copyText(id);
}

function openSecretDetails(secretName: string): void {
  void secretDetailsStore.openSecretDetails(secretName);
}

function openLabelsModal(): void {
  labelsModalOpen.value = true;
}

function closeLabelsModal(): void {
  labelsModalOpen.value = false;
}

function closeTaskLogsModal(): void {
  logsModalOpen.value = false;
}

function closeTaskDetails(): void {
  taskDetailsRequest++;
  selectedTaskID.value = "";
  selectedTask.value = null;
  taskDetailsLoading.value = false;
  taskDetailsError.value = "";
}

async function openTaskDetails(taskID: string): Promise<void> {
  const id = String(taskID || "").trim();
  if (!id) {
    return;
  }

  const current = ++taskDetailsRequest;
  selectedTaskID.value = id;
  selectedTask.value = null;
  taskDetailsLoading.value = true;
  taskDetailsError.value = "";

  try {
    const response = await fetchSwarmTask(id);
    if (current !== taskDetailsRequest || selectedTaskID.value !== id) {
      return;
    }
    selectedTask.value = response;
  } catch (error) {
    if (current !== taskDetailsRequest || selectedTaskID.value !== id) {
      return;
    }
    taskDetailsError.value = error instanceof Error ? error.message : "Failed to load task details";
  } finally {
    if (current === taskDetailsRequest) {
      taskDetailsLoading.value = false;
    }
  }
}

function openTaskLogs(taskID: string): void {
  const id = String(taskID || "").trim();
  if (!id) {
    return;
  }

  logsModalOpen.value = true;
  logsTaskID.value = id;
}

function handleEscape(event: KeyboardEvent): void {
  if (event.key !== "Escape") {
    return;
  }

  if (labelsModalOpen.value) {
    closeLabelsModal();
    return;
  }

  if (selectedTaskID.value) {
    closeTaskDetails();
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
    const response = await fetchServiceRealtime(swarmServiceName.value);
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
    const response = await fetchServiceDeployments(stackName.value, serviceName.value, 4);
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
    closeTaskDetails();
    closeLabelsModal();
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
  taskDetailsRequest++;
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
        <AppPanel
          class="overview-summary-panel service-panel service-details-card"
          header-class="overview-summary-panel-header"
          content-class="service-panel-content"
        >
          <template #header>
            <div class="service-panel-heading">
              <span class="overview-summary-panel-icon" aria-hidden="true">
                <DeploymentIcon name="service" />
              </span>
              <h3 class="overview-panel-title">Service</h3>
            </div>
          </template>
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
                <td>
                  {{ serviceImage || "n/a" }}
                  <button
                    v-if="serviceImage"
                    type="button"
                    class="service-copy-task-id-button service-image-copy-button"
                    aria-label="Copy image reference"
                    title="Copy image reference"
                    @click="copyText(serviceImage)"
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
                  <div class="service-labels-row">
                    <div class="service-labels-inline">
                      <code
                        v-for="[key, value] in inlineServiceLabels"
                        :key="key"
                        class="service-label-chip"
                        :title="`${key}=${value}`"
                      >{{ key }}={{ value }}</code>
                      <span v-if="serviceLabelCount === 0" class="meta">No labels.</span>
                    </div>
                    <button
                      v-if="serviceLabelCount > 0"
                      type="button"
                      class="service-labels-show-all"
                      @click="openLabelsModal"
                    >
                      Show all ({{ serviceLabelCount }})
                    </button>
                  </div>
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
        </AppPanel>

        <AppPanel
          class="overview-summary-panel service-panel service-realtime-card"
          header-class="overview-summary-panel-header"
          content-class="service-panel-content service-realtime-table-content"
        >
          <template #header>
            <div class="service-panel-heading">
              <span class="overview-summary-panel-icon" aria-hidden="true">
                <svg viewBox="0 0 24 24" fill="none"><path d="M3 12h4l3-7 4 14 3-7h4" /></svg>
              </span>
              <h3 class="overview-panel-title">Realtime</h3>
            </div>
          </template>
          <AppTableEmpty v-if="realtimeLoading" class="service-realtime-state" message="Loading realtime..." />
          <AppTableEmpty v-else-if="realtimeError" class="service-realtime-state">Failed to load realtime: {{ realtimeError }}</AppTableEmpty>
          <AppTableEmpty v-else-if="realtime.length === 0" class="service-realtime-state" message="No tasks yet." />
          <AppTable
            v-else
            fixed
            table-class="service-realtime-table"
            wrap-class="service-realtime-desktop"
            aria-label="Service realtime"
          >
            <template #head>
              <tr>
                <th>ID</th>
                <th>Node Name</th>
                <th>Current State</th>
                <th>Created At</th>
                <th>Updated At</th>
                <th>Error</th>
                <th>Actions</th>
              </tr>
            </template>
              <tr v-for="task in realtime" :key="task.id">
                <td class="app-table-cell--center app-table-cell--narrow service-realtime-copy-cell">
                  <button
                    type="button"
                    class="service-copy-task-id-button service-realtime-copy-button"
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
                <td>
                  <AppStatusBadge
                    class="status"
                    :class="realtimeTableStateClass(task.current_state)"
                    :variant="realtimeTableStateVariant(task.current_state)"
                  >
                    {{ task.current_state || 'n/a' }}
                  </AppStatusBadge>
                </td>
                <td>{{ formatDate(task.created_at) }}</td>
                <td>{{ formatDate(task.updated_at) }}</td>
                <td>{{ task.error || 'n/a' }}</td>
                <td class="app-table-cell--center app-table-cell--narrow service-realtime-actions-cell">
                  <span class="service-realtime-actions">
                    <button
                      type="button"
                      class="service-realtime-action-button service-task-details-button"
                      :disabled="!task.id"
                      :aria-label="`Open details for task ${task.id}`"
                      title="Task details"
                      @click="openTaskDetails(task.id)"
                    >
                      <svg viewBox="0 0 16 16" aria-hidden="true">
                        <circle cx="3" cy="4" r=".7" />
                        <circle cx="3" cy="8" r=".7" />
                        <circle cx="3" cy="12" r=".7" />
                        <path d="M6 4h7M6 8h7M6 12h7" />
                      </svg>
                    </button>
                    <button
                      type="button"
                      class="service-realtime-action-button service-task-logs-button"
                      :disabled="!task.id"
                      :aria-label="`Open logs for task ${task.id}`"
                      title="Task logs"
                      @click="openTaskLogs(task.id)"
                    >
                      <svg viewBox="0 0 16 16" aria-hidden="true">
                        <path d="M4 2h5l3 3v9H4zM9 2v3h3M6 8h4M6 10.5h4" />
                      </svg>
                    </button>
                  </span>
                </td>
              </tr>
          </AppTable>
          <ul v-if="!realtimeLoading && !realtimeError && realtime.length > 0" class="service-realtime-mobile">
            <li v-for="task in realtime" :key="task.id" class="service-realtime-mobile-item">
              <div class="service-realtime-mobile-head">
                <span class="service-realtime-mobile-state" :class="realtimeStateClass(task.current_state)">
                  <span class="service-realtime-mobile-state-dot" aria-hidden="true" />
                  {{ task.current_state || "n/a" }}
                </span>
                <span class="service-realtime-actions">
                  <button
                    type="button"
                    class="service-realtime-action-button service-task-details-button"
                    :disabled="!task.id"
                    :aria-label="`Open details for task ${task.id}`"
                    title="Task details"
                    @click="openTaskDetails(task.id)"
                  >
                    <svg viewBox="0 0 16 16" aria-hidden="true">
                      <circle cx="3" cy="4" r=".7" />
                      <circle cx="3" cy="8" r=".7" />
                      <circle cx="3" cy="12" r=".7" />
                      <path d="M6 4h7M6 8h7M6 12h7" />
                    </svg>
                  </button>
                  <button
                    type="button"
                    class="service-realtime-action-button service-task-logs-button"
                    :disabled="!task.id"
                    :aria-label="`Open logs for task ${task.id}`"
                    title="Task logs"
                    @click="openTaskLogs(task.id)"
                  >
                    <svg viewBox="0 0 16 16" aria-hidden="true">
                      <path d="M4 2h5l3 3v9H4zM9 2v3h3M6 8h4M6 10.5h4" />
                    </svg>
                  </button>
                </span>
              </div>
              <code class="service-realtime-mobile-node">{{ task.node_name || task.node || "n/a" }}</code>
              <span class="service-realtime-mobile-updated">Updated {{ formatDate(task.updated_at) }}</span>
              <p v-if="task.error" class="service-realtime-mobile-error">{{ task.error }}</p>
            </li>
          </ul>
        </AppPanel>
      </div>

      <div class="service-details-side" :class="{ 'service-details-side--task-details': selectedTaskID }">
        <button
          v-if="selectedTaskID"
          type="button"
          class="service-task-details-backdrop"
          aria-label="Close task details"
          @click="closeTaskDetails"
        />
        <TaskDetailsPanel
          v-if="selectedTaskID"
          :task="selectedTask"
          :loading="taskDetailsLoading"
          :error="taskDetailsError"
          @close="closeTaskDetails"
        />
        <template v-else>
          <AppPanel
            v-if="serviceLinks.length > 0"
            class="overview-summary-panel service-panel service-links-card"
            header-class="overview-summary-panel-header"
            content-class="service-panel-content"
          >
          <template #header>
            <div class="service-panel-heading">
              <span class="overview-summary-panel-icon" aria-hidden="true">
                <svg viewBox="0 0 24 24" fill="none"><path d="M10 13a5 5 0 0 0 7.5.5l2-2a5 5 0 0 0-7-7l-1.2 1.2M14 11a5 5 0 0 0-7.5-.5l-2 2a5 5 0 0 0 7 7l1.2-1.2" /></svg>
              </span>
              <h3 class="overview-panel-title">Links</h3>
            </div>
          </template>
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
          </AppPanel>

          <AppPanel
            class="overview-summary-panel service-panel service-resources-card"
            header-class="overview-summary-panel-header"
            content-class="service-panel-content"
          >
          <template #header>
            <div class="service-panel-heading">
              <span class="overview-summary-panel-icon" aria-hidden="true">
                <svg viewBox="0 0 24 24" fill="none"><rect x="4" y="7" width="16" height="10" rx="2" /><path d="M8 4v3m4-3v3m4-3v3M8 17v3m4-3v3m4-3v3" /></svg>
              </span>
              <h3 class="overview-panel-title">Resources</h3>
            </div>
          </template>
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
          </AppPanel>



          <RecentDeploymentsPanel
            id="service-deployments-title"
            class="service-panel service-deployments-card"
            title="Latest Deployments"
            :link="{ name: 'deployments', query: { stack_name: stackName, service_name: serviceName } }"
            :deployments="recentDeployments"
            :loading="deploymentsLoading"
            :error="deploymentsError"
          />
        </template>
      </div>
    </div>

    <div class="modal" :class="{ hidden: !labelsModalOpen }" :aria-hidden="!labelsModalOpen">
      <div class="modal-overlay" @click="closeLabelsModal" />
      <div class="modal-card service-labels-modal-card" role="dialog" aria-modal="true" aria-labelledby="service-labels-title">
        <div class="modal-header">
          <h2 id="service-labels-title">Service labels</h2>
          <button class="modal-close" type="button" aria-label="Close modal" @click="closeLabelsModal">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" aria-hidden="true"><path d="m7 7 10 10M17 7 7 17" /></svg>
          </button>
        </div>
        <div class="modal-body service-labels-modal-body">
          <section v-for="group in serviceLabelGroups" :key="group.name" class="service-labels-group">
            <h3>{{ group.name }}</h3>
            <dl class="service-labels-list">
              <div v-for="[key, value] in group.labels" :key="key" class="service-labels-list-item">
                <dt><code>{{ key }}</code></dt>
                <dd><code>{{ value }}</code></dd>
              </div>
            </dl>
          </section>
        </div>
      </div>
    </div>

    <TaskLogsModal :open="logsModalOpen" :task-id="logsTaskID" @close="closeTaskLogsModal" />
  </section>
</template>
