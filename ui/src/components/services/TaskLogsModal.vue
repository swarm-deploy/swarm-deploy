<script setup lang="ts">
import { enumLabel, formatNumber, locale, plural, t } from "../../i18n";
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
import { useConfigStore } from "../../stores/config";
import { copyText } from "../../utils/clipboard";

const props = defineProps<{
  open: boolean;
  taskId: string;
}>();

const emit = defineEmits<{
  close: [];
}>();

type LogEntry = TaskLogEvent & { id: number };

const configStore = useConfigStore();
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
  { value: 0, label: t("logs.allTime") },
  { value: 15, label: t("logs.lastMinutes", { count: formatNumber(15) }) },
  { value: 60, label: t("logs.lastHour") },
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
  { id: "error", label: t("common.error"), levels: ["ERROR", "FATAL"] },
  { id: "warn", label: t("logs.warnings"), levels: ["WARN"] },
  { id: "info", label: t("logs.info"), levels: ["INFO"] },
  { id: "debug", label: t("logs.debug"), levels: ["DEBUG"] },
  { id: "other", label: t("logs.other"), levels: ["TRACE"] },
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
  label: t("logs.filterOption", { label: filter.label, count: formatNumber(logLevelCount(filter.levels)) }),
})));

const previewLineCount = 10;
const chartBucketCount = 48;
const logDateFormat = new Intl.DateTimeFormat(locale(), {
  weekday: "short", day: "numeric", month: "short", year: "numeric", timeZone: "UTC",
});

const logTimeFormats = ([undefined, 1, 2, 3] as const).map((fractionalSecondDigits) =>
  new Intl.DateTimeFormat(locale(), {
    hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23", timeZone: "UTC",
    fractionalSecondDigits,
  }),
);
const logAxisFormat = new Intl.DateTimeFormat(locale(), {
  hour: "2-digit", minute: "2-digit", hourCycle: "h23", timeZone: "UTC",
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
      logAxisFormat.format(start + (end - start) * index / 4)),
  };
});

const selectedLog = computed(() => openLog.value === null ? undefined : logs.value[openLog.value]);
const detailFields = computed(() => selectedLog.value
  ? Object.entries(logFields(selectedLog.value)).map(([key, value]) => ({
      key,
      text: formatLogAttributeValue(value),
      href: logFieldHref(key, value),
    }))
  : []);
const detailTabs = computed(() => {
  const tabs = [
    { value: "fields", label: t("logs.fields"), id: "task-log-fields-tab", panelId: "task-log-detail-content" },
    { value: "json", label: "JSON", id: "task-log-json-tab", panelId: "task-log-detail-content" },
  ];
  if (selectedLog.value?.raw_message && selectedLog.value.raw_message !== selectedLog.value.message) {
    tabs.push({ value: "raw", label: t("common.raw"), id: "task-log-raw-tab", panelId: "task-log-detail-content" });
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
        raw_message: payload.raw_message,
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
    if (!ended.value) error.value = t("logs.streamInterrupted");
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
  const time = Date.parse(timestamp);
  if (!Number.isFinite(time)) return timestamp || "—";

  const precision = timestamp.match(/T\d{2}:\d{2}:\d{2}\.(\d{1,3})/)?.[1].length ?? 0;
  return logTimeFormats[precision].format(time);
}

function rawLog(entry: TaskLogEvent): string {
  return entry.raw_message ?? entry.message;
}

function formatLogDay(timestamp: string): string {
  const time = Date.parse(timestamp);
  return Number.isFinite(time) ? logDateFormat.format(time) : t("logs.unknownDate");
}

function formatLogAttributeValue(value: unknown): string {
  if (typeof value === "string") return value;
  return JSON.stringify(value) ?? "—";
}

function logFieldHref(key: string, value: unknown): string | undefined {
  const templates = configStore.logLinkFields;
  if (!Object.hasOwn(templates, key) || value == null || value === "") return;

  try {
    const url = new URL(templates[key].replaceAll("{value}", encodeURIComponent(formatLogAttributeValue(value))));
    if (url.protocol === "http:" || url.protocol === "https:") return url.href;
  } catch {
    // Keep the value as text if the template cannot produce a URL.
  }
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
        <h2 id="task-logs-title">{{ taskId || t('common.notApplicable') }}</h2>
        <button
          type="button"
          class="live-indicator task-logs-live"
          :class="{ 'live-indicator--paused': !isLive }"
          :aria-pressed="isLive"
          :disabled="ended || !!error"
          @click="toggleLive"
        >
          <span aria-hidden="true" />{{ t('logs.live') }}
        </button>
      </div>
    </template>

    <div class="task-logs-toolbar">
      <div class="task-logs-controls">
        <label class="task-logs-search">
          <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="11" cy="11" r="7" /><path d="m20 20-3.5-3.5" /></svg>
          <span class="sr-only">{{ t('logs.searchLogs') }}</span>
          <input
            ref="searchInput"
            v-model="searchQuery"
            type="search"
            :placeholder="t('logs.searchLogs')"
          />
        </label>
        <AppSelect
          v-if="open && parseLogs"
          ref="levelSelect"
          multiple
          class="task-logs-select task-logs-level-select"
          :model-value="selectedLogFilters"
          :options="logLevelOptions"
          :label="t('logs.filterLevel')"
          :heading="t('logs.levels')"
          :placeholder="t('logs.allLevels')"
          @update:model-value="setLogLevelFilters"
        >
          <template #icon><SeverityIcon /></template>
        </AppSelect>
        <AppSelect
          ref="timeRangeSelect"
          v-model="range"
          class="task-logs-select task-logs-range"
          :options="timeRangeOptions"
          :label="t('logs.timeRange')"
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
          :aria-label="t('logs.parsedLogs')"
          :data-tooltip="parseLogs ? t('logs.parsed') : t('common.raw')"
          @click="toggleLogView"
        >
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <rect v-if="parseLogs" x="3" y="3" width="18" height="18" rx="2" />
            <path :d="parseLogs ? 'M9 3v18M3 9h18' : 'm4 17 6-6-6-6M12 19h8'" />
          </svg>
        </AppButton>
      </div>
    </div>

    <div v-if="timeline" class="task-logs-timeline" role="img" :aria-label="t('logs.volumeChart')">
      <div class="task-logs-bars" aria-hidden="true">
        <span v-for="(height, index) in timeline.bars" :key="index" :style="{ height: `${height}%` }" />
      </div>
      <div class="task-logs-axis" aria-hidden="true">
        <span v-for="(label, index) in timeline.labels" :key="index">{{ label }}</span>
      </div>
    </div>

    <div class="task-logs-table" :class="{ 'task-logs-table--raw': !parseLogs, 'task-logs-table--wrap': wrapLines }">
      <div class="task-log-columns task-log-line">
        <span :title="t('logs.utc')">{{ t('common.time') }}</span>
        <span v-if="parseLogs">{{ t('logs.level') }}</span>
        <span class="task-log-message-column">
          {{ t('common.message') }}
          <button
            type="button"
            class="task-log-wrap-button app-tooltip"
            :aria-pressed="wrapLines"
            :aria-label="t('logs.wrap')"
            :data-tooltip="t('logs.wrap')"
            @click="wrapLines = !wrapLines"
          >
            <svg viewBox="0 0 24 24" aria-hidden="true">
              <path v-if="wrapLines" d="M3 6h18M3 12h15a3 3 0 1 1 0 6h-4m2-2-2 2 2 2M3 18h7" />
              <path v-else d="M3 6h18M3 10h18M3 14h18M3 18h18" />
            </svg>
          </button>
        </span>
      </div>
      <div ref="viewer" class="task-logs-viewer" role="region" :aria-label="t('logs.entries')" tabindex="0" @scroll="handleScroll">
        <p v-if="loading && logs.length === 0" class="task-logs-notice" role="status">{{ t('logs.loading') }}</p>
        <p v-if="error" class="task-logs-notice task-logs-error" role="alert">{{ error }}</p>
        <p v-else-if="!loading && logs.length === 0" class="task-logs-notice">{{ t('logs.noLines') }}</p>
        <p v-else-if="logs.length && !visibleLogs.length" class="task-logs-notice">{{ t('logs.noMatches') }}</p>
        <ol v-if="visibleLogs.length" class="task-logs-list">
          <li v-for="(entry, index) in visibleLogs" :key="entry.id">
            <div v-if="index === 0 || entry.timestamp.slice(0, 10) !== visibleLogs[index - 1]?.timestamp.slice(0, 10)" class="task-logs-day">
              <span>{{ formatLogDay(entry.timestamp) }}</span>
              <button v-if="index === 0 && earlierCount" type="button" @click="revealEarlier">
                {{ plural('logs.earlierLines', earlierCount) }}
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
                <span class="task-log-dot" aria-hidden="true" />
                <span class="task-log-level-label">{{ enumLabel('logLevels', entry.level || 'INFO') }}</span>
              </span>
              <span class="task-log-summary-content">
                <span class="task-log-message" :title="entry.message">{{ entry.message }}</span>
                <code v-if="logMethod(entry)" class="task-log-method" :title="String(entry.attributes?.['grpc.method'])">{{ logMethod(entry) }}</code>
              </span>
              <svg class="task-log-chevron" viewBox="0 0 24 24" aria-hidden="true"><path d="m6 9 6 6 6-6" /></svg>
            </button>
            <div v-else class="task-log-line task-log-raw">
              <time class="task-log-time" :datetime="entry.timestamp" :title="entry.timestamp">{{ formatLogTime(entry.timestamp) }}</time>
              <span class="task-log-message" :title="rawLog(entry)">{{ rawLog(entry) }}</span>
            </div>

            <div v-if="parseLogs && openLog === entry.id" class="task-log-detail-panel">
              <div class="task-log-detail-controls">
                <AppTabs v-model="detailView" :tabs="detailTabs" :label="t('logs.detailView')" density="compact" />
                <AppButton
                  variant="secondary" compact class="task-log-copy"
                  @click="copyLogValue(detailView === 'raw' ? rawLog(entry) : detailJson, `${detailView}-${entry.id}`)"
                >
                  <svg viewBox="0 0 24 24" aria-hidden="true"><rect x="9" y="9" width="12" height="12" rx="2" /><path d="M5 15V5a2 2 0 0 1 2-2h10" /></svg>
                  {{ copiedLogKey === `${detailView}-${entry.id}` ? t('common.copied') : detailView === "raw" ? t('logs.copyRaw') : t('logs.copyJson') }}
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
                  <div v-for="{ key, text, href } in detailFields" :key="key" class="task-log-attribute">
                    <dt>{{ key }}</dt>
                    <dd>
                      <a v-if="href" class="task-log-attribute-link" :href="href" target="_blank" rel="noopener noreferrer">{{ text }}</a>
                      <template v-else>{{ text }}</template>
                    </dd>
                    <dd class="task-log-attribute-actions">
                      <AppCopyButton
                        :value="text"
                        color="var(--muted)"
                        :label="t('logs.copyField', { name: key })"
                        :copied-label="t('logs.fieldCopied', { name: key })"
                      />
                    </dd>
                  </div>
                </dl>
                <pre v-else-if="detailView === 'json'" class="task-log-json"><code><span
                  v-for="(token, index) in jsonTokens" :key="index" :class="token.kind && `shj-syn-${token.kind}`"
                >{{ token.text }}</span></code></pre>
                <pre v-else class="task-log-json"><code>{{ rawLog(entry) }}</code></pre>
              </div>
            </div>
          </li>
        </ol>
      </div>
    </div>
  </AppModal>
</template>
