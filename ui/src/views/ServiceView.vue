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
  TaskLogCountsEvent,
  TaskLogEvent,
  TaskLogLevel,
} from "../api/types";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import DeploymentIcon from "../components/overview/DeploymentIcon.vue";
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
const parseLogs = ref(false);
const logSearchQuery = ref("");
const copiedLogKey = ref("");
const taskLogLevels: TaskLogLevel[] = ["TRACE", "DEBUG", "INFO", "WARN", "ERROR", "FATAL"];
const taskLogLevelFilters: Array<{ id: string; label: string; levels: TaskLogLevel[] }> = [
  { id: "all", label: "All", levels: [...taskLogLevels] },
  { id: "error", label: "Error", levels: ["ERROR", "FATAL"] },
  { id: "warn", label: "Warn", levels: ["WARN"] },
  { id: "info", label: "Info", levels: ["INFO"] },
  { id: "debug", label: "Debug", levels: ["DEBUG"] },
  { id: "other", label: "Other", levels: ["TRACE"] },
];
const selectedLogLevels = ref<TaskLogLevel[]>([...taskLogLevels]);
const logLevelCounts = ref<Record<TaskLogLevel, number>>(emptyLogLevelCounts());
const labelsModalOpen = ref(false);
const secretDetailsStore = useSecretDetailsStore();
const overviewStore = useOverviewStore();

let logsStream: EventSource | null = null;

function emptyLogLevelCounts(): Record<TaskLogLevel, number> {
  return { TRACE: 0, DEBUG: 0, INFO: 0, WARN: 0, ERROR: 0, FATAL: 0 };
}

const stackName = computed(() => String(route.params.stack ?? "").trim());
const serviceName = computed(() => String(route.params.service ?? "").trim());

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

const realtime = computed(() => {
  const items = realtimeTasks.value;
  return Array.isArray(items) ? items : [];
});

const filteredLogs = computed(() => {
  const query = logSearchQuery.value.trim().toLocaleLowerCase();
  if (!query) {
    return logs.value;
  }

  return logs.value.filter((entry) => logSearchText(entry).includes(query));
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

function deploymentStatusClass(status: ServiceDeploymentResponse["status"]): string {
  if (status === "succeeded") return "overview-deployment-result--success";
  if (status === "running") return "overview-deployment-result--running";
  if (status === "interrupted") return "overview-deployment-result--interrupted";
  return "overview-deployment-result--failed";
}

function deploymentTime(raw: string): string {
  const date = new Date(raw);
  if (Number.isNaN(date.valueOf())) return raw;
  return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
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

async function copyTaskID(taskID: string): Promise<void> {
  const id = String(taskID || "").trim();
  if (!id) {
    return;
  }

  await copyText(id);
}

async function copyText(value: string): Promise<void> {
  const text = String(value ?? "");

  if (window.isSecureContext && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch {
      // Fall back to legacy clipboard API when browser blocks navigator.clipboard.
    }
  }

  const textarea = document.createElement("textarea");
  textarea.value = text;
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

function openLabelsModal(): void {
  labelsModalOpen.value = true;
}

function closeLabelsModal(): void {
  labelsModalOpen.value = false;
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

function startTaskLogsStream(): void {
  const id = logsTaskID.value;
  if (!id) {
    return;
  }

  closeTaskLogsStream();
  logs.value = [];
  logsError.value = "";
  logsEnded.value = false;
  logsLoading.value = true;
  logsAutoScroll.value = true;
  logLevelCounts.value = emptyLogLevelCounts();

  void nextTick(scrollLogsToBottom);

  const stream = openTaskLogsStream(id, {
    follow: true,
    tail: 200,
    parse: parseLogs.value,
    levels: parseLogs.value ? selectedLogLevels.value : undefined,
  });
  logsStream = stream;

  stream.addEventListener("log", (event) => {
    if (logsStream !== stream) {
      return;
    }
    logsLoading.value = false;
    try {
      const payload = JSON.parse(event.data) as TaskLogEvent;
      appendTaskLog({
        timestamp: String(payload.timestamp || ""),
        stream: String(payload.stream || "stdout"),
        level: payload.level,
        message: String(payload.message || ""),
        attributes: payload.attributes,
        raw_message: String(payload.raw_message || ""),
      });
    } catch {
      appendTaskLog({
        timestamp: "",
        stream: "stdout",
        message: event.data,
      });
    }
  });

  stream.addEventListener("counts", (event) => {
    if (logsStream !== stream) {
      return;
    }
    logsLoading.value = false;
    try {
      const payload = JSON.parse(event.data) as TaskLogCountsEvent;
      const counts = emptyLogLevelCounts();
      for (const level of taskLogLevels) {
        counts[level] = Number(payload.counts?.[level] ?? 0);
      }
      logLevelCounts.value = counts;
    } catch {
      // Ignore malformed count updates without interrupting the log stream.
    }
  });

  stream.addEventListener("eof", () => {
    if (logsStream !== stream) {
      return;
    }
    logsLoading.value = false;
    logsEnded.value = true;
    closeTaskLogsStream();
  });

  stream.onerror = () => {
    if (logsStream !== stream) {
      return;
    }
    logsLoading.value = false;
    if (!logsEnded.value) {
      logsError.value = "Log stream interrupted";
    }
    closeTaskLogsStream();
  };
}

function openTaskLogs(taskID: string): void {
  const id = String(taskID || "").trim();
  if (!id) {
    return;
  }

  logsModalOpen.value = true;
  logsTaskID.value = id;
  parseLogs.value = false;
  logSearchQuery.value = "";
  copiedLogKey.value = "";
  selectedLogLevels.value = [...taskLogLevels];
  startTaskLogsStream();
}

function toggleLogParsing(): void {
  parseLogs.value = !parseLogs.value;
  if (parseLogs.value && selectedLogLevels.value.length === 0) {
    selectedLogLevels.value = [...taskLogLevels];
  }
  startTaskLogsStream();
}

function areAllLogLevelsSelected(): boolean {
  return taskLogLevels.every((level) => selectedLogLevels.value.includes(level));
}

function isLogLevelFilterSelected(filter: { id: string; levels: TaskLogLevel[] }): boolean {
  if (filter.id === "all") {
    return areAllLogLevelsSelected();
  }

  return !areAllLogLevelsSelected() && filter.levels.every((level) => selectedLogLevels.value.includes(level));
}

function logLevelFilterCount(filter: { levels: TaskLogLevel[] }): number {
  return filter.levels.reduce((count, level) => count + logLevelCounts.value[level], 0);
}

function toggleLogLevelFilter(filter: { id: string; levels: TaskLogLevel[] }): void {
  if (filter.id === "all") {
    selectedLogLevels.value = [...taskLogLevels];
    startTaskLogsStream();
    return;
  }

  const filterSelected = filter.levels.every((level) => selectedLogLevels.value.includes(level));
  const nextLevels = filterSelected
    ? selectedLogLevels.value.filter((level) => !filter.levels.includes(level))
    : taskLogLevels.filter((level) => filter.levels.includes(level) || selectedLogLevels.value.includes(level));
  if (nextLevels.length === 0) {
    return;
  }

  selectedLogLevels.value = nextLevels;
  startTaskLogsStream();
}

function logSearchText(entry: TaskLogEvent): string {
  let attributes = "";
  try {
    attributes = JSON.stringify(entry.attributes ?? {});
  } catch {
    attributes = String(entry.attributes ?? "");
  }

  return `${entry.message}\n${entry.raw_message ?? ""}\n${attributes}`.toLocaleLowerCase();
}

function formatLogTimestamp(rawTimestamp: string): string {
  const timestamp = String(rawTimestamp || "").trim();
  if (!timestamp) {
    return "no timestamp";
  }

  return timestamp
    .replace("T", " ")
    .replace(/(\.\d{3})\d+/, "$1")
    .replace(/Z$/, "");
}

function logAttributeEntries(attributes: Record<string, unknown> | undefined): Array<[string, unknown]> {
  return Object.entries(attributes ?? {});
}

function formatLogAttributeValue(value: unknown): string {
  if (typeof value === "string") {
    return value;
  }
  if (value === undefined) {
    return "undefined";
  }

  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

async function copyRawLog(entry: TaskLogEvent, key: string): Promise<void> {
  await copyText(entry.raw_message || entry.message);
  copiedLogKey.value = key;
}

function handleEscape(event: KeyboardEvent): void {
  if (event.key !== "Escape") {
    return;
  }

  if (labelsModalOpen.value) {
    closeLabelsModal();
  } else if (logsModalOpen.value) {
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
        <article class="overview-summary-panel service-panel service-details-card">
          <header class="overview-summary-panel-header">
            <div class="service-panel-heading">
              <span class="overview-summary-panel-icon" aria-hidden="true">
                <DeploymentIcon name="service" />
              </span>
              <h3 class="overview-panel-title">Service</h3>
            </div>
          </header>
          <div class="service-panel-content">
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
                  <div class="service-image-row">
                    <span>{{ serviceImage || "n/a" }}</span>
                    <button
                      v-if="serviceImage"
                      type="button"
                      class="service-copy-task-id-button service-copy-image-button"
                      aria-label="Copy service image"
                      title="Copy service image"
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
                  </div>
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
          </div>
        </article>

        <article class="overview-summary-panel service-panel service-realtime-card">
          <header class="overview-summary-panel-header">
            <div class="service-panel-heading">
              <span class="overview-summary-panel-icon" aria-hidden="true">
                <svg viewBox="0 0 24 24" fill="none"><path d="M3 12h4l3-7 4 14 3-7h4" /></svg>
              </span>
              <h3 class="overview-panel-title">Realtime</h3>
            </div>
          </header>
          <div class="service-panel-content">
          <AppTableEmpty v-if="realtimeLoading" message="Loading realtime..." />
          <AppTableEmpty v-else-if="realtimeError">Failed to load realtime: {{ realtimeError }}</AppTableEmpty>
          <AppTableEmpty v-else-if="realtime.length === 0" message="No tasks yet." />
          <AppTable
            v-else
            fixed
            min-width="820px"
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
          <ul v-if="!realtimeLoading && !realtimeError && realtime.length > 0" class="service-realtime-mobile">
            <li v-for="task in realtime" :key="task.id" class="service-realtime-mobile-item">
              <div class="service-realtime-mobile-head">
                <span class="service-realtime-mobile-state" :class="realtimeStateClass(task.current_state)">
                  <span class="service-realtime-mobile-state-dot" aria-hidden="true" />
                  {{ task.current_state || "n/a" }}
                </span>
                <button
                  type="button"
                  class="service-realtime-mobile-logs"
                  :disabled="!task.id"
                  @click="openTaskLogs(task.id)"
                >
                  Logs
                </button>
              </div>
              <code class="service-realtime-mobile-node">{{ task.node_name || task.node || "n/a" }}</code>
              <span class="service-realtime-mobile-updated">Updated {{ formatDate(task.updated_at) }}</span>
              <p v-if="task.error" class="service-realtime-mobile-error">{{ task.error }}</p>
            </li>
          </ul>
          </div>
        </article>
      </div>

      <div class="service-details-side">
        <article v-if="serviceLinks.length > 0" class="overview-summary-panel service-panel service-links-card">
          <header class="overview-summary-panel-header">
            <div class="service-panel-heading">
              <span class="overview-summary-panel-icon" aria-hidden="true">
                <svg viewBox="0 0 24 24" fill="none"><path d="M10 13a5 5 0 0 0 7.5.5l2-2a5 5 0 0 0-7-7l-1.2 1.2M14 11a5 5 0 0 0-7.5-.5l-2 2a5 5 0 0 0 7 7l1.2-1.2" /></svg>
              </span>
              <h3 class="overview-panel-title">Links</h3>
            </div>
          </header>
          <div class="service-panel-content">
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
          </div>
        </article>

        <article class="overview-summary-panel service-panel service-resources-card">
          <header class="overview-summary-panel-header">
            <div class="service-panel-heading">
              <span class="overview-summary-panel-icon" aria-hidden="true">
                <svg viewBox="0 0 24 24" fill="none"><rect x="4" y="7" width="16" height="10" rx="2" /><path d="M8 4v3m4-3v3m4-3v3M8 17v3m4-3v3m4-3v3" /></svg>
              </span>
              <h3 class="overview-panel-title">Resources</h3>
            </div>
          </header>
          <div class="service-panel-content">
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
          </div>
        </article>



        <article class="overview-summary-panel service-panel service-deployments-card">
          <header class="overview-summary-panel-header">
            <RouterLink
              :to="{ name: 'deployments', query: { stack_name: stackName, service_name: serviceName } }"
              class="overview-summary-panel-header-link"
            >
              <span class="overview-summary-panel-icon" aria-hidden="true">
                <svg viewBox="0 0 24 24" fill="none"><path d="M12 3v11m0 0 4-4m-4 4-4-4M5 17v3h14v-3" /></svg>
              </span>
              <h3 class="overview-panel-title">Latest Deployments</h3>
            </RouterLink>
          </header>
          <div class="service-panel-content">
          <p v-if="deploymentsLoading" class="meta">Loading deployments...</p>
          <p v-else-if="deploymentsError" class="meta">Failed to load deployments: {{ deploymentsError }}</p>
          <p v-else-if="deployments.length === 0" class="meta">No deployments yet.</p>
          <ul v-else class="service-deployments-list overview-deployment-list">
            <li v-for="deployment in deployments" :key="deployment.id"
              class="service-deployment-item service-deployment-item--interactive overview-summary-row overview-deployment-row">
              <button type="button" class="overview-deployment-details"
                :aria-label="`Open ${deployment.status} deployment details from ${formatDate(deployment.created_at)}`"
                @click="overviewStore.openDeploymentDetailsModal(deployment.id)" />
              <span class="overview-summary-severity overview-deployment-result"
                :class="deploymentStatusClass(deployment.status)" :aria-label="`Deployment ${deployment.status}`"
                :title="deployment.status" role="img"></span>
              <span class="service-deployment-status">{{ deployment.status }}</span>
              <time class="overview-deployment-time" :datetime="deployment.created_at" :title="formatDate(deployment.created_at)">
                {{ deploymentTime(deployment.created_at) }}
              </time>
              <button v-if="deployment.commit" type="button" class="overview-commit-badge overview-summary-sha-badge"
                :aria-label="`Open Git commit ${deployment.commit}`" @click.stop="openCommitDetails(deployment.commit)">
                {{ shortCommitHash(deployment.commit) }}
              </button>
              <span v-else class="overview-summary-value-empty">n/a</span>
              <span class="service-deployment-image" :title="deployment.image">
                Image {{ deployment.image_version || "n/a" }}
              </span>
            </li>
          </ul>
          </div>
        </article>
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

    <div class="modal" :class="{ hidden: !logsModalOpen }" :aria-hidden="!logsModalOpen">
      <div class="modal-overlay" @click="closeTaskLogsModal" />
      <div class="modal-card task-logs-modal-card" role="dialog" aria-modal="true" aria-labelledby="task-logs-title">
        <div class="modal-header task-logs-modal-header">
          <div class="task-logs-title">
            <h2 id="task-logs-title">Task logs</h2>
            <p>task: <code>{{ logsTaskID || "n/a" }}</code></p>
          </div>
          <button class="modal-close" type="button" aria-label="Close modal" @click="closeTaskLogsModal">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" aria-hidden="true"><path d="m7 7 10 10M17 7 7 17" /></svg>
          </button>
        </div>
        <div class="modal-body task-logs-modal-body">
          <div class="task-logs-toolbar" aria-label="Log parsing and filters">
            <button
              class="task-logs-parse-toggle"
              :class="{ 'task-logs-parse-toggle--active': parseLogs }"
              type="button"
              role="switch"
              :aria-checked="parseLogs"
              @click="toggleLogParsing"
            >
              <span class="task-logs-parse-switch" aria-hidden="true"><span /></span>
              <span>Parse logs</span>
            </button>
            <div v-if="parseLogs" class="task-log-level-filters" aria-label="Log level filters">
              <button
                v-for="filter in taskLogLevelFilters"
                :key="filter.id"
                class="task-log-level-filter"
                :class="[`task-log-level-filter--${filter.id}`, { 'task-log-level-filter--selected': isLogLevelFilterSelected(filter) }]"
                type="button"
                :aria-pressed="isLogLevelFilterSelected(filter)"
                @click="toggleLogLevelFilter(filter)"
              >
                <svg v-if="filter.id === 'all'" viewBox="0 0 16 16" aria-hidden="true"><path d="M3 3.5 5.5 6 8 3.5 10.5 6 13 3.5M3 12.5 5.5 10 8 12.5 10.5 10l2.5 2.5" /></svg>
                <span v-else class="task-log-filter-dot" aria-hidden="true" />
                <span class="task-log-filter-label">{{ filter.label }}</span>
                <span class="task-log-filter-count">{{ logLevelFilterCount(filter) }}</span>
              </button>
            </div>
            <label class="task-logs-search">
              <svg viewBox="0 0 20 20" aria-hidden="true"><circle cx="8.5" cy="8.5" r="5.5" /><path d="m12.5 12.5 4 4" /></svg>
              <span class="sr-only">Search logs</span>
              <input v-model="logSearchQuery" type="search" placeholder="Search in messages..." />
            </label>
          </div>
          <p v-if="logsLoading && logs.length === 0" class="meta">Loading logs...</p>
          <p v-if="logsError" class="meta task-logs-error">{{ logsError }}</p>
          <div ref="logsViewer" class="task-logs-viewer" aria-live="polite" @scroll="handleLogsScroll">
            <p v-if="logs.length === 0 && !logsLoading" class="meta">No log lines.</p>
            <p v-else-if="filteredLogs.length === 0" class="meta">No logs match your search.</p>
            <ol v-else class="task-logs-list">
              <li
                v-for="(entry, index) in filteredLogs"
                :key="`${entry.timestamp}-${index}`"
                class="task-log-entry"
                :class="{ 'task-log-entry--raw': !parseLogs }"
              >
                <details v-if="parseLogs" class="task-log-details">
                  <summary class="task-log-line">
                    <time class="task-log-time" :datetime="entry.timestamp">{{ formatLogTimestamp(entry.timestamp) }}</time>
                    <span
                      class="task-log-level"
                      :class="`task-log-level-${String(entry.level || 'INFO').toLowerCase()}`"
                    >{{ entry.level || "INFO" }}</span>
                    <span class="task-log-message">{{ entry.message }}</span>
                    <svg class="task-log-chevron" viewBox="0 0 20 20" aria-hidden="true"><path d="m7 5 5 5-5 5" /></svg>
                  </summary>
                  <div class="task-log-detail-panel">
                    <section v-if="logAttributeEntries(entry.attributes).length > 0" class="task-log-detail-section">
                      <h3>Attributes</h3>
                      <dl class="task-log-attributes">
                        <div v-for="[key, value] in logAttributeEntries(entry.attributes)" :key="key" class="task-log-attribute">
                          <dt>{{ key }}</dt>
                          <dd>{{ formatLogAttributeValue(value) }}</dd>
                        </div>
                      </dl>
                    </section>
                    <section class="task-log-detail-section">
                      <div class="task-log-detail-heading">
                        <h3>Raw</h3>
                        <button
                          type="button"
                          class="task-log-copy"
                          :aria-label="`Copy raw log ${index + 1}`"
                          @click="copyRawLog(entry, `${entry.timestamp}-${index}`)"
                        >
                          <svg viewBox="0 0 16 16" aria-hidden="true"><rect x="5" y="5" width="8" height="8" rx="1" /><path d="M3 10H2.5A1.5 1.5 0 0 1 1 8.5v-6A1.5 1.5 0 0 1 2.5 1h6A1.5 1.5 0 0 1 10 2.5V3" /></svg>
                          {{ copiedLogKey === `${entry.timestamp}-${index}` ? "Copied" : "Copy" }}
                        </button>
                      </div>
                      <pre>{{ entry.raw_message || entry.message }}</pre>
                    </section>
                  </div>
                </details>
                <div v-else class="task-log-line task-log-line--raw">
                  <time class="task-log-time" :datetime="entry.timestamp">{{ formatLogTimestamp(entry.timestamp) }}</time>
                  <span class="task-log-message task-log-message--raw">{{ entry.message }}</span>
                </div>
              </li>
            </ol>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>
