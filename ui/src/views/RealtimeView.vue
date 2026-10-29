<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";

import { fetchSwarmServices, fetchSwarmServiceTasks, fetchSwarmTask } from "../api/services";
import type { ServiceRealtimeTask, SwarmService, SwarmTaskDetails } from "../api/types";
import AppStatusBadge from "../components/common/AppStatusBadge.vue";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import TaskDetailsPanel from "../components/services/TaskDetailsPanel.vue";
import TaskLogsModal from "../components/services/TaskLogsModal.vue";
import { formatDate } from "../utils/format";
import { serviceRuntimeState, type RuntimeState } from "../utils/realtime";

interface TaskListState {
  tasks: ServiceRealtimeTask[];
  loading: boolean;
  error: string;
}

const services = ref<SwarmService[]>([]);
const initialLoading = ref(true);
const refreshing = ref(false);
const refreshError = ref("");
const lastSuccessfulRefresh = ref<Date | null>(null);
const selectedStacks = ref<string[]>([]);
const stateFilter = ref<RuntimeState | "all">("all");
const searchQuery = ref("");
const automaticRefresh = ref(true);
const stackFilter = ref<HTMLDetailsElement | null>(null);
const expandedServiceIDs = ref(new Set<string>());
const tasksByService = ref<Record<string, TaskListState>>({});
const selectedTaskID = ref("");
const selectedTask = ref<SwarmTaskDetails | null>(null);
const taskDetailsLoading = ref(false);
const taskDetailsError = ref("");
const logsTaskID = ref("");
const logsOpen = ref(false);

let pollingTimer: number | undefined;
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
  });
});

const selectedStackSummary = computed(() => {
  if (selectedStacks.value.length === 0) return ["All stacks"];
  if (selectedStacks.value.length <= 2) return selectedStacks.value.map(stackLabel);
  return [`${selectedStacks.value.length} stacks selected`];
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

const lastRefreshLabel = computed(() => {
  if (!lastSuccessfulRefresh.value) return "Not refreshed yet";
  return lastSuccessfulRefresh.value.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
});

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

function toggleStack(stack: string): void {
  selectedStacks.value = selectedStacks.value.includes(stack)
    ? selectedStacks.value.filter((selectedStack) => selectedStack !== stack)
    : [...selectedStacks.value, stack];
}

function clearStacks(): void {
  selectedStacks.value = [];
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

function shortID(id: string): string {
  if (!id) return "n/a";
  if (id.length <= 18) return id;
  return `${id.slice(0, 10)}…${id.slice(-6)}`;
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
    lastSuccessfulRefresh.value = new Date();
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
  if (!automaticRefresh.value || document.hidden) return;
  pollingTimer = window.setInterval(() => void loadServices(), 5_000);
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

function handleDocumentClick(event: MouseEvent): void {
  if (!(event.target instanceof Node) || stackFilter.value?.contains(event.target)) return;
  if (stackFilter.value) stackFilter.value.open = false;
}

watch(automaticRefresh, startPolling);

onMounted(() => {
  document.addEventListener("click", handleDocumentClick);
  document.addEventListener("visibilitychange", handleVisibilityChange);
  void loadServices();
  startPolling();
});

onUnmounted(() => {
  disposed = true;
  serviceRequest++;
  detailRequest++;
  stopPolling();
  document.removeEventListener("click", handleDocumentClick);
  document.removeEventListener("visibilitychange", handleVisibilityChange);
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
      <div class="realtime-refresh-status">
        <span>Updated {{ lastRefreshLabel }}</span>
        <button
          type="button"
          class="realtime-auto-refresh"
          role="switch"
          :aria-checked="automaticRefresh"
          @click="automaticRefresh = !automaticRefresh"
        >
          <span class="realtime-switch" :class="{ 'realtime-switch--active': automaticRefresh }" aria-hidden="true"><span /></span>
          Auto 5s
        </button>
        <button type="button" class="button-ghost realtime-refresh-button" :disabled="refreshing" @click="loadServices">
          <svg viewBox="0 0 20 20" aria-hidden="true"><path d="M16.5 6.5V2.8m0 0h-3.7m3.7 0-2.2 2.1a6.5 6.5 0 1 0 1.4 6.9" /></svg>
          {{ refreshing ? "Refreshing" : "Refresh" }}
        </button>
      </div>
    </header>

    <p v-if="refreshError && services.length > 0" class="realtime-stale-warning" role="status">
      Live data may be stale: {{ refreshError }}
    </p>

    <div class="realtime-filters" aria-label="Service filters">
      <label class="realtime-search">
        <svg viewBox="0 0 20 20" aria-hidden="true"><circle cx="8.5" cy="8.5" r="5.5" /><path d="m12.5 12.5 4 4" /></svg>
        <span class="sr-only">Search services</span>
        <input v-model="searchQuery" type="search" placeholder="Search services, stacks, or images" />
      </label>
      <div class="realtime-filter-field">
        <span>Stack</span>
        <details ref="stackFilter" class="realtime-stack-filter">
          <summary aria-label="Filter services by stack">
            <span class="realtime-stack-filter-selection">
              <span v-for="summary in selectedStackSummary" :key="summary" class="realtime-stack-chip">{{ summary }}</span>
            </span>
            <span aria-hidden="true">⌄</span>
          </summary>
          <div class="realtime-stack-filter-menu">
            <div class="realtime-stack-filter-menu-header">
              <strong>Stacks</strong>
              <button type="button" class="realtime-stack-filter-clear" :disabled="selectedStacks.length === 0" @click="clearStacks">
                Clear
              </button>
            </div>
            <label v-for="stack in stackOptions" :key="stack || 'standalone'" class="realtime-stack-filter-option">
              <input
                type="checkbox"
                :value="stack"
                :checked="selectedStacks.includes(stack)"
                @change="toggleStack(stack)"
              />
              <span>{{ stackLabel(stack) }}</span>
            </label>
          </div>
        </details>
      </div>
      <label class="realtime-filter-field">
        <span>State</span>
        <select v-model="stateFilter">
          <option value="all">All states</option>
          <option value="running">Running</option>
          <option value="degraded">Degraded</option>
          <option value="scaled-down">Scaled down</option>
          <option value="unknown">Unknown</option>
        </select>
      </label>
      <span class="realtime-result-count">{{ filteredServices.length }} of {{ services.length }} services</span>
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

        <AppTable v-else fixed min-width="860px" table-class="realtime-services-table" aria-label="Swarm services">
          <template #colgroup>
            <col class="realtime-col-service" />
            <col class="realtime-col-stack" />
            <col class="realtime-col-replicas" />
            <col class="realtime-col-image" />
            <col class="realtime-col-state" />
            <col class="realtime-col-updated" />
          </template>
          <template #head>
            <tr><th>Service</th><th>Stack</th><th>Replicas</th><th>Image</th><th>State</th><th>Updated</th></tr>
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
              <td class="realtime-replicas" data-label="Replicas"><strong>{{ service.replicas.running }}</strong><span>/</span>{{ service.replicas.desired }}</td>
              <td class="realtime-image" data-label="Image"><span>{{ service.image }}</span></td>
              <td data-label="State"><AppStatusBadge class="realtime-state" :variant="stateVariant(serviceRuntimeState(service))" dot>{{ stateLabel(serviceRuntimeState(service)) }}</AppStatusBadge></td>
              <td data-label="Updated"><time :datetime="service.updated_at">{{ formatDate(service.updated_at) }}</time></td>
            </tr>
            <tr v-if="expandedServiceIDs.has(service.id)" class="realtime-tasks-row">
              <td colspan="6">
                <div class="realtime-tasks-container">
                  <div class="realtime-tasks-heading"><span>Tasks</span><small>{{ tasksFor(service.id).tasks.length }} entries</small></div>
                  <AppTableEmpty v-if="tasksFor(service.id).loading && tasksFor(service.id).tasks.length === 0" message="Loading tasks..." />
                  <AppTableEmpty v-else-if="tasksFor(service.id).error && tasksFor(service.id).tasks.length === 0" role="alert">Failed to load tasks: {{ tasksFor(service.id).error }}</AppTableEmpty>
                  <AppTableEmpty v-else-if="tasksFor(service.id).tasks.length === 0" message="No tasks for this service." />
                  <template v-else>
                    <p v-if="tasksFor(service.id).error" class="realtime-inline-error" role="status">Task data may be stale: {{ tasksFor(service.id).error }}</p>
                    <AppTable fixed min-width="690px" table-class="realtime-tasks-table" :aria-label="`Tasks for ${service.name}`">
                      <template #head><tr><th>Task</th><th>Node</th><th>State</th><th>Updated</th><th>Error</th><th>Actions</th></tr></template>
                      <tr
                        v-for="task in tasksFor(service.id).tasks"
                        :key="task.id"
                        class="realtime-task-row"
                        :class="{ 'realtime-task-row--selected': selectedTaskID === task.id }"
                      >
                        <td data-label="Task"><button type="button" class="realtime-task-select" @click="selectTask(task)"><strong>Slot {{ task.slot }}</strong><code>{{ shortID(task.id) }}</code></button></td>
                        <td data-label="Node"><span>{{ task.node_name || task.node || "Unavailable" }}</span></td>
                        <td data-label="State"><AppStatusBadge class="realtime-state" :variant="taskStateVariant(task.current_state)" dot>{{ task.current_state || "Unknown" }}</AppStatusBadge></td>
                        <td data-label="Updated">{{ formatDate(task.updated_at) }}</td>
                        <td class="realtime-task-error-cell" data-label="Error">{{ task.error || "—" }}</td>
                        <td class="realtime-task-actions" data-label="Actions">
                          <button type="button" class="realtime-text-button" @click="selectTask(task)">Details</button>
                          <button type="button" class="realtime-text-button" @click="openLogs(task.id)">Logs</button>
                        </td>
                      </tr>
                    </AppTable>
                  </template>
                </div>
              </td>
            </tr>
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
.realtime-refresh-status { display: flex; align-items: center; justify-content: flex-end; gap: 8px; color: var(--muted); font-size: .76rem; }
.realtime-refresh-status button { font-size: .78rem; font-weight: 600; transform: none; filter: none; }
.realtime-auto-refresh, .realtime-refresh-button { display: inline-flex; align-items: center; gap: 7px; white-space: nowrap; }
.realtime-auto-refresh { border: 0; background: transparent; color: inherit; padding: 0; }
.realtime-refresh-button { min-height: 36px; padding: 7px 10px; border-radius: 7px; }
.realtime-refresh-button svg { width: 16px; height: 16px; fill: none; stroke: currentColor; stroke-width: 1.7; stroke-linecap: round; stroke-linejoin: round; }
.realtime-switch { position: relative; width: 26px; height: 16px; border: 1px solid var(--border-strong); border-radius: 999px; background: var(--surface-muted-strong); }
.realtime-switch span { position: absolute; top: 2px; left: 2px; width: 10px; height: 10px; border-radius: 50%; background: var(--muted); transition: transform 120ms ease; }
.realtime-switch--active { border-color: var(--success-border); background: var(--success-muted); }
.realtime-switch--active span { background: var(--success); transform: translateX(10px); }
.realtime-stale-warning, .realtime-inline-error { margin: 0; border: 1px solid var(--warning-border); border-radius: 7px; background: var(--warning-muted); color: var(--warning); padding: 9px 12px; font-size: .8rem; }
.realtime-filters { min-width: 0; display: grid; grid-template-columns: minmax(240px, 1fr) 180px 170px auto; gap: 10px; align-items: end; padding: 12px; border: 1px solid var(--border-subtle); border-radius: 10px; background: var(--surface-card-soft); }
.realtime-search { position: relative; min-width: 0; }
.realtime-search svg { position: absolute; left: 11px; top: 50%; width: 17px; height: 17px; color: var(--muted); fill: none; stroke: currentColor; stroke-width: 1.6; transform: translateY(-50%); pointer-events: none; }
.realtime-search input, .realtime-filter-field select, .realtime-stack-filter > summary { box-sizing: border-box; width: 100%; height: 38px; border: 1px solid var(--border-default); border-radius: 7px; background: var(--surface-input); color: var(--text); font: inherit; font-size: .83rem; }
.realtime-search input { padding: 7px 10px 7px 36px; }
.realtime-filter-field { display: grid; gap: 4px; color: var(--muted); font-size: .7rem; }
.realtime-filter-field select { padding: 7px 10px; }
.realtime-stack-filter { position: relative; min-width: 0; }
.realtime-stack-filter > summary { display: flex; align-items: center; justify-content: space-between; gap: 8px; padding: 7px 10px; cursor: pointer; list-style: none; }
.realtime-stack-filter > summary::-webkit-details-marker { display: none; }
.realtime-stack-filter-selection { min-width: 0; overflow: hidden; color: var(--text); text-overflow: ellipsis; white-space: nowrap; }
.realtime-stack-chip + .realtime-stack-chip::before { content: ", "; }
.realtime-stack-filter-menu { position: absolute; z-index: 30; top: calc(100% + 6px); left: 0; box-sizing: border-box; width: max(100%, 260px); max-width: calc(100vw - 32px); max-height: min(320px, calc(100dvh - 128px)); overflow-y: auto; border: 1px solid var(--border-default); border-radius: 8px; background: var(--surface-elevated); box-shadow: var(--shadow-popover); padding: 7px; }
.realtime-stack-filter-menu-header { display: flex; align-items: center; justify-content: space-between; padding: 5px 6px 8px; color: var(--text); }
.realtime-stack-filter-clear { border: 0; background: transparent; color: var(--accent-strong); padding: 0; font: inherit; }
.realtime-stack-filter-option { display: flex; align-items: center; gap: 9px; border-radius: 6px; padding: 7px 6px; color: var(--text); font-size: .82rem; }
.realtime-stack-filter-option:hover { background: var(--surface-hover-soft); }
.realtime-stack-filter-option input { flex: 0 0 16px; width: 16px; min-width: 16px; height: 16px; margin: 0; padding: 0; }
.realtime-result-count { align-self: center; color: var(--muted); font-size: .76rem; white-space: nowrap; font-variant-numeric: tabular-nums; }
.realtime-search input:focus-visible, .realtime-filter-field select:focus-visible, .realtime-stack-filter > summary:focus-visible, .realtime-page button:focus-visible { outline: 2px solid var(--accent-focus); outline-offset: 2px; }
.realtime-workspace { min-width: 0; display: grid; grid-template-columns: minmax(0, 1fr); gap: 16px; align-items: start; }
.realtime-workspace--details { grid-template-columns: minmax(0, 1fr) minmax(360px, 410px); }
.realtime-services-panel { min-width: 0; }
.realtime-services-panel :deep(.app-table-wrap) { min-width: 0; }
.realtime-services-panel :deep(.app-table th) { height: 44px; padding-inline: 14px; }
.realtime-services-panel :deep(.app-table td) { height: 52px; padding-inline: 14px; }
.realtime-col-service { width: 25%; }
.realtime-col-stack { width: 12%; }
.realtime-col-replicas { width: 9%; }
.realtime-col-image { width: 25%; }
.realtime-col-state { width: 13%; }
.realtime-col-updated { width: 16%; }
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
.realtime-replicas span { margin-inline: 3px; color: var(--muted); }
.realtime-state { border: 0; padding: 0; }
.realtime-image span { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .76rem; }
.realtime-service-row time { color: var(--muted); font-size: .76rem; white-space: nowrap; }
.realtime-tasks-row > td { height: auto !important; padding: 0 !important; background: var(--surface-input-strong); }
.realtime-tasks-container { min-width: 0; padding: 12px 14px 14px 46px; border-bottom: 1px solid var(--border-default); }
.realtime-tasks-heading { display: flex; align-items: baseline; gap: 8px; margin-bottom: 8px; }
.realtime-tasks-heading span { font-size: .78rem; font-weight: 700; }
.realtime-tasks-heading small { color: var(--muted); font-size: .7rem; }
.realtime-tasks-container :deep(.app-table-wrap) { border: 1px solid var(--border-subtle); border-radius: 8px; background: var(--surface-card-soft); }
.realtime-tasks-container :deep(.app-table th) { height: 36px; padding: 7px 10px; font-size: .7rem; }
.realtime-tasks-container :deep(.app-table td) { height: 46px; padding: 8px 10px; font-size: .78rem; }
.realtime-task-row--selected { background: var(--surface-selected); }
.realtime-task-select { width: 100%; min-width: 0; display: grid; gap: 2px; border: 0; background: transparent; color: var(--text); padding: 0; text-align: left; font: inherit; transform: none; filter: none; }
.realtime-task-select strong { font-size: .78rem; }
.realtime-task-select code { color: var(--muted); font-size: .68rem; }
.realtime-task-error-cell { max-width: 210px; color: var(--error); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.realtime-task-actions { white-space: nowrap; }
.realtime-text-button { border: 0; background: transparent; color: var(--control-primary); padding: 4px 5px; font: inherit; font-size: .76rem; font-weight: 600; transform: none; filter: none; }
.realtime-text-button:hover { text-decoration: underline; }
.realtime-mobile-panel-backdrop { display: none; }
:deep(.realtime-task-panel) { position: sticky; top: calc(var(--shell-header-height) + 16px); max-height: calc(100vh - var(--shell-header-height) - 44px); }

@media (max-width: 1180px) {
  .realtime-workspace--details { grid-template-columns: minmax(0, 1fr) minmax(340px, 380px); }
  .realtime-filters { grid-template-columns: minmax(220px, 1fr) 160px 150px; }
  .realtime-result-count { grid-column: 1 / -1; }
}

@media (max-width: 900px) {
  .realtime-header { align-items: stretch; flex-direction: column; gap: 10px; }
  .realtime-refresh-status { justify-content: flex-start; flex-wrap: wrap; }
  .realtime-filters { grid-template-columns: minmax(0, 1fr) minmax(130px, .45fr) minmax(130px, .45fr); }
  .realtime-search { grid-column: 1 / -1; }
  .realtime-workspace--details { grid-template-columns: minmax(0, 1fr); }
  :deep(.realtime-task-panel) { position: fixed; z-index: 61; inset: 8px; max-height: calc(100dvh - 16px); }
  .realtime-mobile-panel-backdrop { display: block; position: fixed; z-index: 60; inset: 0; width: 100%; height: 100%; border: 0; border-radius: 0; background: var(--backdrop); padding: 0; }
}

@media (max-width: 700px) {
  .realtime-page { gap: 12px; }
  .realtime-filters { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); padding: 10px; }
  .realtime-search, .realtime-result-count { grid-column: 1 / -1; }
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
  .realtime-service-row > td:nth-child(4) { grid-column: 1 / -1; grid-row: 3; }
  .realtime-service-row > td:nth-child(5) { grid-column: 2; grid-row: 1; justify-content: flex-end; }
  .realtime-service-row > td:nth-child(6) { display: none !important; }
  .realtime-image span { max-width: 100%; }
  .realtime-tasks-row { display: block; }
  .realtime-tasks-row > td { display: block; }
  .realtime-tasks-container { padding: 10px 12px 14px; }
  .realtime-tasks-container :deep(.app-table-wrap) { overflow: visible; border: 0; border-radius: 0; background: transparent; }
  .realtime-tasks-container :deep(.realtime-tasks-table) { display: block; min-width: 0 !important; }
  .realtime-tasks-container :deep(.realtime-tasks-table thead) { display: none; }
  .realtime-tasks-container :deep(.realtime-tasks-table tbody) { display: grid; gap: 8px; padding: 0; }
  .realtime-tasks-container :deep(.realtime-task-row) { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 5px 12px; border: 1px solid var(--border-subtle); border-radius: 7px; background: var(--surface-card-soft); padding: 10px; }
  .realtime-tasks-container :deep(.realtime-task-row--selected) { background: var(--surface-selected); }
  .realtime-tasks-container :deep(.realtime-task-row > td) { display: flex; min-width: 0; height: auto; padding: 0; border: 0; }
  .realtime-tasks-container :deep(.realtime-task-row > td:nth-child(1)) { grid-column: 1; grid-row: 1; }
  .realtime-tasks-container :deep(.realtime-task-row > td:nth-child(2)) { grid-column: 1; grid-row: 2; color: var(--muted); }
  .realtime-tasks-container :deep(.realtime-task-row > td:nth-child(3)) { grid-column: 2; grid-row: 1; justify-content: flex-end; }
  .realtime-tasks-container :deep(.realtime-task-row > td:nth-child(4)) { display: none; }
  .realtime-tasks-container :deep(.realtime-task-row > td:nth-child(5)) { grid-column: 1 / -1; grid-row: 3; max-width: none; white-space: normal; }
  .realtime-tasks-container :deep(.realtime-task-row > td:nth-child(6)) { grid-column: 2; grid-row: 2; justify-content: flex-end; }
}

@media (max-width: 460px) {
  .realtime-refresh-status > span { flex-basis: 100%; }
  .realtime-filter-field select, .realtime-stack-filter > summary, .realtime-search input { font-size: 16px; }
}
</style>
