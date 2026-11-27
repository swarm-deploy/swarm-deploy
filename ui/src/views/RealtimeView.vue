<script setup lang="ts">
import { syncPauseDurationOptions } from "../utils/syncPause";
import { formatNumber, plural, t } from "../i18n";
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { useRoute } from "vue-router";

import AppModal from "../components/common/AppModal.vue";
import { ApiError } from "../api/client";
import { fetchSwarmServices, fetchSwarmServiceTasks, fetchSwarmTask, restartSwarmService, scaleSwarmService } from "../api/services";
import {
  expirationForDuration,
  getServiceSyncPause,
  setServiceSyncPause,
} from "../api/syncPauses";
import type { ServiceRealtimeTask, ServiceSyncPauseResponse, SwarmService, SwarmTaskDetails } from "../api/types";
import AppButton from "../components/common/AppButton.vue";
import AppSelect from "../components/common/AppSelect.vue";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";
import SidebarIcon from "../components/layout/SidebarIcon.vue";
import DeploymentIcon from "../components/overview/DeploymentIcon.vue";
import ServiceReplicasBadge from "../components/services/ServiceReplicasBadge.vue";
import TaskDetailsPanel from "../components/services/TaskDetailsPanel.vue";
import TaskStatusBadge from "../components/services/TaskStatusBadge.vue";
import TaskLogsModal from "../components/services/TaskLogsModal.vue";
import { copyText } from "../utils/clipboard";
import { formatDate, formatRelativeTime } from "../utils/format";
import { serviceRuntimeState, type RuntimeState } from "../utils/realtime";

interface TaskListState {
  tasks: ServiceRealtimeTask[];
  loading: boolean;
  error: string;
}

const REFRESH_INTERVAL_STORAGE_KEY = "realtime.refreshInterval";
const refreshIntervals = [
  { label: t("realtime.refreshOff"), value: 0 },
  { label: t("common.secondsCompact", { count: formatNumber(5) }), value: 5_000 },
  { label: t("common.secondsCompact", { count: formatNumber(10) }), value: 10_000 },
  { label: t("common.secondsCompact", { count: formatNumber(30) }), value: 30_000 },
  { label: t("common.minutesCompact", { count: formatNumber(1) }), value: 60_000 },
  { label: t("common.minutesCompact", { count: formatNumber(5) }), value: 300_000 },
  { label: t("common.minutesCompact", { count: formatNumber(15) }), value: 900_000 },
  { label: t("common.minutesCompact", { count: formatNumber(30) }), value: 1_800_000 },
  { label: t("common.hoursCompact", { count: formatNumber(1) }), value: 3_600_000 },
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

const route = useRoute();
const linkedService = computed(() => typeof route.query.service === "string" ? route.query.service : "");
const linkedStack = computed(() => typeof route.query.stack === "string" ? route.query.stack : "");
const services = ref<SwarmService[]>([]);
const initialLoading = ref(true);
const refreshing = ref(false);
const refreshError = ref("");
const selectedStacks = ref<string[]>(linkedStack.value ? [linkedStack.value] : []);
const selectedServices = ref<string[]>(linkedService.value ? [linkedService.value] : []);
const stateFilter = ref<RuntimeState | "all">("all");
const stateOptions: { value: RuntimeState | "all"; label: string }[] = [
  { value: "all", label: t("realtime.allStates") },
  { value: "running", label: t("realtime.running") },
  { value: "degraded", label: t("realtime.degraded") },
  { value: "scaled-down", label: t("realtime.scaledDown") },
  { value: "unknown", label: t("common.unknown") },
];
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
const scalingService = ref<SwarmService | null>(null);
const actionsService = ref<SwarmService | null>(null);
const actionsPosition = ref({ top: "0px", right: "0px" });
const actionsTrigger = ref<HTMLElement | null>(null);
const actionsMenu = ref<HTMLElement | null>(null);
const restartingService = ref<SwarmService | null>(null);
const restartPending = ref(false);
const restartError = ref("");
const restartFailed = ref(false);

function closeActions(): void {
  actionsService.value = null;
  actionsTrigger.value?.focus();
}

function handleActionsFocusout(event: FocusEvent): void {
  const nextFocus = event.relatedTarget;
  // A tap can clear focus before the browser dispatches the click.
  if (!nextFocus || nextFocus === actionsTrigger.value || (nextFocus instanceof Node && actionsMenu.value?.contains(nextFocus))) return;
  actionsService.value = null;
}

async function toggleActions(service: SwarmService, event: MouseEvent): Promise<void> {
  if (actionsService.value?.id === service.id) { closeActions(); return; }
  actionsTrigger.value = event.currentTarget as HTMLElement;
  const rect = actionsTrigger.value.getBoundingClientRect();
  actionsPosition.value = { top: `${Math.min(rect.bottom + 4, window.innerHeight - 100)}px`, right: `${Math.min(window.innerWidth - 188, Math.max(8, window.innerWidth - rect.right))}px` };
  actionsService.value = service;
  await nextTick();
  actionsMenu.value?.querySelector("button")?.focus();
}

function openRestart(service: SwarmService): void {
  closeActions();
  restartingService.value = service;
  restartError.value = "";
  restartFailed.value = false;
}

async function submitRestart(): Promise<void> {
  if (!restartingService.value || restartPending.value || restartFailed.value) return;
  restartPending.value = true;
  try {
    await restartSwarmService(restartingService.value.id);
    if (!disposed) restartingService.value = null;
  } catch (reason) {
    if (!disposed) {
      restartFailed.value = true;
      restartError.value = t("realtime.restartWarning", { error: errorMessage(reason) });
    }
  } finally {
    if (!disposed) {
      for (const [id, state] of Object.entries(tasksByService.value)) {
        taskRequests.set(id, (taskRequests.get(id) ?? 0) + 1);
        state.loading = false;
      }
      await loadServices(true);
      restartPending.value = false;
    }
  }
}

const scaleInput = ref<number | string>("");
const scalePending = ref(false);
const scaleError = ref("");
const scaleInputElement = ref<HTMLInputElement | null>(null);
const scalePauseLoading = ref(false);
const scalePauseManaged = ref(false);
const scalePauseChecked = ref(false);
const scalePauseHelp = ref<"hover" | "pinned" | null>(null);
const scalePauseDuration = ref("1");
const scalePauseState = ref<ServiceSyncPauseResponse | null>(null);
const scalePausePersisted = ref(false);
const scalePauseLookupFailed = ref(false);
const scalePauseDurationOptions = syncPauseDurationOptions();
let scalePauseRequest = 0;

const validScaleInput = computed(() => (
  typeof scaleInput.value === "number" && Number.isSafeInteger(scaleInput.value) && scaleInput.value >= 0
));
const canScale = computed(() => (
  validScaleInput.value
  && scaleInput.value !== scalingService.value?.configured_replicas
  && !scalePending.value
  && !scalePauseLoading.value
  && !scalePauseLookupFailed.value
));

const scaleEffectivePauseLabel = computed(() => {
  const source = scalePauseState.value?.effective_source;
  if (source === "both") return t("realtime.stackAndService");
  if (source === "stack") return t("common.stack");
  if (source === "service") return t("common.service");
  return "";
});

function pauseUntil(expiresAt: string | null | undefined): string {
  return expiresAt ? formatDate(expiresAt) : t("realtime.noExpiration");
}

function showScalePauseHelp(event: PointerEvent): void {
  if (event.pointerType === "mouse" && !scalePauseHelp.value) scalePauseHelp.value = "hover";
}

function hideScalePauseHelp(event: PointerEvent): void {
  if (event.pointerType === "mouse" && scalePauseHelp.value === "hover") scalePauseHelp.value = null;
}

function dismissScalePauseHelp(event: PointerEvent): void {
  if (!(event.target as Element | null)?.closest(".realtime-scale-pause-help")) scalePauseHelp.value = null;
}

function onScaleEscape(event: KeyboardEvent): void {
  if (!scalePauseHelp.value) return;
  event.preventDefault();
  scalePauseHelp.value = null;
}

async function openScale(service: SwarmService): Promise<void> {
  closeActions();
  const request = ++scalePauseRequest;
  scalingService.value = service;
  scaleInput.value = service.configured_replicas ?? "";
  scaleError.value = "";
  scalePauseLoading.value = Boolean(service.stack && service.name);
  scalePauseManaged.value = false;
  scalePauseChecked.value = false;
  scalePauseHelp.value = null;
  scalePauseDuration.value = "1";
  scalePauseState.value = null;
  scalePausePersisted.value = false;
  scalePauseLookupFailed.value = false;
  if (service.stack && service.name) {
    try {
      const pauseState = await getServiceSyncPause(service.stack, service.name);
      if (disposed || request !== scalePauseRequest || scalingService.value?.id !== service.id) return;
      scalePauseState.value = pauseState;
      scalePauseManaged.value = true;
    } catch (reason) {
      if (disposed || request !== scalePauseRequest || scalingService.value?.id !== service.id) return;
      if (!(reason instanceof ApiError) || reason.status !== 404) {
        scalePauseLookupFailed.value = true;
        scaleError.value = errorMessage(reason);
      }
    } finally {
      if (!disposed && request === scalePauseRequest && scalingService.value?.id === service.id) {
        scalePauseLoading.value = false;
      }
    }
  }
  if (disposed || request !== scalePauseRequest || scalingService.value?.id !== service.id) return;
  await nextTick();
  scaleInputElement.value?.focus();
  scaleInputElement.value?.select();
}

function closeScale(): void {
  if (scalePending.value) return;
  scalePauseRequest++;
  scalingService.value = null;
}

function stepScale(delta: number): void {
  const current = validScaleInput.value ? Number(scaleInput.value) : 0;
  scaleInput.value = Math.min(Number.MAX_SAFE_INTEGER, Math.max(0, current + delta));
}

async function submitScale(): Promise<void> {
  if (!scalingService.value || !canScale.value) return;
  const serviceID = scalingService.value.id;
  scalePauseHelp.value = null;
  scalePending.value = true;
  scaleError.value = "";
  try {
    const service = scalingService.value;
    if (scalePauseManaged.value && scalePauseChecked.value && !scalePausePersisted.value) {
      scalePauseState.value = await setServiceSyncPause(
        service.stack,
        service.name,
        expirationForDuration(scalePauseDuration.value),
      );
      scalePausePersisted.value = true;
    }
    await scaleSwarmService(serviceID, Number(scaleInput.value));
    if (disposed) return;
    scalePending.value = false;
    closeScale();
    // Supersede pre-mutation polls, including task requests for expanded rows.
    for (const [id, state] of Object.entries(tasksByService.value)) {
      taskRequests.set(id, (taskRequests.get(id) ?? 0) + 1);
      state.loading = false;
    }
    await loadServices(true);
  } catch (reason) {
    if (!disposed) {
      scaleError.value = scalePausePersisted.value
        ? t("realtime.scaleAfterPauseFailed", { error: errorMessage(reason) })
        : errorMessage(reason);
    }
  } finally {
    if (!disposed) scalePending.value = false;
  }
}

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

const serviceOptions = computed(() => {
  const stacksByService = new Map<string, Set<string>>();
  for (const service of services.value) {
    if (selectedStacks.value.length > 0 && !selectedStacks.value.includes(service.stack)) continue;
    const stacks = stacksByService.get(service.name) ?? new Set<string>();
    stacks.add(service.stack);
    stacksByService.set(service.name, stacks);
  }
  for (const service of selectedServices.value) {
    if (!stacksByService.has(service)) stacksByService.set(service, new Set());
  }
  return [...stacksByService.entries()]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([service, stacks]) => {
      const stackNames = [...stacks].sort((left, right) => left.localeCompare(right));
      return {
        value: service,
        label: service,
        description: selectedStacks.value.length === 1 ? undefined : stackNames.map(stackLabel).join(", "),
        searchText: `${service} ${stackNames.join(" ")}`,
      };
    });
});

const selectedStackSummary = computed(() => {
  if (selectedStacks.value.length === 0) return t("realtime.allStacks");
  if (selectedStacks.value.length <= 2) return selectedStacks.value.map(stackLabel).join(", ");
  return plural("common.stacksSelected", selectedStacks.value.length);
});

const selectedServiceSummary = computed(() => {
  if (selectedServices.value.length === 0) return t("realtime.allServices");
  if (selectedServices.value.length <= 2) return selectedServices.value.join(", ");
  return plural("common.servicesSelected", selectedServices.value.length);
});

const filteredServices = computed(() => {
  const query = searchQuery.value.trim().toLocaleLowerCase();
  return services.value.filter((service) => {
    if (selectedStacks.value.length > 0 && !selectedStacks.value.includes(service.stack)) return false;
    if (selectedServices.value.length > 0 && !selectedServices.value.includes(service.name)) return false;
    if (stateFilter.value !== "all" && serviceRuntimeState(service) !== stateFilter.value) return false;
    if (!query) return true;
    return [service.name, service.full_name, service.stack, service.image]
      .some((value) => value.toLocaleLowerCase().includes(query));
  });
});

const automaticRefresh = computed(() => refreshInterval.value !== 0);
const refreshIntervalLabel = computed(() => (
  refreshIntervals.find((interval) => interval.value === refreshInterval.value)?.label ?? refreshIntervals[1].label
));

function errorMessage(reason: unknown): string {
  return reason instanceof Error ? reason.message : t("common.requestFailed");
}

function stackLabel(stack: string): string {
  return stack || t("realtime.standalone");
}

function selectStacks(stacks: string[]): void {
  const availableServices = new Set(services.value
    .filter((service) => stacks.length === 0 || stacks.includes(service.stack))
    .map((service) => service.name));
  selectedStacks.value = stacks;
  selectedServices.value = selectedServices.value.filter((service) => availableServices.has(service));
}

function selectServices(selected: string[]): void {
  selectedServices.value = selected;
}

function shortID(id: string): string {
  if (!id) return t("common.notApplicable");
  if (id.length <= 18) return id;
  return `${id.slice(0, 10)}…${id.slice(-6)}`;
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

async function loadServices(force = false): Promise<void> {
  if ((refreshing.value && !force) || disposed) return;
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
  if (!(event.target as Element | null)?.closest(".realtime-actions-menu, .realtime-actions-button")) actionsService.value = null;
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

watch([linkedService, linkedStack, services], ([name, stack], previous) => {
  if (!name) return;
  const service = services.value.find((item) => item.name === name && item.stack === stack);
  if (service && (name !== previous?.[0] || stack !== previous?.[1] || !previous?.[2]?.length)
      && !expandedServiceIDs.value.has(service.id)) toggleService(service);
});

onMounted(() => {
  document.addEventListener("visibilitychange", handleVisibilityChange);
  document.addEventListener("click", handleDocumentClick, true);
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
  document.removeEventListener("click", handleDocumentClick, true);
});
</script>

<template>
  <section class="realtime-page">
    <header class="realtime-header">
      <div class="realtime-heading">
        <div class="realtime-title-row">
          <h2 class="mobile-title-duplicate">{{ t('common.realtime') }}</h2>
          <span class="live-indicator realtime-live-indicator" :class="{ 'live-indicator--paused': !automaticRefresh }">
            <span aria-hidden="true" />{{ automaticRefresh ? t('realtime.active') : t('realtime.paused') }}
          </span>
        </div>
        <p>{{ t('realtime.description') }}</p>
      </div>
      <div class="realtime-refresh-control">
        <AppButton
          variant="secondary"
          class="realtime-refresh-button"
          :disabled="refreshing"
          :aria-busy="refreshing"
          @click="loadServices()"
        >
          <svg viewBox="0 0 20 20" aria-hidden="true"><path d="M16.5 6.5V2.8m0 0h-3.7m3.7 0-2.2 2.1a6.5 6.5 0 1 0 1.4 6.9" /></svg>
          {{ t('realtime.refresh') }}
        </AppButton>
        <details ref="refreshMenu" class="realtime-refresh-dropdown" @keydown="handleRefreshMenuKeydown">
          <summary :aria-label="t('realtime.refreshIntervalValue', { interval: refreshIntervalLabel })">
            <span>{{ refreshIntervalLabel }}</span>
            <svg viewBox="0 0 16 16" aria-hidden="true"><path d="m4 6 4 4 4-4" /></svg>
          </summary>
          <div class="realtime-refresh-menu" role="menu" :aria-label="t('realtime.refreshInterval')">
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
      {{ t('realtime.staleDataWarning', { error: refreshError }) }}
    </p>
    <div class="realtime-filters" :aria-label="t('realtime.filters')">
      <label class="realtime-search">
        <svg class="realtime-filter-icon" viewBox="0 0 20 20" aria-hidden="true"><circle cx="8.5" cy="8.5" r="5.5" /><path d="m12.5 12.5 4 4" /></svg>
        <span class="sr-only">{{ t('realtime.searchServices') }}</span>
        <input v-model="searchQuery" type="search" :placeholder="t('realtime.searchPlaceholder')" />
      </label>
      <div class="realtime-filter-field">
        <AppSelect
          multiple
          class="realtime-stack-filter"
          :model-value="selectedStacks"
          :options="stackOptions"
          :label="t('realtime.filterStack')"
          :placeholder="t('realtime.allStacks')"
          :heading="t('common.stacks')"
          :selection-label="selectedStackSummary"
          @update:model-value="selectStacks"
        >
          <template #icon>
            <SidebarIcon class="realtime-filter-icon" name="services" />
          </template>
        </AppSelect>
      </div>
      <div class="realtime-filter-field">
        <AppSelect
          multiple
          class="realtime-service-filter"
          :model-value="selectedServices"
          :options="serviceOptions"
          :label="t('realtime.filterService')"
          :placeholder="t('realtime.allServices')"
          :heading="t('common.services')"
          :selection-label="selectedServiceSummary"
          searchable
          :search-label="t('common.searchServices')"
          :search-placeholder="t('common.searchServices')"
          :empty-message="t('common.noMatchingServices')"
          descriptive
          @update:model-value="selectServices"
        >
          <template #icon>
            <DeploymentIcon class="realtime-filter-icon" name="service" />
          </template>
        </AppSelect>
      </div>
      <AppSelect
        v-model="stateFilter"
        class="realtime-filter-field realtime-state-filter"
        :options="stateOptions"
        :label="t('realtime.filterState')"
      >
        <template #icon>
          <SidebarIcon class="realtime-filter-icon" name="realtime" />
        </template>
      </AppSelect>
    </div>

    <div class="realtime-workspace" :class="{ 'realtime-workspace--details': selectedTaskID }">
      <div class="realtime-services-panel">
        <AppTableEmpty v-if="initialLoading && services.length === 0" :message="t('realtime.loadingServices')" />
        <AppTableEmpty v-else-if="refreshError && services.length === 0" role="alert">
          {{ t('realtime.loadServicesError', { error: refreshError }) }}
          <AppButton variant="secondary" compact @click="loadServices()">{{ t('common.retry') }}</AppButton>
        </AppTableEmpty>
        <AppTableEmpty v-else-if="services.length === 0" :message="t('realtime.noServices')" />
        <AppTableEmpty v-else-if="filteredServices.length === 0" :message="t('realtime.noFilterMatches')" />

        <AppTable v-else fixed min-width="900px" table-class="realtime-services-table" :aria-label="t('realtime.swarmServices')">
          <template #colgroup>
            <col class="realtime-col-service" />
            <col class="realtime-col-stack" />
            <col class="realtime-col-state" />
            <col class="realtime-col-updated" />
            <col class="realtime-col-actions" />
          </template>
          <template #head>
            <tr><th>{{ t('common.service') }}</th><th>{{ t('common.stack') }}</th><th>{{ t('common.state') }}</th><th>{{ t('common.updated') }}</th><th>{{ t('common.action') }}</th></tr>
          </template>
          <template v-for="service in filteredServices" :key="service.id">
            <tr class="realtime-service-row" :class="{ 'realtime-service-row--expanded': expandedServiceIDs.has(service.id) }" @click="toggleService(service)">
              <td class="realtime-service-name-cell" :data-label="t('common.service')">
                <div>
                  <span class="realtime-service-title">
                    <button type="button" class="realtime-expand-button" :aria-expanded="expandedServiceIDs.has(service.id)" :aria-label="t(expandedServiceIDs.has(service.id) ? 'realtime.collapseService' : 'realtime.expandService', { service: service.name || service.full_name })" @click.stop="toggleService(service)">
                      <svg viewBox="0 0 16 16" aria-hidden="true"><path d="m6 3 5 5-5 5" /></svg>
                    </button>
                    <strong>{{ service.name || service.full_name }}</strong>
                  </span>
                  <small v-if="service.full_name !== service.name">{{ service.full_name }}</small>
                </div>
              </td>
              <td class="realtime-stack-cell" :data-label="t('common.stack')"><span :class="{ 'realtime-standalone': !service.stack }">{{ service.stack || t('realtime.standalone') }}</span></td>
              <td class="realtime-state-cell" :data-label="t('common.state')">
                <ServiceReplicasBadge :replicas="service.replicas" compact />
              </td>
              <td class="realtime-updated-cell" :data-label="t('common.updated')"><time :datetime="service.updated_at" :title="formatDate(service.updated_at)">{{ formatRelativeTime(service.updated_at) }}</time></td>
              <td class="realtime-actions-cell" :data-label="t('common.action')" @click.stop>
                <button v-if="service.mode === 'replicated'" class="realtime-actions-button" type="button" :aria-label="t('realtime.actionsForService', { service: service.name || service.full_name })" :aria-expanded="actionsService?.id === service.id" aria-haspopup="true" :disabled="restartPending" @click.stop="toggleActions(service, $event)" @focusout="handleActionsFocusout">⋯</button>
              </td>
            </tr>
            <template v-if="expandedServiceIDs.has(service.id)">
              <tr v-if="tasksFor(service.id).loading && tasksFor(service.id).tasks.length === 0" class="realtime-task-message-row">
                <td colspan="5"><span>{{ t('realtime.loadingTasks') }}</span></td>
              </tr>
              <tr v-else-if="tasksFor(service.id).error && tasksFor(service.id).tasks.length === 0" class="realtime-task-message-row realtime-task-message-row--error">
                <td colspan="5"><span role="alert">{{ t('realtime.loadTasksError', { error: tasksFor(service.id).error }) }}</span></td>
              </tr>
              <tr v-else-if="tasksFor(service.id).tasks.length === 0" class="realtime-task-message-row">
                <td colspan="5"><span>{{ t('realtime.noTasks') }}</span></td>
              </tr>
              <template v-else>
                <tr v-if="tasksFor(service.id).error" class="realtime-task-message-row realtime-task-message-row--warning">
                  <td colspan="5"><span role="status">{{ t('realtime.staleTasksWarning', { error: tasksFor(service.id).error }) }}</span></td>
                </tr>
                <tr
                  v-for="task in tasksFor(service.id).tasks"
                  :key="task.id"
                  class="realtime-task-row"
                  :class="{ 'realtime-task-row--selected': selectedTaskID === task.id }"
                >
                  <td colspan="5">
                    <div class="realtime-task-grid">
                      <button type="button" class="realtime-task-select" @click="selectTask(task)"><strong>{{ t('realtime.slotNumber', { slot: formatNumber(task.slot) }) }}</strong><code>{{ shortID(task.id) }}</code></button>
                      <span class="realtime-task-node">{{ task.node_name || task.node || t('common.unavailable') }}</span>
                      <TaskStatusBadge class="realtime-task-state" :state="task.current_state" />
                      <time :datetime="task.updated_at" :title="formatDate(task.updated_at)">{{ formatRelativeTime(task.updated_at) }}</time>
                      <span class="realtime-task-error-cell" :class="{ 'realtime-task-error-cell--failed': task.error && isFailedTask(task.current_state) }">
                        <span :title="task.error || undefined">{{ task.error || "—" }}</span>
                        <button
                          v-if="task.error"
                          type="button"
                          class="realtime-task-error-copy"
                          :aria-label="copiedErrorTaskID === task.id ? t('realtime.taskErrorCopied') : t('realtime.copyTaskError')"
                          :title="copiedErrorTaskID === task.id ? t('common.copied') : t('realtime.copyFullError')"
                          @click="copyTaskError(task)"
                        >
                          <svg v-if="copiedErrorTaskID === task.id" class="realtime-task-error-copy-check" viewBox="0 0 16 16" aria-hidden="true"><path d="m3 8.5 3 3L13 4.8" /></svg>
                          <svg v-else viewBox="0 0 16 16" aria-hidden="true"><rect x="5" y="2" width="8" height="9" rx="1.5" /><path d="M3 5v7.5A1.5 1.5 0 0 0 4.5 14H11" /></svg>
                        </button>
                      </span>
                      <span class="realtime-task-actions">
                        <button type="button" class="realtime-task-action-button" :aria-label="t('realtime.openTaskDetails', { task: task.id })" :title="t('realtime.taskDetails')" @click="selectTask(task)">
                          <svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="3" cy="4" r=".7" /><circle cx="3" cy="8" r=".7" /><circle cx="3" cy="12" r=".7" /><path d="M6 4h7M6 8h7M6 12h7" /></svg>
                        </button>
                        <button type="button" class="realtime-task-action-button" :aria-label="t('realtime.openTaskLogs', { task: task.id })" :title="t('realtime.taskLogs')" @click="openLogs(task.id)">
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

      <button v-if="selectedTaskID" type="button" class="realtime-mobile-panel-backdrop" :aria-label="t('realtime.closeTaskDetails')" @click="closeTaskDetails" />
      <TaskDetailsPanel
        v-if="selectedTaskID"
        :task="selectedTask"
        :loading="taskDetailsLoading"
        :error="taskDetailsError"
        @close="closeTaskDetails"
      />
    </div>

    <Teleport to="body">
      <div v-if="actionsService" ref="actionsMenu" class="realtime-actions-menu" :style="actionsPosition" :aria-label="t('realtime.serviceActions')" @click.stop @keydown.esc.stop.prevent="closeActions" @focusout="handleActionsFocusout">
        <button v-if="(actionsService.configured_replicas ?? 0) > 0 && actionsService.replicas.desired > 0" type="button" @click="openRestart(actionsService)">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 7v5h-5M4 17v-5h5M6.1 6.1a8 8 0 0 1 13.2 3M4.7 14.9a8 8 0 0 0 13.2 3" /></svg>{{ t('realtime.restartService') }}
        </button>
        <button type="button" class="realtime-scale-button" :aria-label="t('realtime.scaleNamedService', { service: actionsService.name || actionsService.full_name })" @click="openScale(actionsService)">
          <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M5 4h6M8 1v6M5 12h6" /></svg>{{ t('realtime.scaleReplicas') }}
        </button>
      </div>
    </Teleport>
    <AppModal :open="Boolean(restartingService)" labelledby="realtime-restart-title" card-class="realtime-scale-card" :busy="restartPending" :close-disabled="restartPending" @close="restartingService = null">
      <template #title><h2 id="realtime-restart-title">{{ t('realtime.restartService') }}</h2></template>
      <div class="modal-body">
        <p>{{ t('realtime.confirmRestart', { service: restartingService?.name || restartingService?.full_name || '' }) }}</p>
        <p v-if="restartError" class="realtime-scale-error" role="alert">{{ restartError }}</p>
        <div class="realtime-scale-actions">
          <AppButton variant="secondary" :disabled="restartPending" @click="restartingService = null">{{ restartFailed ? t('common.close') : t('common.cancel') }}</AppButton>
          <AppButton v-if="!restartFailed" :disabled="restartPending" @click="submitRestart">{{ restartPending ? t('realtime.restarting') : t('realtime.restart') }}</AppButton>
        </div>
      </div>
    </AppModal>

    <AppModal
      :open="Boolean(scalingService)"
      labelledby="realtime-scale-title"
      card-class="realtime-scale-card"
      v-if="scalingService"
      class="realtime-scale-modal"
      as="form"
      :busy="scalePending"
      :close-disabled="scalePending"
      :close-on-escape="!scalePauseHelp"
      :close-label="t('realtime.closeScaling')"
      initial-focus="#realtime-scale-replicas"
      @pointerdown="dismissScalePauseHelp"
      @escape="onScaleEscape"
      @submit.prevent="submitScale"
      @close="closeScale"
    >
      <template #title>
        <h2 id="realtime-scale-title">{{ t('realtime.scaleService') }}</h2>
      </template>
      <div class="modal-body">
        <p class="realtime-scale-section-label">{{ t('realtime.serviceDetails') }}</p>
        <dl class="realtime-scale-summary">
          <dt>{{ t('common.service') }}</dt><dd>{{ scalingService.name || scalingService.full_name }}</dd>
          <dt>{{ t('common.stack') }}</dt><dd>{{ stackLabel(scalingService.stack) }}</dd>
          <dt>{{ t('realtime.currentScale') }}</dt><dd>{{ scalingService.configured_replicas == null ? t('common.unavailable') : formatNumber(scalingService.configured_replicas) }}</dd>
        </dl>
        <hr class="realtime-scale-divider" />
        <p class="realtime-scale-section-label">{{ t('realtime.scale') }}</p>
        <label for="realtime-scale-replicas">{{ t('realtime.desiredReplicas') }}</label>
        <div class="realtime-scale-input">
          <AppButton variant="secondary" compact :aria-label="t('realtime.decreaseReplicas')" :disabled="scalePending || scaleInput === 0" @click="stepScale(-1)">−</AppButton>
          <input id="realtime-scale-replicas" ref="scaleInputElement" v-model.number="scaleInput" type="number" min="0" step="1" :max="Number.MAX_SAFE_INTEGER" required :disabled="scalePending" :aria-invalid="!validScaleInput" />
          <AppButton variant="secondary" compact :aria-label="t('realtime.increaseReplicas')" :disabled="scalePending || scaleInput === Number.MAX_SAFE_INTEGER" @click="stepScale(1)">+</AppButton>
        </div>
        <p v-if="scalePauseLoading || scalePauseState?.effective_paused || scalePauseChecked" class="form-helper" role="note">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true"><circle cx="12" cy="12" r="9" /><path d="M12 11v6m0-10h.01" /></svg>
          <span v-if="scalePauseLoading">{{ t('realtime.checkingPause') }}</span>
          <span v-else-if="scalePauseState?.effective_paused">{{ t('realtime.scaleDuringPause') }}</span>
          <span v-else>{{ t('realtime.scaleAfterPause') }}</span>
        </p>
        <template v-if="scalePauseManaged && !scalePauseLoading">
          <div v-if="scalePauseState?.effective_paused" class="realtime-scale-pause-state" role="status">
            <strong>{{ t('realtime.pausedByScope', { scope: scaleEffectivePauseLabel }) }}</strong>
            <span v-if="scalePauseState.stack_pause">{{ t('realtime.stackPauseExpires', { expiration: pauseUntil(scalePauseState.stack_pause.expires_at) }) }}</span>
            <span v-if="scalePauseState.service_pause">{{ t('realtime.servicePauseExpires', { expiration: pauseUntil(scalePauseState.service_pause.expires_at) }) }}</span>
          </div>
          <template v-else>
            <div class="realtime-scale-pause-row">
              <label class="realtime-scale-pause-toggle">
                <input v-model="scalePauseChecked" type="checkbox" :disabled="scalePending || scalePausePersisted" />
                <span>{{ t('common.pauseSync') }}</span>
              </label>
              <div class="realtime-scale-pause-help" @pointerenter="showScalePauseHelp" @pointerleave="hideScalePauseHelp">
                <button
                  type="button"
                  class="realtime-scale-pause-help-button"
                  :aria-label="t('realtime.aboutPause')"
                  :aria-expanded="Boolean(scalePauseHelp)"
                  :aria-describedby="scalePauseHelp ? 'realtime-scale-pause-tooltip' : undefined"
                  @click="scalePauseHelp = scalePauseHelp === 'pinned' ? null : 'pinned'"
                >
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><circle cx="12" cy="12" r="9" /><path d="M9.5 9.5a2.5 2.5 0 0 1 5 0c0 1.5-2.5 2-2.5 3.5m0 3h.01" /></svg>
                </button>
                <div v-if="scalePauseHelp" id="realtime-scale-pause-tooltip" class="realtime-scale-pause-tooltip" role="tooltip">
                  {{ t('realtime.scaleMayBeOverwritten') }}
                </div>
              </div>
            </div>
            <div v-if="scalePauseChecked" class="realtime-scale-pause-duration">
              <span>{{ t('realtime.pauseDuration') }}</span>
              <AppSelect v-model="scalePauseDuration" :options="scalePauseDurationOptions" :label="t('realtime.pauseDuration')" />
            </div>
          </template>
        </template>
        <p v-if="scalePausePersisted && scaleError" class="form-helper" role="status">
          {{ t('realtime.retryScaling') }}
        </p>
        <p v-if="scaleError" class="realtime-scale-error" role="alert">{{ scaleError }}</p>
        <div class="realtime-scale-actions">
          <AppButton variant="secondary" :disabled="scalePending" @click="closeScale">{{ t('common.cancel') }}</AppButton>
          <AppButton type="submit" :disabled="!canScale">{{ scalePending ? t('realtime.scaling') : t('realtime.scale') }}</AppButton>
        </div>
      </div>
    </AppModal>

    <TaskLogsModal :open="logsOpen" :task-id="logsTaskID" @close="logsOpen = false" />
  </section>
</template>

<style scoped>
.realtime-col-actions { width: 64px; }
.realtime-actions-cell { text-align: center; }
.realtime-actions-button { width: 28px; height: 26px; padding: 0; background: transparent; color: var(--text); border: 1px solid var(--line); border-radius: 4px; font-size: 20px; line-height: 1; }
.realtime-actions-menu { position: fixed; z-index: 50; display: grid; min-width: 170px; padding: 4px; border: 1px solid var(--line); border-radius: 6px; background: var(--surface-elevated); }
.realtime-actions-menu button { display: flex; align-items: center; gap: 8px; width: 100%; height: 32px; padding: 6px 8px; border: 0; background: transparent; color: var(--text); font-size: .8rem; text-align: left; border-radius: 4px; }
.realtime-actions-menu button:hover { background: var(--surface-hover); }
.realtime-actions-menu button:focus-visible, .realtime-actions-button:focus-visible { outline: 2px solid var(--accent-focus); }
.realtime-actions-menu svg { width: 16px; height: 16px; fill: none; stroke: currentColor; stroke-width: 1.5; stroke-linecap: round; stroke-linejoin: round; }

:deep(.realtime-scale-card) { width: min(440px, 94vw); }
.realtime-scale-section-label { margin: 0; color: var(--muted); font-size: .7rem; font-weight: 600; letter-spacing: .055em; text-transform: uppercase; }
.realtime-scale-summary { display: grid; grid-template-columns: auto minmax(0, 1fr); gap: 8px 16px; margin: 0; font-size: .84rem; }
.realtime-scale-divider { width: 100%; margin: 8px 0 2px; border: 0; border-top: 1px solid var(--line); }
.realtime-scale-summary dt { color: var(--muted); }
.realtime-scale-summary dd { margin: 0; overflow-wrap: anywhere; }
.realtime-scale-pause-state { display: grid; gap: 4px; padding: 10px; border: 1px solid var(--line); border-radius: 6px; font-size: .8rem; }
.realtime-scale-pause-state span { color: var(--muted); }
.realtime-scale-pause-row { position: relative; display: flex; align-items: center; gap: 8px; }
.realtime-scale-pause-toggle { display: flex; align-items: center; gap: 8px; font-size: .84rem; }
.realtime-scale-pause-help { display: flex; }
.realtime-scale-pause-help-button { display: inline-grid; place-items: center; width: 25px; height: 25px; padding: 2px; border: 0; border-radius: 50%; background: transparent; color: var(--muted); cursor: help; }
.realtime-scale-pause-help-button svg { width: 20px; height: 20px; }
.realtime-scale-pause-help-button:hover, .realtime-scale-pause-help-button[aria-expanded="true"] { color: var(--text); transform: none; filter: none; }
.realtime-scale-pause-help-button:focus-visible { outline: 2px solid var(--interactive-primary); outline-offset: 2px; }
.realtime-scale-pause-tooltip {
  position: absolute;
  z-index: 10;
  bottom: 100%;
  left: 0;
  width: min(315px, 100%);
  padding: 11px 13px;
  border: 1px solid var(--border-default);
  border-radius: 8px;
  background: var(--surface-elevated);
  box-shadow: var(--shadow-popover);
  color: var(--text);
  font-size: .82rem;
  line-height: 1.5;
}
.realtime-scale-pause-duration { display: grid; gap: 6px; font-size: .8rem; }
.realtime-scale-input { display: flex; align-items: stretch; min-width: 0; height: 32px; overflow: hidden; border: 1px solid var(--border-default); border-radius: 6px; background: var(--surface-input); }
.realtime-scale-input:focus-within { border-color: var(--accent-border); outline: 2px solid var(--accent-focus); outline-offset: 1px; }
.realtime-scale-input input { flex: 1 1 auto; min-width: 0; width: 100%; height: 100%; border: 0; border-radius: 0; outline: 0; background: transparent; color: var(--text); padding: 4px 8px; text-align: center; font: inherit; font-size: .84rem; appearance: textfield; }
.realtime-scale-input input::-webkit-inner-spin-button, .realtime-scale-input input::-webkit-outer-spin-button { appearance: none; margin: 0; }
.realtime-scale-input button { display: inline-grid; place-items: center; flex: 0 0 28px; width: 28px; min-width: 0; border: 0; border-radius: 0; background: transparent; color: var(--interactive-primary); padding: 0; font-size: 16px; line-height: 1; transform: none; filter: none; }
.realtime-scale-input button:hover:not(:disabled) { background: var(--surface-hover); }
.realtime-scale-input button:focus-visible { outline: 2px solid var(--accent-focus); outline-offset: -3px; }
.realtime-scale-modal button:disabled { cursor: not-allowed; opacity: .58; }
.realtime-scale-error { margin: 0; color: var(--error); overflow-wrap: anywhere; }
.realtime-scale-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 4px; }

.realtime-page { display: grid; gap: 16px; min-width: 0; }
.realtime-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 20px; min-width: 0; }
.realtime-heading { display: grid; gap: 4px; min-width: 0; }
.realtime-title-row { display: flex; align-items: center; gap: 10px; }
.realtime-title-row h2 { margin: 0; font-size: 1.45rem; line-height: 1.2; }
.realtime-heading p { margin: 0; color: var(--muted); font-size: .84rem; }
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
.realtime-refresh-option svg { width: 15px; height: 15px; fill: none; stroke: var(--interactive-primary); stroke-width: 1.8; stroke-linecap: round; stroke-linejoin: round; }
.realtime-stale-warning, .realtime-inline-error { margin: 0; border: 1px solid var(--warning-border); border-radius: 7px; background: var(--warning-muted); color: var(--warning); padding: 9px 12px; font-size: .8rem; }
.realtime-filters { min-width: 0; display: grid; grid-template-columns: minmax(240px, 1fr) 180px 180px 170px; gap: 10px; align-items: center; padding: 12px; border: 1px solid var(--border-subtle); border-radius: 10px; background: var(--surface-card-soft); }
.realtime-search { position: relative; min-width: 0; }
.realtime-filter-icon { width: 17px; height: 17px; flex: 0 0 17px; color: var(--muted); fill: none; stroke: currentColor; stroke-width: 1.6; pointer-events: none; }
.realtime-search .realtime-filter-icon { position: absolute; z-index: 1; left: 11px; top: 50%; transform: translateY(-50%); }
.realtime-search input { box-sizing: border-box; width: 100%; height: 38px; border: 1px solid var(--border-default); border-radius: 7px; background: var(--surface-input); color: var(--text); font: inherit; font-size: .83rem; }
.realtime-search input { padding: 7px 10px 7px 36px; }
.realtime-filter-field { position: relative; min-width: 0; color: var(--muted); }
.realtime-state-filter :deep(.app-select-menu) { left: auto; right: 0; }
.realtime-search input:focus-visible, .realtime-page button:focus-visible { outline: 2px solid var(--accent-focus); outline-offset: 2px; }
.realtime-workspace { min-width: 0; display: grid; grid-template-columns: minmax(0, 1fr); gap: 16px; align-items: start; }
.realtime-workspace--details { grid-template-columns: minmax(0, 1fr) minmax(360px, 410px); }
.realtime-services-panel { min-width: 0; }
.realtime-services-panel :deep(.app-table-wrap) { min-width: 0; }
.realtime-services-panel :deep(.app-table th) { height: 40px; padding: 7px 14px; }
.realtime-services-panel :deep(.app-table td) { height: 46px; padding: 9px 14px; }
.realtime-col-service { width: 38%; }
.realtime-col-stack { width: 20%; }
.realtime-col-state { width: 20%; }
.realtime-col-updated { width: auto; }
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
.realtime-task-action-button { display: inline-grid; place-items: center; width: 28px; height: 28px; border: 0; border-radius: 4px; background: transparent; color: var(--interactive-primary); padding: 0; transform: none; filter: none; }
.realtime-task-action-button:hover { background: var(--surface-hover); transform: none; filter: none; }
.realtime-task-action-button svg { width: 16px; height: 16px; fill: none; stroke: #606060; stroke-width: 1.5; stroke-linecap: round; stroke-linejoin: round; }
.realtime-mobile-panel-backdrop { display: none; }
:deep(.realtime-task-panel) { position: sticky; top: calc(var(--shell-header-height) + 16px); max-height: calc(100vh - var(--shell-header-height) - 44px); }

@media (max-width: 1180px) {
  .realtime-workspace--details { grid-template-columns: minmax(0, 1fr) minmax(340px, 380px); }
  .realtime-filters { grid-template-columns: repeat(3, minmax(150px, 1fr)); }
  .realtime-search { grid-column: 1 / -1; }
}

@media (max-width: 900px) {
  .realtime-header { align-items: stretch; flex-direction: column; gap: 10px; }
  .realtime-refresh-control { align-self: flex-end; }
  .realtime-filters { grid-template-columns: repeat(3, minmax(130px, 1fr)); }
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
  .realtime-state-filter { grid-column: 1 / -1; }
  .realtime-services-panel :deep(.app-table-wrap) { overflow: visible; }
  .realtime-services-panel :deep(.realtime-services-table) { display: block; min-width: 0 !important; }
  .realtime-services-panel :deep(.realtime-services-table colgroup), .realtime-services-panel :deep(.realtime-services-table > thead) { display: none; }
  .realtime-services-panel :deep(.realtime-services-table > tbody) { display: block; }
  .realtime-service-row { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 7px 12px; padding: 12px; border-bottom: 1px solid var(--border-subtle); }
  .realtime-services-panel :deep(.realtime-service-row > td) { display: flex; min-width: 0; height: auto; padding: 0; border: 0; }
  .realtime-service-name-cell { grid-column: 1; grid-row: 1; }
  .realtime-service-title strong { order: 1; }
  .realtime-service-title .realtime-expand-button { order: 2; }
  .realtime-stack-cell { grid-column: 1; grid-row: 2; align-items: center; font-size: .76rem; }
  .realtime-state-cell { grid-column: 2; grid-row: 1; align-items: center; justify-content: flex-end; }
  .realtime-actions-cell { grid-column: 2; grid-row: 2; justify-content: flex-end; }
  .realtime-updated-cell { display: none !important; }
  .realtime-task-row, .realtime-task-message-row { display: block; }
  .realtime-task-row > td, .realtime-task-message-row > td { display: block; }
  .realtime-task-grid { width: calc(100% - 28px); grid-template-columns: minmax(0, 1fr) auto; gap: 5px 12px; min-height: 0; margin-left: 28px; padding: 9px 12px; }
  .realtime-task-select { grid-column: 1; grid-row: 1; }
  .realtime-task-node { grid-column: 1; grid-row: 2; color: var(--muted); }
  .realtime-task-grid > .realtime-task-state { grid-column: 2; grid-row: 1; justify-self: end; }
  .realtime-task-grid time { grid-column: 2; grid-row: 2; justify-self: end; }
  .realtime-task-error-cell { grid-column: 1 / -1; grid-row: 3; }
  .realtime-task-actions { position: static; grid-column: 1 / -1; grid-row: 4; justify-content: flex-start; transform: none; }
  .realtime-task-message-row > td { padding-left: 42px !important; }
}

@media (max-width: 460px) {
  .realtime-search input { font-size: 16px; }
}
</style>
