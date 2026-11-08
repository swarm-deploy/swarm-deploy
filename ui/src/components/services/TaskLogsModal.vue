<script setup lang="ts">
import json from "@speed-highlight/core/languages/json.js";
import { tokenizeWith } from "@speed-highlight/core/tokenize";
import { computed, nextTick, onUnmounted, ref, watch } from "vue";

import AppButton from "../common/AppButton.vue";
import AppCopyButton from "../common/AppCopyButton.vue";
import AppModal from "../common/AppModal.vue";
import AppSelect from "../common/AppSelect.vue";
import AppTabs from "../common/AppTabs.vue";
import SeverityIcon from "../common/SeverityIcon.vue";
import { openTaskLogsStream } from "../../api/overview";
import type { TaskLogCountsEvent, TaskLogEvent, TaskLogLevel } from "../../api/types";
import { copyText } from "../../utils/clipboard";

const props = defineProps<{
  open: boolean;
  taskId: string;
}>();

const emit = defineEmits<{
  close: [];
}>();

type LogEntry = TaskLogEvent & { id: number };

const logs = ref<LogEntry[]>([]);
const loading = ref(false);
const error = ref("");
const ended = ref(false);
const viewer = ref<HTMLElement | null>(null);
const searchInput = ref<HTMLInputElement | null>(null);
const levelSelect = ref<{ dismiss(): boolean } | null>(null);
const timeRangeSelect = ref<{ dismiss(): boolean } | null>(null);
const autoScroll = ref(true);
const parseLogs = ref(true);
const wrapLines = ref(false);
const range = ref(0);
const timeRangeOptions = [
  { value: 0, label: "All time" },
  { value: 15, label: "Last 15 min" },
  { value: 60, label: "Last hour" },
];
const since = ref("");
const firstVisibleLog = ref(0);
const showEarlier = ref(false);
const openLog = ref<number | null>(null);
const detailView = ref("fields");
const searchQuery = ref("");
const copiedLogKey = ref("");
const taskLogLevels: TaskLogLevel[] = ["TRACE", "DEBUG", "INFO", "WARN", "ERROR", "FATAL"];
const taskLogLevelFilters: Array<{ id: string; label: string; levels: TaskLogLevel[] }> = [
  { id: "error", label: "Error", levels: ["ERROR", "FATAL"] },
  { id: "warn", label: "Warn", levels: ["WARN"] },
  { id: "info", label: "Info", levels: ["INFO"] },
  { id: "debug", label: "Debug", levels: ["DEBUG"] },
  { id: "other", label: "Other", levels: ["TRACE"] },
];
const selectedLogFilters = ref<string[]>([]);
const logLevelCounts = ref<Record<TaskLogLevel, number>>(emptyLogLevelCounts());
const selectedLogLevels = computed(() => {
  if (!selectedLogFilters.value.length) return taskLogLevels;
  return taskLogLevels.filter((level) => taskLogLevelFilters.some((filter) =>
    selectedLogFilters.value.includes(filter.id) && filter.levels.includes(level),
  ));
});
const logLevelOptions = computed(() => taskLogLevelFilters.map((filter) => ({
  value: filter.id,
  label: `${filter.label} (${logLevelCount(filter.levels)})`,
})));

const previewLineCount = 10;
const chartBucketCount = 48;
const logDateFormat = new Intl.DateTimeFormat("en-GB", {
  weekday: "short", day: "numeric", month: "short", year: "numeric", timeZone: "UTC",
});

let stream: EventSource | null = null;

function emptyLogLevelCounts(): Record<TaskLogLevel, number> {
  return { TRACE: 0, DEBUG: 0, INFO: 0, WARN: 0, ERROR: 0, FATAL: 0 };
}

const filteredLogs = computed(() => {
  const query = searchQuery.value.trim().toLowerCase();
  return query ? logs.value.filter((entry) => logSearchText(entry).includes(query)) : logs.value;
});

const earlierCount = computed(() => showEarlier.value || searchQuery.value.trim() ? 0 : firstVisibleLog.value);
const visibleLogs = computed(() => filteredLogs.value.slice(earlierCount.value));
const isLive = computed(() => autoScroll.value && !ended.value && !error.value);

const timeline = computed(() => {
  const timestamps = logs.value.map(({ timestamp }) => Date.parse(timestamp)).filter(Number.isFinite);
  if (!timestamps.length) return null;

  const start = since.value ? Date.parse(since.value) : timestamps.reduce((oldest, time) => Math.min(oldest, time));
  const latest = timestamps.reduce((newest, time) => Math.max(newest, time));
  const end = Math.max(start + 1, latest, since.value ? Date.now() : latest);
  const counts = Array<number>(chartBucketCount).fill(0);
  for (const time of timestamps) {
    if (time < start) continue;
    const index = Math.min(chartBucketCount - 1, Math.floor((time - start) / (end - start) * chartBucketCount));
    counts[index]++;
  }
  const peak = Math.max(1, ...counts);
  return {
    bars: counts.map((count) => count ? 7.5 + 92.5 * count / peak : 0),
    labels: Array.from({ length: 5 }, (_, index) =>
      new Date(start + (end - start) * index / 4).toISOString().slice(11, 16)),
  };
});

const selectedLog = computed(() => openLog.value === null ? undefined : logs.value[openLog.value]);
const detailTabs = computed(() => {
  const tabs = [
    { value: "fields", label: "Fields", id: "task-log-fields-tab", panelId: "task-log-detail-content" },
    { value: "json", label: "JSON", id: "task-log-json-tab", panelId: "task-log-detail-content" },
  ];
  if (selectedLog.value?.raw_message && selectedLog.value.raw_message !== selectedLog.value.message) {
    tabs.push({ value: "raw", label: "Raw", id: "task-log-raw-tab", panelId: "task-log-detail-content" });
  }
  return tabs;
});
const detailJson = computed(() => selectedLog.value ? JSON.stringify(logFields(selectedLog.value), null, 2) : "");
const jsonTokens = computed(() => {
  const tokens: Array<{ text: string; kind: string | undefined }> = [];
  tokenizeWith(detailJson.value, json, (text, kind) => tokens.push({ text, kind }));
  return tokens;
});

function closeStream(): void {
  stream?.close();
  stream = null;
}

function close(): void {
  closeStream();
  loading.value = false;
  emit("close");
}

function isViewerAtBottom(): boolean {
  if (!viewer.value) return true;
  return viewer.value.scrollHeight - viewer.value.scrollTop - viewer.value.clientHeight <= 8;
}

function scrollToBottom(): void {
  if (viewer.value) viewer.value.scrollTop = viewer.value.scrollHeight;
}

function handleScroll(): void {
  if (autoScroll.value && !isViewerAtBottom()) autoScroll.value = false;
}

function toggleLive(): void {
  autoScroll.value = !autoScroll.value;
  if (autoScroll.value) void nextTick(scrollToBottom);
}

function appendLog(entry: TaskLogEvent): void {
  logs.value.push({ ...entry, id: logs.value.length });
  if (autoScroll.value && openLog.value === null) {
    firstVisibleLog.value = Math.max(0, logs.value.length - previewLineCount);
  }
  if (autoScroll.value) void nextTick(() => {
    if (autoScroll.value) scrollToBottom();
  });
}

function startStream(): void {
  if (!props.open || !props.taskId) return;

  closeStream();
  logs.value = [];
  firstVisibleLog.value = 0;
  showEarlier.value = false;
  openLog.value = null;
  copiedLogKey.value = "";
  error.value = "";
  ended.value = false;
  loading.value = true;
  autoScroll.value = true;
  logLevelCounts.value = emptyLogLevelCounts();
  void nextTick(scrollToBottom);

  const activeStream = openTaskLogsStream(props.taskId, {
    follow: true,
    tail: 200,
    parse: parseLogs.value,
    levels: parseLogs.value ? selectedLogLevels.value : undefined,
    ...(since.value ? { since: since.value } : {}),
  });
  stream = activeStream;

  activeStream.addEventListener("log", (event) => {
    if (stream !== activeStream) return;
    loading.value = false;
    try {
      const payload = JSON.parse(event.data) as TaskLogEvent;
      appendLog({
        timestamp: String(payload.timestamp || ""),
        stream: String(payload.stream || "stdout"),
        level: payload.level,
        message: String(payload.message || ""),
        attributes: payload.attributes,
        raw_message: String(payload.raw_message || ""),
      });
    } catch {
      appendLog({ timestamp: "", stream: "stdout", message: event.data });
    }
  });

  activeStream.addEventListener("counts", (event) => {
    if (stream !== activeStream) return;
    loading.value = false;
    try {
      const payload = JSON.parse(event.data) as TaskLogCountsEvent;
      const counts = emptyLogLevelCounts();
      for (const level of taskLogLevels) counts[level] = Number(payload.counts?.[level] ?? 0);
      logLevelCounts.value = counts;
    } catch {
      // Ignore malformed count updates without interrupting the stream.
    }
  });

  activeStream.addEventListener("eof", () => {
    if (stream !== activeStream) return;
    loading.value = false;
    ended.value = true;
    closeStream();
  });

  activeStream.onerror = () => {
    if (stream !== activeStream) return;
    loading.value = false;
    if (!ended.value) error.value = "Log stream interrupted";
    closeStream();
  };
}

function resetAndStart(): void {
  parseLogs.value = true;
  searchQuery.value = "";
  wrapLines.value = false;
  range.value = 0;
  since.value = "";
  selectedLogFilters.value = [];
  startStream();
}

function toggleLogView(): void {
  parseLogs.value = !parseLogs.value;
  startStream();
}

function changeTimeRange(): void {
  since.value = range.value ? new Date(Date.now() - range.value * 60_000).toISOString() : "";
  startStream();
}

function focusSearch(event: KeyboardEvent): void {
  if (event.key !== "/" || event.ctrlKey || event.altKey || event.metaKey) return;
  if (event.target instanceof HTMLElement && event.target.closest("input, select, textarea, [contenteditable]")) return;
  event.preventDefault();
  searchInput.value?.focus();
}

function dismissLogSelect(event: KeyboardEvent): void {
  if (levelSelect.value?.dismiss() || timeRangeSelect.value?.dismiss()) event.preventDefault();
}

function logLevelCount(levels: TaskLogLevel[]): number {
  return levels.reduce((count, level) => count + logLevelCounts.value[level], 0);
}

function setLogLevelFilters(filters: string[]): void {
  selectedLogFilters.value = filters;
  startStream();
}

function logFields(entry: TaskLogEvent): Record<string, unknown> {
  return {
    time: entry.timestamp,
    level: entry.level,
    msg: entry.message,
    ...entry.attributes,
  };
}

function logSearchText(entry: TaskLogEvent): string {
  return `${entry.message}\n${entry.raw_message ?? ""}\n${JSON.stringify(logFields(entry))}`.toLowerCase();
}

function formatLogTime(timestamp: string): string {
  return timestamp.match(/T(\d{2}:\d{2}:\d{2}(?:\.\d{1,3})?)/)?.[1] ?? (timestamp || "—");
}

function formatLogDay(timestamp: string): string {
  const time = Date.parse(timestamp);
  return Number.isFinite(time) ? logDateFormat.format(time) : "Unknown date";
}

function formatLogAttributeValue(value: unknown): string {
  if (typeof value === "string") return value;
  return JSON.stringify(value) ?? "—";
}

function logMethod(entry: TaskLogEvent): string {
  const method = entry.attributes?.["grpc.method"];
  return typeof method === "string" ? method.replace(/^\/?[^/]*\./, "") : "";
}

function revealEarlier(): void {
  showEarlier.value = true;
  autoScroll.value = false;
  if (viewer.value) {
    viewer.value.scrollTop = 0;
    viewer.value.focus({ preventScroll: true });
  }
}

function toggleDetails(entry: LogEntry): void {
  openLog.value = openLog.value === entry.id ? null : entry.id;
  detailView.value = "fields";
  if (openLog.value !== null) autoScroll.value = false;
}

async function copyLogValue(value: string, key: string): Promise<void> {
  await copyText(value);
  copiedLogKey.value = key;
}

watch(
  () => [props.open, props.taskId] as const,
  ([open], previous) => {
    if (!open) {
      closeStream();
      return;
    }
    if (!previous || !previous[0] || previous[1] !== props.taskId) resetAndStart();
  },
  { immediate: true },
);

onUnmounted(() => {
  closeStream();
});
</script>

<template>
  <AppModal
    :open="open"
    labelledby="task-logs-title"
    keep-mounted
    card-class="task-logs-modal-card"
    header-class="task-logs-modal-header"
    @close="close"
    @keydown="focusSearch"
    @escape="dismissLogSelect"
  >
    <template #title>
      <div class="task-logs-title">
        <h2 id="task-logs-title">{{ taskId || "n/a" }}</h2>
        <button
          type="button"
          class="live-indicator task-logs-live"
          :class="{ 'live-indicator--paused': !isLive }"
          :aria-pressed="isLive"
          :disabled="ended || !!error"
          @click="toggleLive"
        >
          <span aria-hidden="true" />Live
        </button>
      </div>
    </template>

    <div class="task-logs-toolbar">
      <div class="task-logs-controls">
        <label class="task-logs-search">
          <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="11" cy="11" r="7" /><path d="m20 20-3.5-3.5" /></svg>
          <span class="sr-only">Search logs</span>
          <input
            ref="searchInput"
            v-model="searchQuery"
            type="search"
            placeholder="Search logs"
          />
        </label>
        <AppSelect
          v-if="open && parseLogs"
          ref="levelSelect"
          multiple
          class="task-logs-select task-logs-level-select"
          :model-value="selectedLogFilters"
          :options="logLevelOptions"
          label="Filter by log level"
          heading="Log levels"
          placeholder="All levels"
          @update:model-value="setLogLevelFilters"
        >
          <template #icon><SeverityIcon /></template>
        </AppSelect>
        <AppSelect
          ref="timeRangeSelect"
          v-model="range"
          class="task-logs-select task-logs-range"
          :options="timeRangeOptions"
          label="Time range"
          @update:model-value="changeTimeRange"
        >
          <template #icon>
            <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></svg>
          </template>
        </AppSelect>
        <AppButton
          variant="secondary"
          class="task-logs-mode-button app-tooltip"
          :aria-pressed="parseLogs"
          aria-label="Parsed logs"
          :data-tooltip="parseLogs ? 'Parsed' : 'Raw'"
          @click="toggleLogView"
        >
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <rect v-if="parseLogs" x="3" y="3" width="18" height="18" rx="2" />
            <path :d="parseLogs ? 'M9 3v18M3 9h18' : 'm4 17 6-6-6-6M12 19h8'" />
          </svg>
        </AppButton>
      </div>
    </div>

    <div v-if="timeline" class="task-logs-timeline" role="img" aria-label="Volume of received log lines over time (UTC)">
      <div class="task-logs-bars" aria-hidden="true">
        <span v-for="(height, index) in timeline.bars" :key="index" :style="{ height: `${height}%` }" />
      </div>
      <div class="task-logs-axis" aria-hidden="true">
        <span v-for="(label, index) in timeline.labels" :key="index">{{ label }}</span>
      </div>
    </div>

    <div class="task-logs-table" :class="{ 'task-logs-table--raw': !parseLogs, 'task-logs-table--wrap': wrapLines }">
      <div class="task-log-columns task-log-line">
        <span title="Coordinated Universal Time">Time</span>
        <span v-if="parseLogs">Level</span>
        <span class="task-log-message-column">
          Message
          <button
            type="button"
            class="task-log-wrap-button app-tooltip"
            :aria-pressed="wrapLines"
            aria-label="Wrap lines"
            data-tooltip="Wrap lines"
            @click="wrapLines = !wrapLines"
          >
            <svg viewBox="0 0 24 24" aria-hidden="true">
              <path v-if="wrapLines" d="M3 6h18M3 12h15a3 3 0 1 1 0 6h-4m2-2-2 2 2 2M3 18h7" />
              <path v-else d="M3 6h18M3 10h18M3 14h18M3 18h18" />
            </svg>
          </button>
        </span>
      </div>
      <div ref="viewer" class="task-logs-viewer" role="region" aria-label="Log entries" tabindex="0" @scroll="handleScroll">
        <p v-if="loading && logs.length === 0" class="task-logs-notice" role="status">Loading logs…</p>
        <p v-if="error" class="task-logs-notice task-logs-error" role="alert">{{ error }}</p>
        <p v-else-if="!loading && logs.length === 0" class="task-logs-notice">No log lines.</p>
        <p v-else-if="logs.length && !visibleLogs.length" class="task-logs-notice">No logs match your search.</p>
        <ol v-if="visibleLogs.length" class="task-logs-list">
          <li v-for="(entry, index) in visibleLogs" :key="entry.id">
            <div v-if="index === 0 || entry.timestamp.slice(0, 10) !== visibleLogs[index - 1]?.timestamp.slice(0, 10)" class="task-logs-day">
              <span>{{ formatLogDay(entry.timestamp) }}</span>
              <button v-if="index === 0 && earlierCount" type="button" @click="revealEarlier">
                Show {{ earlierCount }} earlier lines
              </button>
            </div>
            <button
              v-if="parseLogs"
              class="task-log-line task-log-summary"
              type="button"
              :aria-expanded="openLog === entry.id"
              @click="toggleDetails(entry)"
            >
              <time class="task-log-time" :datetime="entry.timestamp" :title="entry.timestamp">{{ formatLogTime(entry.timestamp) }}</time>
              <span class="task-log-level" :class="`task-log-level-${(entry.level || 'INFO').toLowerCase()}`">
                <span class="task-log-dot" aria-hidden="true" />{{ entry.level || "INFO" }}
              </span>
              <span class="task-log-summary-content">
                <span class="task-log-message" :title="entry.message">{{ entry.message }}</span>
                <code v-if="logMethod(entry)" class="task-log-method" :title="String(entry.attributes?.['grpc.method'])">{{ logMethod(entry) }}</code>
              </span>
              <svg class="task-log-chevron" viewBox="0 0 24 24" aria-hidden="true"><path d="m6 9 6 6 6-6" /></svg>
            </button>
            <div v-else class="task-log-line task-log-raw">
              <time class="task-log-time" :datetime="entry.timestamp" :title="entry.timestamp">{{ formatLogTime(entry.timestamp) }}</time>
              <span class="task-log-message" :title="entry.message">{{ entry.message }}</span>
            </div>

            <div v-if="parseLogs && openLog === entry.id" class="task-log-detail-panel">
              <div class="task-log-detail-controls">
                <AppTabs v-model="detailView" :tabs="detailTabs" label="Log details view" density="compact" />
                <AppButton
                  variant="secondary" compact class="task-log-copy"
                  @click="copyLogValue(detailView === 'raw' ? entry.raw_message || entry.message : detailJson, `${detailView}-${entry.id}`)"
                >
                  <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="9" y="9" width="12" height="12" rx="2" /><path d="M5 15V5a2 2 0 0 1 2-2h10" /></svg>
                  {{ copiedLogKey === `${detailView}-${entry.id}` ? "Copied" : detailView === "raw" ? "Copy raw" : "Copy JSON" }}
                </AppButton>
              </div>

              <div
                id="task-log-detail-content"
                class="task-log-detail-content"
                role="tabpanel"
                tabindex="0"
                :aria-labelledby="`task-log-${detailView}-tab`"
              >
                <dl v-if="detailView === 'fields'" class="task-log-attributes">
                  <div v-for="(value, key) in logFields(entry)" :key="key" class="task-log-attribute">
                    <dt>{{ key }}</dt>
                    <dd>{{ formatLogAttributeValue(value) }}</dd>
                    <dd class="task-log-attribute-actions">
                      <AppCopyButton
                        :value="formatLogAttributeValue(value)"
                        color="var(--muted)"
                        :label="`Copy ${key}`"
                        :copied-label="`${key} copied`"
                      />
                    </dd>
                  </div>
                </dl>
                <pre v-else-if="detailView === 'json'" class="task-log-json"><code><span
                  v-for="(token, index) in jsonTokens" :key="index" :class="token.kind && `shj-syn-${token.kind}`"
                >{{ token.text }}</span></code></pre>
                <pre v-else class="task-log-json"><code>{{ entry.raw_message || entry.message }}</code></pre>
              </div>
            </div>
          </li>
        </ol>
      </div>
    </div>
  </AppModal>
</template>
