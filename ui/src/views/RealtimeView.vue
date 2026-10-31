<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";

import { fetchSwarmServices, fetchSwarmServiceTasks, fetchSwarmTask } from "../api/services";
import type { ServiceRealtimeTask, SwarmService, SwarmTaskDetails } from "../api/types";
import AppMultiSelect from "../components/common/AppMultiSelect.vue";
import AppStatusBadge from "../components/common/AppStatusBadge.vue";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import SidebarIcon from "../components/layout/SidebarIcon.vue";
import TaskDetailsPanel from "../components/services/TaskDetailsPanel.vue";
import TaskLogsModal from "../components/services/TaskLogsModal.vue";
import { copyText } from "../utils/clipboard";
import { formatDate } from "../utils/format";
import { serviceRuntimeState, type RuntimeState } from "../utils/realtime";

interface TaskListState {
  tasks: ServiceRealtimeTask[];
  loading: boolean;
  error: string;
}

const REFRESH_INTERVAL_STORAGE_KEY = "realtime.refreshInterval";
const refreshIntervals = [
  { label: "Off", value: 0 },
  { label: "5s", value: 5_000 },
  { label: "10s", value: 10_000 },
  { label: "30s", value: 30_000 },
  { label: "1m", value: 60_000 },
  { label: "5m", value: 300_000 },
  { label: "15m", value: 900_000 },
  { label: "30m", value: 1_800_000 },
  { label: "1h", value: 3_600_000 },
] as const;

type RefreshInterval = typeof refreshIntervals[number]["value"];

function readRefreshInterval(): RefreshInterval {
  try {
    const stored = window.localStorage.getItem(REFRESH_INTERVAL_STORAGE_KEY);
    if (stored === null) return 5_000;
    const value = Number(stored);
    return refreshIntervals.some((interval) => interval.value === value) ? value as RefreshInterval : 5_000;
  } catch {
    return 5_000;
  }
}

const services = ref<SwarmService[]>([]);
const initialLoading = ref(true);
const refreshing = ref(false);
const refreshError = ref("");
const selectedStacks = ref<string[]>([]);
const stateFilter = ref<RuntimeState | "all">("all");
const searchQuery = ref("");
const refreshInterval = ref<RefreshInterval>(readRefreshInterval());
const refreshMenu = ref<HTMLDetailsElement | null>(null);
const expandedServiceIDs = ref(new Set<string>());
const tasksByService = ref<Record<string, TaskListState>>({});
const selectedTaskID = ref("");
const selectedTask = ref<SwarmTaskDetails | null>(null);
const taskDetailsLoading = ref(false);
const taskDetailsError = ref("");
const logsTaskID = ref("");
const logsOpen = ref(false);
const copiedErrorTaskID = ref("");

let pollingTimer: number | undefined;
let copiedErrorResetTimer: number | undefined;
let serviceRequest = 0;
let detailRequest = 0;
let disposed = false;
const taskRequests = new Map<string, number>();

const stackOptions = computed(() => {
  const values = new Set(services.value.map((service) => service.stack));
  return [...values].sort((left, right) => {
    if (left === "") return 1;
    if (right === "") return -1;
    return left.localeCompare(right);
  }).map((stack) => ({ value: stack, label: stackLabel(stack) }));
});

const selectedStackSummary = computed(() => {
  if (selectedStacks.value.length === 0) return "All stacks";
  if (selectedStacks.value.length <= 2) return selectedStacks.value.map(stackLabel).join(", ");
  return `${selectedStacks.value.length} stacks selected`;
});

const filteredServices = computed(() => {
  const query = searchQuery.value.trim().toLocaleLowerCase();
  return services.value.filter((service) => {
    if (selectedStacks.value.length > 0 && !selectedStacks.value.includes(service.stack)) return false;
    if (stateFilter.value !== "all" && serviceRuntimeState(service) !== stateFilter.value) return false;
    if (!query) return true;
    return [service.name, service.full_name, service.stack, service.image]
      .some((value) => value.toLocaleLowerCase().includes(query));
  });
});

const automaticRefresh = computed(() => refreshInterval.value !== 0);
const refreshIntervalLabel = computed(() => (
  refreshIntervals.find((interval) => interval.value === refreshInterval.value)?.label ?? "5s"
));

function errorMessage(reason: unknown): string {
  return reason instanceof Error ? reason.message : "Request failed";
}

function stateLabel(state: RuntimeState): string {
  if (state === "scaled-down") return "Scaled down";
  return state.charAt(0).toUpperCase() + state.slice(1);
}

function stackLabel(stack: string): string {
  return stack || "Standalone";
}

function selectStacks(stacks: string[]): void {
  selectedStacks.value = stacks;
}

function stateVariant(state: RuntimeState): "success" | "warning" | "neutral" {
  if (state === "running") return "success";
  if (state === "degraded") return "warning";
  return "neutral";
}

function taskStateVariant(state: string): "success" | "error" | "neutral" {
  const normalized = state.trim().toLowerCase();
  if (["running", "complete", "completed"].includes(normalized)) return "success";
  if (["failed", "rejected", "shutdown"].includes(normalized)) return "error";
  return "neutral";
}

function taskStateLabel(state: string): string {
  const value = state.trim();
  if (!value) return "Unknown";
  return value.charAt(0).toUpperCase() + value.slice(1);
}

function shortID(id: string): string {
  if (!id) return "n/a";
  if (id.length <= 18) return id;
  return `${id.slice(0, 10)}…${id.slice(-6)}`;
}

function formatRelativeTime(raw: string | undefined): string {
  if (!raw) return "n/a";
  const timestamp = new Date(raw).valueOf();
  if (Number.isNaN(timestamp)) return raw;

  const elapsedSeconds = Math.max(0, Math.floor((Date.now() - timestamp) / 1_000));
  if (elapsedSeconds < 60) return "now";
  if (elapsedSeconds < 3_600) return `${Math.floor(elapsedSeconds / 60)}m ago`;
  if (elapsedSeconds < 86_400) return `${Math.floor(elapsedSeconds / 3_600)}h ago`;
  return `${Math.floor(elapsedSeconds / 86_400)}d ago`;
}

function isFailedTask(state: string): boolean {
  return ["failed", "rejected", "shutdown"].includes(state.trim().toLowerCase());
}

async function copyTaskError(task: ServiceRealtimeTask): Promise<void> {
  if (!task.error) return;
  await copyText(task.error);
  if (copiedErrorResetTimer !== undefined) window.clearTimeout(copiedErrorResetTimer);
  copiedErrorTaskID.value = task.id;
  copiedErrorResetTimer = window.setTimeout(() => {
    copiedErrorTaskID.value = "";
    copiedErrorResetTimer = undefined;
  }, 1_600);
}

function tasksFor(serviceID: string): TaskListState {
  return tasksByService.value[serviceID] ?? { tasks: [], loading: false, error: "" };
}

async function loadTasks(service: SwarmService): Promise<void> {
  const current = (taskRequests.get(service.id) ?? 0) + 1;
  const previous = tasksFor(service.id);
  if (previous.loading) return;
  taskRequests.set(service.id, current);
  tasksByService.value = {
    ...tasksByService.value,
    [service.id]: { tasks: previous.tasks, loading: true, error: "" },
  };

  try {
    const response = await fetchSwarmServiceTasks(service.id || service.full_name);
    if (disposed || taskRequests.get(service.id) !== current) return;
    tasksByService.value = {
      ...tasksByService.value,
      [service.id]: { tasks: Array.isArray(response.tasks) ? response.tasks : [], loading: false, error: "" },
    };
  } catch (reason) {
    if (disposed || taskRequests.get(service.id) !== current) return;
    tasksByService.value = {
      ...tasksByService.value,
      [service.id]: { tasks: previous.tasks, loading: false, error: errorMessage(reason) },
    };
  }
}

async function refreshExpandedTasks(): Promise<void> {
  const expanded = services.value.filter((service) => expandedServiceIDs.value.has(service.id));
  await Promise.all(expanded.map((service) => loadTasks(service)));
}

async function loadServices(): Promise<void> {
  if (refreshing.value || disposed) return;
  const current = ++serviceRequest;
  refreshing.value = true;
  refreshError.value = "";

  try {
    const response = await fetchSwarmServices();
    if (disposed || current !== serviceRequest) return;
    services.value = (Array.isArray(response.services) ? response.services : [])
      .slice()
      .sort((left, right) => left.full_name.localeCompare(right.full_name));
    await refreshExpandedTasks();
  } catch (reason) {
    if (disposed || current !== serviceRequest) return;
    refreshError.value = errorMessage(reason);
  } finally {
    if (!disposed && current === serviceRequest) {
      initialLoading.value = false;
      refreshing.value = false;
    }
  }
}

function toggleService(service: SwarmService): void {
  const next = new Set(expandedServiceIDs.value);
  if (next.has(service.id)) {
    next.delete(service.id);
  } else {
    next.add(service.id);
    if (!tasksByService.value[service.id]) void loadTasks(service);
  }
  expandedServiceIDs.value = next;
}

async function selectTask(task: ServiceRealtimeTask): Promise<void> {
  const current = ++detailRequest;
  selectedTaskID.value = task.id;
  selectedTask.value = null;
  taskDetailsError.value = "";
  taskDetailsLoading.value = true;

  try {
    const response = await fetchSwarmTask(task.id);
    if (disposed || current !== detailRequest || selectedTaskID.value !== task.id) return;
    selectedTask.value = response;
  } catch (reason) {
    if (disposed || current !== detailRequest || selectedTaskID.value !== task.id) return;
    taskDetailsError.value = errorMessage(reason);
  } finally {
    if (!disposed && current === detailRequest) taskDetailsLoading.value = false;
  }
}

function closeTaskDetails(): void {
  detailRequest++;
  selectedTaskID.value = "";
  selectedTask.value = null;
  taskDetailsError.value = "";
  taskDetailsLoading.value = false;
}

function openLogs(taskID: string): void {
  logsTaskID.value = taskID;
  logsOpen.value = true;
}

function startPolling(): void {
  stopPolling();
  if (refreshInterval.value === 0 || document.hidden) return;
  pollingTimer = window.setInterval(() => void loadServices(), refreshInterval.value);
}

function stopPolling(): void {
  if (pollingTimer !== undefined) window.clearInterval(pollingTimer);
  pollingTimer = undefined;
}

function handleVisibilityChange(): void {
  if (document.hidden) {
    stopPolling();
    return;
  }
  if (automaticRefresh.value) {
    void loadServices();
    startPolling();
  }
}

function selectRefreshInterval(interval: RefreshInterval): void {
  refreshInterval.value = interval;
  refreshMenu.value?.removeAttribute("open");
  try {
    window.localStorage.setItem(REFRESH_INTERVAL_STORAGE_KEY, String(interval));
  } catch {
    // Refreshing still works when storage is unavailable.
  }
}

function handleDocumentClick(event: MouseEvent): void {
  if (!(event.target instanceof Node) || refreshMenu.value?.contains(event.target)) return;
  refreshMenu.value?.removeAttribute("open");
}

function handleRefreshMenuKeydown(event: KeyboardEvent): void {
  if (event.key !== "Escape" || !refreshMenu.value?.open) return;
  event.preventDefault();
  refreshMenu.value.removeAttribute("open");
  refreshMenu.value.querySelector("summary")?.focus();
}

watch(refreshInterval, startPolling);

onMounted(() => {
  document.addEventListener("visibilitychange", handleVisibilityChange);
  document.addEventListener("click", handleDocumentClick);
  void loadServices();
  startPolling();
});

onUnmounted(() => {
  disposed = true;
  serviceRequest++;
  detailRequest++;
  stopPolling();
  if (copiedErrorResetTimer !== undefined) window.clearTimeout(copiedErrorResetTimer);
  document.removeEventListener("visibilitychange", handleVisibilityChange);
  document.removeEventListener("click", handleDocumentClick);
});
</script>

<template>
  <section class="realtime-page">
    <header class="realtime-header">
      <div class="realtime-heading">
        <div class="realtime-title-row">
          <h2>Realtime</h2>
          <span class="realtime-live-indicator" :class="{ 'realtime-live-indicator--paused': !automaticRefresh }">
            <span aria-hidden="true" />{{ automaticRefresh ? "Live" : "Paused" }}
          </span>
        </div>
        <p>Live Docker Swarm services and tasks</p>
      </div>
      <div class="realtime-refresh-control">
        <button
          type="button"
          class="button-ghost realtime-refresh-button"
          :disabled="refreshing"
          :aria-busy="refreshing"
          @click="loadServices"
        >
          <svg viewBox="0 0 20 20" aria-hidden="true"><path d="M16.5 6.5V2.8m0 0h-3.7m3.7 0-2.2 2.1a6.5 6.5 0 1 0 1.4 6.9" /></svg>
          Refresh
        </button>
        <details ref="refreshMenu" class="realtime-refresh-dropdown" @keydown="handleRefreshMenuKeydown">
          <summary :aria-label="`Refresh interval: ${refreshIntervalLabel}`">
            <span>{{ refreshIntervalLabel }}</span>
            <svg viewBox="0 0 16 16" aria-hidden="true"><path d="m4 6 4 4 4-4" /></svg>
          </summary>
          <div class="realtime-refresh-menu" role="menu" aria-label="Refresh interval">
            <button
              v-for="interval in refreshIntervals"
              :key="interval.value"
              type="button"
              class="realtime-refresh-option"
              :class="{ 'realtime-refresh-option--selected': refreshInterval === interval.value }"
              role="menuitemradio"
              :aria-checked="refreshInterval === interval.value"
              @click="selectRefreshInterval(interval.value)"
            >
              <span>{{ interval.label }}</span>
              <svg v-if="refreshInterval === interval.value" viewBox="0 0 16 16" aria-hidden="true"><path d="m3 8.5 3 3L13 4.8" /></svg>
            </button>
          </div>
        </details>
      </div>
    </header>

    <p v-if="refreshError && services.length > 0" class="realtime-stale-warning" role="status">
      Live data may be stale: {{ refreshError }}
    </p>

    <div class="realtime-filters" aria-label="Service filters">
      <label class="realtime-search">
        <svg class="realtime-filter-icon" viewBox="0 0 20 20" aria-hidden="true"><circle cx="8.5" cy="8.5" r="5.5" /><path d="m12.5 12.5 4 4" /></svg>
        <span class="sr-only">Search services</span>
        <input v-model="searchQuery" type="search" placeholder="Search services, stacks, or images" />
      </label>
      <div class="realtime-filter-field">
        <AppMultiSelect
          class="realtime-stack-filter"
          :model-value="selectedStacks"
          :options="stackOptions"
          label="Filter services by stack"
          placeholder="All stacks"
          heading="Stacks"
          :selection-label="selectedStackSummary"
          @update:model-value="selectStacks"
        >
          <template #icon>
            <SidebarIcon class="realtime-filter-icon" name="services" />
          </template>
        </AppMultiSelect>
      </div>
      <label class="realtime-filter-field">
        <SidebarIcon class="realtime-filter-icon" name="realtime" />
        <select v-model="stateFilter" aria-label="Filter services by state">
          <option value="all">All states</option>
          <option value="running">Running</option>
          <option value="degraded">Degraded</option>
          <option value="scaled-down">Scaled down</option>
          <option value="unknown">Unknown</option>
        </select>
      </label>
    </div>

    <div class="realtime-workspace" :class="{ 'realtime-workspace--details': selectedTaskID }">
      <div class="realtime-services-panel">
        <AppTableEmpty v-if="initialLoading && services.length === 0" message="Loading Swarm services..." />
        <AppTableEmpty v-else-if="refreshError && services.length === 0" role="alert">
          Failed to load Swarm services: {{ refreshError }}
          <button type="button" class="button-ghost" @click="loadServices">Retry</button>
        </AppTableEmpty>
        <AppTableEmpty v-else-if="services.length === 0" message="No Swarm services found." />
        <AppTableEmpty v-else-if="filteredServices.length === 0" message="No services match the current filters." />

        <AppTable v-else fixed min-width="900px" table-class="realtime-services-table" aria-label="Swarm services">
          <template #colgroup>
            <col class="realtime-col-service" />
            <col class="realtime-col-stack" />
            <col class="realtime-col-replicas" />
            <col class="realtime-col-state" />
            <col class="realtime-col-updated" />
          </template>
          <template #head>
            <tr><th>Service</th><th>Stack</th><th>Replicas</th><th>State</th><th>Updated</th></tr>
          </template>
          <template v-for="service in filteredServices" :key="service.id">
            <tr class="realtime-service-row" :class="{ 'realtime-service-row--expanded': expandedServiceIDs.has(service.id) }" @click="toggleService(service)">
              <td class="realtime-service-name-cell" data-label="Service">
                <div>
                  <span class="realtime-service-title">
                    <button type="button" class="realtime-expand-button" :aria-expanded="expandedServiceIDs.has(service.id)" :aria-label="`${expandedServiceIDs.has(service.id) ? 'Collapse' : 'Expand'} ${service.name}`" @click.stop="toggleService(service)">
                      <svg viewBox="0 0 16 16" aria-hidden="true"><path d="m6 3 5 5-5 5" /></svg>
                    </button>
                    <strong>{{ service.name || service.full_name }}</strong>
                  </span>
                  <small v-if="service.full_name !== service.name">{{ service.full_name }}</small>
                </div>
              </td>
              <td data-label="Stack"><span :class="{ 'realtime-standalone': !service.stack }">{{ service.stack || "Standalone" }}</span></td>
              <td class="realtime-replicas" data-label="Replicas"><strong :class="{ 'realtime-replicas-running--degraded': serviceRuntimeState(service) === 'degraded' }">{{ service.replicas.running }}</strong><span>/</span>{{ service.replicas.desired }}</td>
              <td data-label="State"><AppStatusBadge class="realtime-state" :variant="stateVariant(serviceRuntimeState(service))" dot>{{ stateLabel(serviceRuntimeState(service)) }}</AppStatusBadge></td>
              <td data-label="Updated"><time :datetime="service.updated_at" :title="formatDate(service.updated_at)">{{ formatRelativeTime(service.updated_at) }}</time></td>
            </tr>
            <template v-if="expandedServiceIDs.has(service.id)">
              <tr v-if="tasksFor(service.id).loading && tasksFor(service.id).tasks.length === 0" class="realtime-task-message-row">
                <td colspan="5"><span>Loading tasks...</span></td>
              </tr>
              <tr v-else-if="tasksFor(service.id).error && tasksFor(service.id).tasks.length === 0" class="realtime-task-message-row realtime-task-message-row--error">
                <td colspan="5"><span role="alert">Failed to load tasks: {{ tasksFor(service.id).error }}</span></td>
              </tr>
              <tr v-else-if="tasksFor(service.id).tasks.length === 0" class="realtime-task-message-row">
                <td colspan="5"><span>No tasks for this service.</span></td>
              </tr>
              <template v-else>
                <tr v-if="tasksFor(service.id).error" class="realtime-task-message-row realtime-task-message-row--warning">
                  <td colspan="5"><span role="status">Task data may be stale: {{ tasksFor(service.id).error }}</span></td>
                </tr>
                <tr
                  v-for="task in tasksFor(service.id).tasks"
                  :key="task.id"
                  class="realtime-task-row"
                  :class="{ 'realtime-task-row--selected': selectedTaskID === task.id }"
                >
                  <td colspan="5">
                    <div class="realtime-task-grid">
                      <button type="button" class="realtime-task-select" @click="selectTask(task)"><strong>Slot {{ task.slot }}</strong><code>{{ shortID(task.id) }}</code></button>
                      <span class="realtime-task-node">{{ task.node_name || task.node || "Unavailable" }}</span>
                      <AppStatusBadge class="realtime-state" :variant="taskStateVariant(task.current_state)" dot>{{ taskStateLabel(task.current_state) }}</AppStatusBadge>
                      <time :datetime="task.updated_at" :title="formatDate(task.updated_at)">{{ formatRelativeTime(task.updated_at) }}</time>
                      <span class="realtime-task-error-cell" :class="{ 'realtime-task-error-cell--failed': task.error && isFailedTask(task.current_state) }">
                        <span :title="task.error || undefined">{{ task.error || "—" }}</span>
                        <button
                          v-if="task.error"
                          type="button"
                          class="realtime-task-error-copy"
                          :aria-label="copiedErrorTaskID === task.id ? 'Task error copied' : 'Copy full task error'"
                          :title="copiedErrorTaskID === task.id ? 'Copied' : 'Copy full error'"
                          @click="copyTaskError(task)"
                        >
                          <svg v-if="copiedErrorTaskID === task.id" class="realtime-task-error-copy-check" viewBox="0 0 16 16" aria-hidden="true"><path d="m3 8.5 3 3L13 4.8" /></svg>
                          <svg v-else viewBox="0 0 16 16" aria-hidden="true"><rect x="5" y="2" width="8" height="9" rx="1.5" /><path d="M3 5v7.5A1.5 1.5 0 0 0 4.5 14H11" /></svg>
                        </button>
                      </span>
                      <span class="realtime-task-actions">
                        <button type="button" class="realtime-task-action-button" :aria-label="`Open details for task ${task.id}`" title="Task details" @click="selectTask(task)">
                          <svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="3" cy="4" r=".7" /><circle cx="3" cy="8" r=".7" /><circle cx="3" cy="12" r=".7" /><path d="M6 4h7M6 8h7M6 12h7" /></svg>
                        </button>
                        <button type="button" class="realtime-task-action-button" :aria-label="`Open logs for task ${task.id}`" title="Task logs" @click="openLogs(task.id)">
                          <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M4 2h5l3 3v9H4zM9 2v3h3M6 8h4M6 10.5h4" /></svg>
                        </button>
                      </span>
                    </div>
                  </td>
                </tr>
              </template>
            </template>
          </template>
        </AppTable>
      </div>

      <button v-if="selectedTaskID" type="button" class="realtime-mobile-panel-backdrop" aria-label="Close task details" @click="closeTaskDetails" />
      <TaskDetailsPanel
        v-if="selectedTaskID"
        :task="selectedTask"
        :loading="taskDetailsLoading"
        :error="taskDetailsError"
        @close="closeTaskDetails"
      />
    </div>

    <TaskLogsModal :open="logsOpen" :task-id="logsTaskID" @close="logsOpen = false" />
  </section>
</template>

<style scoped>
.realtime-page { display: grid; gap: 16px; min-width: 0; }
.realtime-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 20px; min-width: 0; }
.realtime-heading { display: grid; gap: 4px; min-width: 0; }
.realtime-title-row { display: flex; align-items: center; gap: 10px; }
.realtime-title-row h2 { margin: 0; font-size: 1.45rem; line-height: 1.2; }
.realtime-heading p { margin: 0; color: var(--muted); font-size: .84rem; }
.realtime-live-indicator { display: inline-flex; align-items: center; gap: 6px; color: var(--success); font-size: .76rem; font-weight: 600; }
.realtime-live-indicator > span { width: 7px; height: 7px; border-radius: 50%; background: currentColor; }
.realtime-live-indicator--paused { color: var(--muted); }
.realtime-refresh-control { display: inline-flex; align-items: stretch; justify-self: end; }
.realtime-refresh-control button, .realtime-refresh-dropdown > summary { font-size: .78rem; font-weight: 600; transform: none; filter: none; }
.realtime-refresh-button { min-height: 36px; display: inline-flex; align-items: center; gap: 7px; border-radius: 7px 0 0 7px; padding: 7px 10px; white-space: nowrap; }
.realtime-refresh-button:disabled { cursor: wait; opacity: .65; }
.realtime-refresh-button svg { width: 16px; height: 16px; fill: none; stroke: currentColor; stroke-width: 1.7; stroke-linecap: round; stroke-linejoin: round; }
.realtime-refresh-dropdown { position: relative; }
.realtime-refresh-dropdown > summary { box-sizing: border-box; min-width: 66px; min-height: 36px; display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-left: -1px; border: 1px solid var(--border-default); border-radius: 0 7px 7px 0; background: var(--surface-control); color: var(--text); padding: 7px 9px 7px 11px; cursor: pointer; list-style: none; }
.realtime-refresh-dropdown > summary::-webkit-details-marker { display: none; }
.realtime-refresh-dropdown > summary:hover, .realtime-refresh-dropdown[open] > summary { background: var(--surface-control-strong); }
.realtime-refresh-dropdown > summary svg { width: 13px; height: 13px; fill: none; stroke: currentColor; stroke-width: 1.7; stroke-linecap: round; stroke-linejoin: round; }
.realtime-refresh-menu { position: absolute; z-index: 30; top: calc(100% + 6px); right: 0; width: 150px; max-height: min(360px, calc(100dvh - 128px)); overflow-y: auto; border: 1px solid var(--border-default); border-radius: 8px; background: var(--surface-elevated); box-shadow: var(--shadow-popover); padding: 6px; }
.realtime-refresh-option { width: 100%; min-height: 32px; display: flex; align-items: center; justify-content: space-between; gap: 12px; border: 0; border-radius: 6px; background: transparent; color: var(--text); padding: 6px 9px; text-align: left; }
.realtime-refresh-option:hover, .realtime-refresh-option--selected { background: var(--surface-hover-soft); }
.realtime-refresh-option svg { width: 15px; height: 15px; fill: none; stroke: var(--control-primary); stroke-width: 1.8; stroke-linecap: round; stroke-linejoin: round; }
.realtime-stale-warning, .realtime-inline-error { margin: 0; border: 1px solid var(--warning-border); border-radius: 7px; background: var(--warning-muted); color: var(--warning); padding: 9px 12px; font-size: .8rem; }
.realtime-filters { min-width: 0; display: grid; grid-template-columns: minmax(240px, 1fr) 180px 170px; gap: 10px; align-items: center; padding: 12px; border: 1px solid var(--border-subtle); border-radius: 10px; background: var(--surface-card-soft); }
.realtime-search { position: relative; min-width: 0; }
.realtime-filter-icon { width: 17px; height: 17px; flex: 0 0 17px; color: var(--muted); fill: none; stroke: currentColor; stroke-width: 1.6; pointer-events: none; }
.realtime-search .realtime-filter-icon, .realtime-filter-field > .realtime-filter-icon { position: absolute; z-index: 1; left: 11px; top: 50%; transform: translateY(-50%); }
.realtime-search input, .realtime-filter-field select { box-sizing: border-box; width: 100%; height: 38px; border: 1px solid var(--border-default); border-radius: 7px; background: var(--surface-input); color: var(--text); font: inherit; font-size: .83rem; }
.realtime-search input { padding: 7px 10px 7px 36px; }
.realtime-filter-field { position: relative; min-width: 0; color: var(--muted); }
.realtime-filter-field select { padding: 7px 10px 7px 36px; }
.realtime-search input:focus-visible, .realtime-filter-field select:focus-visible, .realtime-page button:focus-visible { outline: 2px solid var(--accent-focus); outline-offset: 2px; }
.realtime-workspace { min-width: 0; display: grid; grid-template-columns: minmax(0, 1fr); gap: 16px; align-items: start; }
.realtime-workspace--details { grid-template-columns: minmax(0, 1fr) minmax(360px, 410px); }
.realtime-services-panel { min-width: 0; }
.realtime-services-panel :deep(.app-table-wrap) { min-width: 0; }
.realtime-services-panel :deep(.app-table th) { height: 40px; padding: 7px 14px; }
.realtime-services-panel :deep(.app-table td) { height: 46px; padding: 9px 14px; }
.realtime-col-service { width: 34%; }
.realtime-col-stack { width: 17%; }
.realtime-col-replicas { width: 12%; }
.realtime-col-state { width: 17%; }
.realtime-col-updated { width: 20%; }
.realtime-service-row { cursor: pointer; transition: background-color 120ms ease; }
.realtime-service-row:hover, .realtime-service-row--expanded { background: var(--surface-hover-soft); }
.realtime-service-name-cell { min-width: 0; }
.realtime-service-name-cell > div { min-width: 0; display: grid; gap: 2px; }
.realtime-service-title { min-width: 0; display: flex; align-items: center; gap: 8px; }
.realtime-service-name-cell strong, .realtime-service-name-cell small { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.realtime-service-name-cell strong { min-width: 0; font-size: .86rem; }
.realtime-service-name-cell small { color: var(--muted); font-size: .7rem; }
.realtime-expand-button, .realtime-icon-button { display: inline-grid; place-items: center; flex: 0 0 auto; border: 0; background: transparent; color: var(--muted); padding: 0; transform: none; filter: none; }
.realtime-expand-button { width: 24px; height: 24px; }
.realtime-expand-button svg { width: 15px; height: 15px; fill: none; stroke: currentColor; stroke-width: 1.7; stroke-linecap: round; stroke-linejoin: round; transition: transform 120ms ease; }
.realtime-expand-button[aria-expanded="true"] svg { transform: rotate(90deg); }
.realtime-standalone { color: var(--muted); font-style: italic; }
.realtime-replicas { font-variant-numeric: tabular-nums; white-space: nowrap; }
.realtime-replicas strong { color: var(--text); }
.realtime-replicas .realtime-replicas-running--degraded { color: var(--warning); }
.realtime-replicas span { margin-inline: 3px; color: var(--muted); }
.realtime-state { border: 0; padding: 0; }
.realtime-service-row time { color: var(--muted); font-size: .76rem; white-space: nowrap; }
.realtime-task-row > td { height: auto !important; padding: 0 !important; background: transparent; }
.realtime-task-grid { position: relative; box-sizing: border-box; width: calc(100% - 44px); min-width: 0; min-height: 46px; display: grid; grid-template-columns: minmax(150px, 1.05fr) minmax(90px, .7fr) minmax(95px, .7fr) minmax(76px, .6fr) minmax(120px, 1.15fr); align-items: center; gap: 12px; margin-left: 44px; border-left: 1px solid var(--border-subtle); padding: 6px calc(20% + 118px) 6px 16px; font-size: .78rem; }
.realtime-task-row--selected .realtime-task-grid { background: var(--surface-selected); }
.realtime-task-select { width: 100%; min-width: 0; display: grid; gap: 2px; border: 0; background: transparent; color: var(--text); padding: 0; text-align: left; font: inherit; transform: none; filter: none; }
.realtime-task-select strong { font-size: .78rem; }
.realtime-task-select code { min-width: 0; overflow: hidden; color: var(--muted); font-size: .68rem; text-overflow: ellipsis; white-space: nowrap; }
.realtime-task-node { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.realtime-task-grid time { color: var(--muted); font-size: .76rem; white-space: nowrap; }
.realtime-task-error-cell { min-width: 0; display: grid; grid-template-columns: minmax(0, 1fr) 24px; align-items: center; gap: 4px; color: var(--muted); }
.realtime-task-error-cell > span { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.realtime-task-error-cell--failed { color: var(--error); }
.realtime-task-error-copy { display: inline-grid; place-items: center; width: 24px; height: 24px; border: 0; border-radius: 4px; background: transparent; color: currentColor; padding: 0; opacity: .75; transform: none; filter: none; }
.realtime-task-error-copy:hover { background: var(--surface-hover); opacity: 1; transform: none; filter: none; }
.realtime-task-error-copy svg { width: 14px; height: 14px; fill: none; stroke: currentColor; stroke-width: 1.4; stroke-linecap: round; stroke-linejoin: round; }
.realtime-task-error-copy .realtime-task-error-copy-check { color: var(--success); stroke-width: 2; }
.realtime-task-actions { position: absolute; top: 50%; right: calc(20% + 8px); display: flex; align-items: center; justify-content: flex-end; gap: 2px; white-space: nowrap; transform: translateY(-50%); }
.realtime-task-message-row > td { height: auto !important; padding: 9px 14px 9px 60px !important; color: var(--muted); font-size: .78rem; }
.realtime-task-message-row--error > td { color: var(--error); }
.realtime-task-message-row--warning > td { color: var(--warning); }
.realtime-task-action-button { display: inline-grid; place-items: center; width: 28px; height: 28px; border: 0; border-radius: 4px; background: transparent; color: var(--control-primary); padding: 0; transform: none; filter: none; }
.realtime-task-action-button:hover { background: var(--surface-hover); transform: none; filter: none; }
.realtime-task-action-button svg { width: 16px; height: 16px; fill: none; stroke: #606060; stroke-width: 1.5; stroke-linecap: round; stroke-linejoin: round; }
.realtime-mobile-panel-backdrop { display: none; }
:deep(.realtime-task-panel) { position: sticky; top: calc(var(--shell-header-height) + 16px); max-height: calc(100vh - var(--shell-header-height) - 44px); }

@media (max-width: 1180px) {
  .realtime-workspace--details { grid-template-columns: minmax(0, 1fr) minmax(340px, 380px); }
  .realtime-filters { grid-template-columns: minmax(220px, 1fr) 160px 150px; }
}

@media (max-width: 900px) {
  .realtime-header { align-items: stretch; flex-direction: column; gap: 10px; }
  .realtime-refresh-control { align-self: flex-end; }
  .realtime-filters { grid-template-columns: minmax(0, 1fr) minmax(130px, .45fr) minmax(130px, .45fr); }
  .realtime-search { grid-column: 1 / -1; }
  .realtime-workspace--details { grid-template-columns: minmax(0, 1fr); }
  :deep(.realtime-task-panel) { position: fixed; z-index: 61; inset: 8px; max-height: calc(100dvh - 16px); }
  .realtime-mobile-panel-backdrop { display: block; position: fixed; z-index: 60; inset: 0; width: 100%; height: 100%; border: 0; border-radius: 0; background: var(--backdrop); padding: 0; }
}

@media (max-width: 700px) {
  .realtime-page { gap: 12px; }
  .realtime-refresh-control { width: 100%; }
  .realtime-refresh-button { flex: 1 1 auto; justify-content: center; }
  .realtime-refresh-dropdown { flex: 0 0 auto; }
  .realtime-filters { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); padding: 10px; }
  .realtime-search { grid-column: 1 / -1; }
  .realtime-services-panel :deep(.app-table-wrap) { overflow: visible; }
  .realtime-services-panel :deep(.realtime-services-table) { display: block; min-width: 0 !important; }
  .realtime-services-panel :deep(.realtime-services-table colgroup), .realtime-services-panel :deep(.realtime-services-table > thead) { display: none; }
  .realtime-services-panel :deep(.realtime-services-table > tbody) { display: block; }
  .realtime-service-row { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 7px 12px; padding: 12px; border-bottom: 1px solid var(--border-subtle); }
  .realtime-services-panel :deep(.realtime-service-row > td) { display: flex; min-width: 0; height: auto; padding: 0; border: 0; }
  .realtime-service-name-cell { grid-column: 1; grid-row: 1; }
  .realtime-service-title strong { order: 1; }
  .realtime-service-title .realtime-expand-button { order: 2; }
  .realtime-service-row > td:nth-child(2) { grid-column: 1; grid-row: 2; font-size: .76rem; }
  .realtime-service-row > td:nth-child(3) { grid-column: 2; grid-row: 2; justify-content: flex-end; }
  .realtime-service-row > td:nth-child(4) { grid-column: 2; grid-row: 1; justify-content: flex-end; }
  .realtime-service-row > td:nth-child(5) { display: none !important; }
  .realtime-task-row, .realtime-task-message-row { display: block; }
  .realtime-task-row > td, .realtime-task-message-row > td { display: block; }
  .realtime-task-grid { width: calc(100% - 28px); grid-template-columns: minmax(0, 1fr) auto; gap: 5px 12px; min-height: 0; margin-left: 28px; padding: 9px 12px; }
  .realtime-task-select { grid-column: 1; grid-row: 1; }
  .realtime-task-node { grid-column: 1; grid-row: 2; color: var(--muted); }
  .realtime-task-grid > .realtime-state { grid-column: 2; grid-row: 1; justify-self: end; }
  .realtime-task-grid time { grid-column: 2; grid-row: 2; justify-self: end; }
  .realtime-task-error-cell { grid-column: 1 / -1; grid-row: 3; }
  .realtime-task-actions { position: static; grid-column: 1 / -1; grid-row: 4; justify-content: flex-start; transform: none; }
  .realtime-task-message-row > td { padding-left: 42px !important; }
}

@media (max-width: 460px) {
  .realtime-filter-field select, .realtime-search input { font-size: 16px; }
}
</style>
