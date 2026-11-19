<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRouter } from "vue-router";

import { getAssistantUsage } from "../api/assistant";
import { ApiError } from "../api/client";
import type { AssistantUsageInterval, AssistantUsageQuery, AssistantUsageResponse } from "../api/types";
import AppButton from "../components/common/AppButton.vue";
import AppStackedBarChart from "../components/common/AppStackedBarChart.vue";
import AppTabs from "../components/common/AppTabs.vue";

const router = useRouter();
const previous = router.options.history.state.assistantChatsPath;
const chatsRoute = typeof previous === "string" && previous.startsWith("/") && !previous.startsWith("//")
  ? previous
  : { name: "overview", query: { assistant: "chats" } };

const periods = [
  { value: "daily", label: "Daily" },
  { value: "weekly", label: "Weekly" },
  { value: "monthly", label: "Monthly" },
  { value: "custom", label: "Custom" },
].map((tab) => ({ ...tab, id: `usage-${tab.value}`, panelId: "usage-period" }));

const fullNumber = new Intl.NumberFormat();
const compactNumber = new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 });
const formatNumber = (value: number) => fullNumber.format(value);
const formatAxisNumber = (value: number) => compactNumber.format(value);
const dayMs = 86_400_000;

function dateString(date: Date): string {
  return date.toISOString().slice(0, 10);
}

const today = dateString(new Date());
const todayMs = Date.parse(today);
const tomorrow = dateString(new Date(todayMs + dayMs));
const now = new Date(todayMs);
const mondayOffset = (now.getUTCDay() + 6) % 7;
const weeklyStart = dateString(new Date(todayMs - (49 + mondayOffset) * dayMs));
const rangeStart = ref(weeklyStart);
const rangeEnd = ref(today);
const rangeError = ref("");
const period = ref("weekly");
const customQuery = ref<AssistantUsageQuery>({ from: weeklyStart, to: tomorrow, interval: "weekly" });
const retryCount = ref(0);
const loading = ref(false);
const error = ref("");
const unavailable = ref(false);
const response = ref<AssistantUsageResponse>();

const query = computed<AssistantUsageQuery>(() => {
  if (period.value === "custom") return customQuery.value;
  if (period.value === "daily") {
    return { from: dateString(new Date(todayMs - 29 * dayMs)), to: tomorrow, interval: "daily" };
  }
  if (period.value === "monthly") {
    const start = new Date(now);
    start.setUTCDate(1);
    start.setUTCMonth(start.getUTCMonth() - 11);
    return { from: dateString(start), to: tomorrow, interval: "monthly" };
  }
  return { from: weeklyStart, to: tomorrow, interval: "weekly" };
});

function applyRange() {
  const from = Date.parse(rangeStart.value);
  const end = Date.parse(rangeEnd.value);
  if (!Number.isFinite(from) || !Number.isFinite(end) || from > end || end > todayMs) {
    rangeError.value = "Choose a start date and an end date in order, up to today.";
    return;
  }

  rangeError.value = "";
  const days = (end - from) / dayMs + 1;
  const interval: AssistantUsageInterval = days <= 30 ? "daily" : days <= 84 ? "weekly" : "monthly";
  const to = dateString(new Date(end + dayMs));
  if (customQuery.value.from !== rangeStart.value || customQuery.value.to !== to) {
    customQuery.value = { from: rangeStart.value, to, interval };
  }
}

watch([query, retryCount], async ([selected], _, onCleanup) => {
  const controller = new AbortController();
  onCleanup(() => controller.abort());
  loading.value = true;
  error.value = "";
  unavailable.value = false;
  response.value = undefined;
  try {
    const result = await getAssistantUsage(selected, controller.signal);
    if (!Array.isArray(result?.buckets)) throw new Error("Invalid usage response");
    if (!controller.signal.aborted) response.value = result;
  } catch (reason) {
    if (controller.signal.aborted) return;
    unavailable.value = reason instanceof ApiError && (reason.status === 404 || reason.status === 501);
    error.value = reason instanceof Error ? reason.message : "Request failed";
  } finally {
    if (!controller.signal.aborted) loading.value = false;
  }
}, { immediate: true });

const buckets = computed(() => response.value?.buckets ?? []);
const series = computed(() => [
  { label: "Input tokens", color: "var(--info)", values: buckets.value.map((bucket) => bucket.input_tokens) },
  { label: "Output tokens", color: "var(--success)", values: buckets.value.map((bucket) => bucket.output_tokens) },
]);

const summary = computed(() => [
  { label: "Total tokens", value: buckets.value.reduce((total, bucket) => total + bucket.total_tokens, 0) },
  { label: "Input tokens", value: series.value[0].values.reduce((total, value) => total + value, 0) },
  { label: "Output tokens", value: series.value[1].values.reduce((total, value) => total + value, 0) },
]);
const hasUsage = computed(() => summary.value.some(({ value }) => value > 0));

function bucketLabels(long: boolean) {
  const monthly = query.value.interval === "monthly";
  const firstYear = buckets.value[0]?.start.slice(0, 4);
  const lastYear = buckets.value.at(-1)?.start.slice(0, 4);
  const formatter = new Intl.DateTimeFormat("en", {
    month: "short",
    ...(!monthly ? { day: "numeric" } : {}),
    ...(long || (monthly && firstYear !== lastYear) ? { year: "numeric" } : {}),
    timeZone: "UTC",
  });
  return buckets.value.map(({ start }) => {
    const label = formatter.format(new Date(start));
    return long && query.value.interval === "weekly" ? `Week of ${label}` : label;
  });
}
</script>

<template>
  <section class="assistant-usage" aria-labelledby="assistant-usage-title">
    <header class="assistant-usage-heading">
      <RouterLink :to="chatsRoute" class="assistant-icon-button" aria-label="Back to chats" title="Back to chats">
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M19 12H5m7 7-7-7 7-7" />
        </svg>
      </RouterLink>
      <h1 id="assistant-usage-title">Usage</h1>
    </header>

    <AppTabs v-model="period" :tabs="periods" label="Usage period" density="compact" />
    <div id="usage-period" class="assistant-usage-period" role="tabpanel" :aria-labelledby="`usage-${period}`">
      <form v-if="period === 'custom'" class="assistant-usage-dates" @submit.prevent="applyRange">
        <label>
          From
          <input v-model="rangeStart" type="date" :max="today" required @input="rangeError = ''" />
        </label>
        <label>
          To
          <input v-model="rangeEnd" type="date" :max="today" required @input="rangeError = ''" />
        </label>
        <AppButton type="submit" variant="secondary" compact>Apply</AppButton>
        <p v-if="rangeError" class="assistant-usage-range-error" role="alert">{{ rangeError }}</p>
      </form>

      <AppStackedBarChart
        legend-label="Token types"
        :chart-label="`Assistant token usage by ${query.interval === 'daily' ? 'day' : query.interval === 'weekly' ? 'week' : 'month'}`"
        :labels="bucketLabels(false)"
        :tooltip-labels="bucketLabels(true)"
        :totals="buckets.map((bucket) => bucket.total_tokens)"
        :series="series"
        :summary="summary"
        :show-plot="hasUsage"
        :format-value="formatNumber"
        :format-axis-value="formatAxisNumber"
      >
        <template #status>
          <p v-if="loading" class="assistant-usage-status" role="status">Loading token usage…</p>
          <p v-else-if="unavailable" class="assistant-usage-status">Token usage history is unavailable on this server.</p>
          <div v-else-if="error" class="assistant-usage-status assistant-usage-error" role="alert">
            Failed to load token usage ({{ error }}).
            <AppButton variant="secondary" compact @click="retryCount++">Retry</AppButton>
          </div>
          <p v-else-if="!hasUsage" class="assistant-usage-status">No token usage in the selected period.</p>
        </template>
      </AppStackedBarChart>
    </div>
  </section>
</template>

<style scoped>
.assistant-usage {
  display: grid;
  gap: 12px;
  width: 100%;
  max-width: 960px;
  min-width: 0;
  --stacked-chart-height: 230px;
  --stacked-chart-height-mobile: 190px;
  --stacked-chart-summary-padding-mobile: 8px;
}

.assistant-usage-heading { display: flex; align-items: center; gap: 8px; }
.assistant-usage-heading h1 { margin: 0; font-size: 1.25rem; }
.assistant-usage-period { display: grid; gap: 14px; min-width: 0; }
.assistant-usage-dates { display: flex; flex-wrap: wrap; gap: 8px; align-items: end; }
.assistant-usage-dates label { display: grid; gap: 4px; min-width: 0; color: var(--muted); font-size: .75rem; }
.assistant-usage-dates input {
  box-sizing: border-box;
  max-width: 100%;
  min-width: 0;
  min-height: 34px;
  padding: 4px 8px;
  border: 1px solid var(--border-default);
  border-radius: 6px;
  background: var(--surface-control);
  color: var(--text);
  font: inherit;
  font-size: .82rem;
}
.assistant-usage-status {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 118px;
  margin: 0;
  color: var(--muted);
  font-size: .82rem;
}
.assistant-usage-error, .assistant-usage-range-error { color: var(--error); }
.assistant-usage-range-error { flex-basis: 100%; margin: 0; font-size: .82rem; }

@media (max-width: 560px) {
  .assistant-usage-dates label { flex: 1 1 6.5rem; }
  .assistant-usage-dates input { width: 100%; }
}
</style>
