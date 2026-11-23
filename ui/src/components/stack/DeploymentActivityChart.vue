<script setup lang="ts">
import { formatNumber, locale, plural, t } from "../../i18n";
import { computed, onBeforeUnmount, ref, watch } from "vue";

import { fetchStackDeploymentActivity } from "../../api/overview";
import type {
  DeploymentActivityBucket,
  DeploymentActivityInterval,
  DeploymentActivityResponse,
} from "../../api/types";
import AppButton from "../common/AppButton.vue";
import AppStackedBarChart from "../common/AppStackedBarChart.vue";
import DeploymentIcon from "../overview/DeploymentIcon.vue";

const props = defineProps<{ stack: string }>();

const statuses = [
  { key: "succeeded", label: t("common.succeeded"), color: "var(--success)" },
  { key: "failed", label: t("common.failed"), color: "var(--error)" },
  { key: "running", label: t("common.running"), color: "var(--warning)" },
  { key: "interrupted", label: t("common.interrupted"), color: "var(--muted)" },
  { key: "superseded", label: t("common.superseded"), color: "var(--info)" },
] as const;

const interval = ref<DeploymentActivityInterval>("weekly");
const responses = ref<Partial<Record<DeploymentActivityInterval, DeploymentActivityResponse>>>({});
const loading = ref<Partial<Record<DeploymentActivityInterval, boolean>>>({});
const errors = ref<Partial<Record<DeploymentActivityInterval, string>>>({});
let request = 0;

const response = computed(() => responses.value[interval.value]);
const buckets = computed(() => response.value?.buckets ?? []);
const isLoading = computed(() => loading.value[interval.value] === true);
const error = computed(() => errors.value[interval.value] ?? "");
const totals = computed(() => {
  const result = { succeeded: 0, failed: 0, running: 0, interrupted: 0, superseded: 0 };
  for (const bucket of buckets.value) {
    for (const status of statuses) result[status.key] += bucket[status.key];
  }
  return result;
});
const total = computed(() => Object.values(totals.value).reduce((sum, value) => sum + value, 0));
const compactSummary = computed(() => {
  if (isLoading.value && !response.value) return t("deployments.loadingActivity");
  if (error.value && !response.value) return t("deployments.activityUnavailable");
  return interval.value === "daily"
    ? plural("deployments.activityDaily", total.value, { days: formatNumber(30) })
    : plural("deployments.activityWeekly", total.value, { weeks: formatNumber(8) });
});

function formatBucket(start: string, long = false): string {
  const date = new Date(start);
  const formatted = new Intl.DateTimeFormat(locale(), {
    month: "short",
    day: "numeric",
    ...(long ? { year: "numeric" } : {}),
    timeZone: "UTC",
  }).format(date);
  return interval.value === "weekly" && long ? t("deployments.weekOf", { date: formatted }) : formatted;
}

function bucketTotal(bucket: DeploymentActivityBucket): number {
  return statuses.reduce((sum, status) => sum + bucket[status.key], 0);
}

const chartSeries = computed(() => statuses.map((status) => ({
  label: status.label,
  color: status.color,
  values: buckets.value.map((bucket) => bucket[status.key]),
})));
const summary = computed(() => [
  { label: t("deployments.total"), value: total.value },
  { label: t("common.succeeded"), value: totals.value.succeeded },
  { label: t("common.failed"), value: totals.value.failed },
]);

async function load(selected: DeploymentActivityInterval, force = false) {
  if (!props.stack || loading.value[selected] || (!force && responses.value[selected])) return;
  const current = request;
  loading.value = { ...loading.value, [selected]: true };
  errors.value = { ...errors.value, [selected]: "" };
  try {
    const result = await fetchStackDeploymentActivity(props.stack, selected);
    if (current !== request) return;
    responses.value = { ...responses.value, [selected]: result };
  } catch (reason) {
    if (current !== request) return;
    errors.value = {
      ...errors.value,
      [selected]: reason instanceof Error ? reason.message : t("common.requestFailed"),
    };
  } finally {
    if (current === request) loading.value = { ...loading.value, [selected]: false };
  }
}

function selectInterval(selected: DeploymentActivityInterval) {
  interval.value = selected;
  void load(selected);
}

function retry() {
  void load(interval.value, true);
}

watch(() => props.stack, () => {
  request++;
  responses.value = {};
  loading.value = {};
  errors.value = {};
  interval.value = "weekly";
  void load("weekly");
}, { immediate: true });

onBeforeUnmount(() => {
  request++;
});
</script>

<template>
  <section class="deployment-activity" aria-labelledby="deployment-activity-title">
    <header class="overview-summary-panel-header">
      <div class="deployment-activity-heading">
        <span class="overview-summary-panel-icon" aria-hidden="true">
          <DeploymentIcon name="calendar" />
        </span>
        <h3 id="deployment-activity-title" class="overview-panel-title">{{ t('deployments.activity') }}</h3>
      </div>
    </header>

    <AppStackedBarChart
      class="deployment-activity-content"
      :legend-label="t('deployments.statuses')"
      :chart-label="compactSummary"
      :labels="buckets.map((bucket) => formatBucket(bucket.start))"
      :tooltip-labels="buckets.map((bucket) => formatBucket(bucket.start, true))"
      :totals="buckets.map(bucketTotal)"
      :series="chartSeries"
      :summary="summary"
      :show-plot="Boolean(response && total > 0)"
    >
      <template #controls>
        <div class="deployment-activity-interval" :aria-label="t('deployments.activityInterval')">
          <button type="button" :class="{ active: interval === 'daily' }" :aria-pressed="interval === 'daily'" @click="selectInterval('daily')">{{ t('deployments.daily') }}</button>
          <button type="button" :class="{ active: interval === 'weekly' }" :aria-pressed="interval === 'weekly'" @click="selectInterval('weekly')">{{ t('deployments.weekly') }}</button>
        </div>
      </template>

      <template #status>
        <p v-if="isLoading && !response" class="deployment-activity-state" role="status">{{ t('deployments.loadingActivityData') }}</p>
        <p v-else-if="error && !response" class="deployment-activity-state deployment-activity-error" role="alert">
          {{ t('deployments.activityError', { error }) }}
          <AppButton variant="secondary" compact @click="retry">{{ t('common.retry') }}</AppButton>
        </p>
        <p v-else-if="response && total === 0" class="deployment-activity-state">{{ t('deployments.emptyPeriod') }}</p>
      </template>
    </AppStackedBarChart>
  </section>
</template>

<style scoped>
/* Hallmark · full redesign · genre: modern-minimal · shape: app component · role: secondary activity · built: the project · v2.0.0 */
/* Hallmark · pre-emit critique: P4 H4 E4 S5 R5 V4 · weakest: E, dense chart labels are enlarged and redundant totals removed */
.deployment-activity { min-width: 0; border: 1px solid var(--border-subtle); border-radius: 10px; background: var(--surface-card-soft); overflow: hidden; }
.deployment-activity-heading { display: flex; align-items: center; min-width: 0; gap: 9px; padding: 17px 16px 13px; background: var(--surface-panel-header); }
.deployment-activity-content { padding: 12px 16px 16px; border-top: 1px solid var(--border-subtle); }
.deployment-activity-interval { display: inline-flex; gap: 4px; border-bottom: 1px solid var(--border-default); }
.deployment-activity-interval button { position: relative; min-height: 32px; padding: 4px 10px; border: 0; border-radius: 4px 4px 0 0; background: transparent; color: var(--muted); box-shadow: none; font-size: .78rem; font-weight: 500; }
.deployment-activity-interval button::after { content: ""; position: absolute; right: 8px; bottom: -1px; left: 8px; height: 2px; border-radius: 2px 2px 0 0; background: transparent; }
.deployment-activity-interval button:hover { transform: none; filter: none; color: var(--text); background: var(--surface-hover); }
.deployment-activity-interval button.active { color: var(--text); font-weight: 650; }
.deployment-activity-interval button.active::after { background: var(--text); }
.deployment-activity-interval button:active { transform: translateY(1px); }
.deployment-activity-interval button:disabled { color: var(--muted); background: var(--surface-muted); opacity: .65; cursor: not-allowed; }
.deployment-activity-interval button:focus-visible { outline: 2px solid var(--accent-focus); outline-offset: 2px; }
.deployment-activity-heading .overview-panel-title { white-space: nowrap; }
.deployment-activity-state { min-height: 118px; display: flex; align-items: center; justify-content: center; gap: 8px; margin: 0; color: var(--muted); font-size: .82rem; font-variant-numeric: tabular-nums; }
.deployment-activity-error { color: var(--error); }
@media (max-width: 560px) {
  .deployment-activity-heading { padding: 15px 13px 13px; }
}
@media (prefers-reduced-motion: reduce) {
  .deployment-activity button { transition-duration: 0s; }
}
</style>
