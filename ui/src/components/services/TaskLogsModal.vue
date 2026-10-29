<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";

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

const logs = ref<TaskLogEvent[]>([]);
const loading = ref(false);
const error = ref("");
const ended = ref(false);
const viewer = ref<HTMLElement | null>(null);
const autoScroll = ref(true);
const parseLogs = ref(false);
const searchQuery = ref("");
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

let stream: EventSource | null = null;

function emptyLogLevelCounts(): Record<TaskLogLevel, number> {
  return { TRACE: 0, DEBUG: 0, INFO: 0, WARN: 0, ERROR: 0, FATAL: 0 };
}

const filteredLogs = computed(() => {
  const query = searchQuery.value.trim().toLocaleLowerCase();
  if (!query) return logs.value;
  return logs.value.filter((entry) => logSearchText(entry).includes(query));
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
  autoScroll.value = isViewerAtBottom();
}

function appendLog(entry: TaskLogEvent): void {
  const shouldScroll = autoScroll.value && isViewerAtBottom();
  logs.value.push(entry);
  if (shouldScroll) void nextTick(scrollToBottom);
}

function startStream(): void {
  if (!props.open || !props.taskId) return;

  closeStream();
  logs.value = [];
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
  parseLogs.value = false;
  searchQuery.value = "";
  copiedLogKey.value = "";
  selectedLogLevels.value = [...taskLogLevels];
  startStream();
}

function toggleLogParsing(): void {
  parseLogs.value = !parseLogs.value;
  if (parseLogs.value && selectedLogLevels.value.length === 0) {
    selectedLogLevels.value = [...taskLogLevels];
  }
  startStream();
}

function areAllLogLevelsSelected(): boolean {
  return taskLogLevels.every((level) => selectedLogLevels.value.includes(level));
}

function isLogLevelFilterSelected(filter: { id: string; levels: TaskLogLevel[] }): boolean {
  if (filter.id === "all") return areAllLogLevelsSelected();
  return !areAllLogLevelsSelected() && filter.levels.every((level) => selectedLogLevels.value.includes(level));
}

function logLevelFilterCount(filter: { levels: TaskLogLevel[] }): number {
  return filter.levels.reduce((count, level) => count + logLevelCounts.value[level], 0);
}

function toggleLogLevelFilter(filter: { id: string; levels: TaskLogLevel[] }): void {
  if (filter.id === "all") {
    selectedLogLevels.value = [...taskLogLevels];
    startStream();
    return;
  }

  const selected = filter.levels.every((level) => selectedLogLevels.value.includes(level));
  const nextLevels = selected
    ? selectedLogLevels.value.filter((level) => !filter.levels.includes(level))
    : taskLogLevels.filter((level) => filter.levels.includes(level) || selectedLogLevels.value.includes(level));
  if (nextLevels.length === 0) return;
  selectedLogLevels.value = nextLevels;
  startStream();
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
  if (!timestamp) return "no timestamp";
  return timestamp.replace("T", " ").replace(/(\.\d{3})\d+/, "$1").replace(/Z$/, "");
}

function logAttributeEntries(attributes: Record<string, unknown> | undefined): Array<[string, unknown]> {
  return Object.entries(attributes ?? {});
}

function formatLogAttributeValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (value === undefined) return "undefined";
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
  if (event.key === "Escape" && props.open) close();
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

onMounted(() => document.addEventListener("keydown", handleEscape));
onUnmounted(() => {
  closeStream();
  document.removeEventListener("keydown", handleEscape);
});
</script>

<template>
  <div class="modal" :class="{ hidden: !open }" :aria-hidden="!open">
    <div class="modal-overlay" @click="close" />
    <div class="modal-card task-logs-modal-card" role="dialog" aria-modal="true" aria-labelledby="task-logs-title">
      <div class="modal-header task-logs-modal-header">
        <div class="task-logs-title">
          <h2 id="task-logs-title">Task logs</h2>
          <p>task: <code>{{ taskId || "n/a" }}</code></p>
        </div>
        <button class="modal-close" type="button" aria-label="Close modal" @click="close">
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
            <input v-model="searchQuery" type="search" placeholder="Search in messages..." />
          </label>
        </div>
        <p v-if="loading && logs.length === 0" class="meta">Loading logs...</p>
        <p v-if="error" class="meta task-logs-error">{{ error }}</p>
        <div ref="viewer" class="task-logs-viewer" aria-live="polite" @scroll="handleScroll">
          <p v-if="logs.length === 0 && !loading" class="meta">No log lines.</p>
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
                  <span class="task-log-level" :class="`task-log-level-${String(entry.level || 'INFO').toLowerCase()}`">{{ entry.level || "INFO" }}</span>
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
                      <button type="button" class="task-log-copy" :aria-label="`Copy raw log ${index + 1}`" @click="copyRawLog(entry, `${entry.timestamp}-${index}`)">
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
</template>
