<script setup lang="ts">
import { BarChart } from "echarts/charts";
import { GridComponent, TooltipComponent } from "echarts/components";
import { init, use, type ECharts, type EChartsCoreOption } from "echarts/core";
import { SVGRenderer } from "echarts/renderers";
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";

import { fetchStackDeploymentActivity } from "../../api/overview";
import type {
  DeploymentActivityBucket,
  DeploymentActivityInterval,
  DeploymentActivityResponse,
} from "../../api/types";
import DeploymentIcon from "../overview/DeploymentIcon.vue";

use([BarChart, GridComponent, TooltipComponent, SVGRenderer]);

const props = defineProps<{ stack: string }>();

const statuses = [
  { key: "succeeded", label: "Succeeded", color: "var(--success)" },
  { key: "failed", label: "Failed", color: "var(--error)" },
  { key: "running", label: "Running", color: "var(--warning)" },
  { key: "interrupted", label: "Interrupted", color: "var(--muted)" },
  { key: "superseded", label: "Superseded", color: "var(--info)" },
] as const;

const interval = ref<DeploymentActivityInterval>("weekly");
const responses = ref<Partial<Record<DeploymentActivityInterval, DeploymentActivityResponse>>>({});
const loading = ref<Partial<Record<DeploymentActivityInterval, boolean>>>({});
const errors = ref<Partial<Record<DeploymentActivityInterval, string>>>({});
const chartElement = ref<HTMLElement | null>(null);
let chart: ECharts | null = null;
let resizeObserver: ResizeObserver | null = null;
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
const periodLabel = computed(() => interval.value === "daily" ? "30 days" : "8 weeks");
const compactSummary = computed(() => {
  if (isLoading.value && !response.value) return "Loading activity…";
  if (error.value && !response.value) return "Activity unavailable";
  return `${total.value} deployment${total.value === 1 ? "" : "s"} in ${periodLabel.value}`;
});

function formatBucket(start: string, long = false): string {
  const date = new Date(start);
  const formatted = new Intl.DateTimeFormat("en", {
    month: "short",
    day: "numeric",
    ...(long ? { year: "numeric" } : {}),
    timeZone: "UTC",
  }).format(date);
  return interval.value === "weekly" && long ? `Week of ${formatted}` : formatted;
}

function bucketTotal(bucket: DeploymentActivityBucket): number {
  return statuses.reduce((sum, status) => sum + bucket[status.key], 0);
}

const chartOption = computed<EChartsCoreOption>(() => ({
  animation: false,
  grid: { left: 8, right: 8, top: 8, bottom: 20, outerBoundsMode: "same", outerBoundsContain: "axisLabel" },
  tooltip: {
    trigger: "axis",
    axisPointer: { type: "shadow" },
    backgroundColor: "var(--surface-elevated)",
    borderColor: "var(--border-default)",
    textStyle: { color: "var(--text)", fontSize: 12 },
    formatter: (items: Array<{ dataIndex: number }>) => {
      const bucket = buckets.value[items[0]?.dataIndex ?? 0];
      if (!bucket) return "";
      const breakdown = statuses.map((status) => `${status.label}: ${bucket[status.key]}`).join("<br />");
      return `<strong>${formatBucket(bucket.start, true)}</strong><br />Total: ${bucketTotal(bucket)}<br />${breakdown}`;
    },
  },
  xAxis: {
    type: "category",
    data: buckets.value.map((bucket) => formatBucket(bucket.start)),
    axisLine: { lineStyle: { color: "var(--border-default)" } },
    axisTick: { show: false },
    axisLabel: { color: "var(--muted)", fontSize: 11, hideOverlap: true },
  },
  yAxis: {
    type: "value",
    minInterval: 1,
    axisLabel: { color: "var(--muted)", fontSize: 11 },
    splitLine: { lineStyle: { color: "var(--border-subtle)" } },
  },
  series: statuses.map((status) => ({
    name: status.label,
    type: "bar",
    stack: "deployments",
    data: buckets.value.map((bucket) => bucket[status.key]),
    itemStyle: { color: status.color },
    emphasis: { focus: "series" },
  })),
}));

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
      [selected]: reason instanceof Error ? reason.message : "Request failed",
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

function renderChart() {
  if (!chartElement.value || !response.value || total.value === 0) return;
  chart ??= init(chartElement.value, undefined, { renderer: "svg" });
  chart.setOption(chartOption.value, true);
  chart.resize();
}

watch(chartOption, async () => {
  await nextTick();
  renderChart();
});

watch(() => props.stack, () => {
  request++;
  responses.value = {};
  loading.value = {};
  errors.value = {};
  interval.value = "weekly";
  chart?.clear();
  void load("weekly");
}, { immediate: true });

onMounted(() => {
  if (typeof ResizeObserver !== "undefined" && chartElement.value) {
    resizeObserver = new ResizeObserver(() => chart?.resize());
    resizeObserver.observe(chartElement.value);
  }
  renderChart();
});

onBeforeUnmount(() => {
  request++;
  resizeObserver?.disconnect();
  chart?.dispose();
});
</script>

<template>
  <section class="deployment-activity" aria-labelledby="deployment-activity-title">
    <header class="overview-summary-panel-header">
      <div class="deployment-activity-heading">
        <span class="overview-summary-panel-icon" aria-hidden="true">
          <DeploymentIcon name="calendar" />
        </span>
        <h3 id="deployment-activity-title" class="overview-panel-title">Deployment Activity</h3>
      </div>
    </header>

    <div class="deployment-activity-content">
      <div class="deployment-activity-toolbar">
        <div class="deployment-activity-interval" aria-label="Deployment activity interval">
          <button type="button" :class="{ active: interval === 'daily' }" :aria-pressed="interval === 'daily'" @click="selectInterval('daily')">Daily</button>
          <button type="button" :class="{ active: interval === 'weekly' }" :aria-pressed="interval === 'weekly'" @click="selectInterval('weekly')">Weekly</button>
        </div>
        <ul class="deployment-activity-legend" aria-label="Deployment statuses">
          <li v-for="status in statuses" :key="status.key">
            <span class="deployment-activity-dot" :style="{ backgroundColor: status.color }" />{{ status.label }}
          </li>
        </ul>
      </div>

      <p v-if="isLoading && !response" class="deployment-activity-state" role="status">Loading deployment activity…</p>
      <p v-else-if="error && !response" class="deployment-activity-state deployment-activity-error" role="alert">
        Failed to load deployment activity: {{ error }}
        <button type="button" class="button-ghost" @click="retry">Retry</button>
      </p>
      <p v-else-if="response && total === 0" class="deployment-activity-state">No deployments in the selected period.</p>
      <div v-show="response && total > 0" ref="chartElement" class="deployment-activity-chart" role="img" :aria-label="compactSummary" />
      <dl v-if="response && total > 0" class="deployment-activity-summary">
        <div><dt>Total</dt><dd>{{ total }}</dd></div>
        <div><dt>Succeeded</dt><dd>{{ totals.succeeded }}</dd></div>
        <div><dt>Failed</dt><dd>{{ totals.failed }}</dd></div>
      </dl>
    </div>
  </section>
</template>

<style scoped>
/* Hallmark · full redesign · genre: modern-minimal · shape: app component · role: secondary activity · built: the project · v2.0.0 */
/* Hallmark · pre-emit critique: P4 H4 E4 S5 R5 V4 · weakest: E, dense chart labels are enlarged and redundant totals removed */
.deployment-activity { min-width: 0; border: 1px solid var(--border-subtle); border-radius: 10px; background: var(--surface-card-soft); overflow: hidden; }
.deployment-activity-heading { display: flex; align-items: center; min-width: 0; gap: 9px; padding: 17px 16px 13px; background: var(--surface-panel-header); }
.deployment-activity-content { display: grid; gap: 12px; padding: 0 16px 16px; border-top: 1px solid var(--border-subtle); }
.deployment-activity-toolbar { min-width: 0; display: flex; align-items: center; justify-content: space-between; gap: 16px; padding-top: 12px; }
.deployment-activity-interval { display: inline-flex; gap: 4px; border-bottom: 1px solid var(--border-default); }
.deployment-activity-interval button { position: relative; min-height: 32px; padding: 4px 10px; border: 0; border-radius: 4px 4px 0 0; background: transparent; color: var(--muted); box-shadow: none; font-size: .78rem; font-weight: 500; }
.deployment-activity-interval button::after { content: ""; position: absolute; right: 8px; bottom: -1px; left: 8px; height: 2px; border-radius: 2px 2px 0 0; background: transparent; }
.deployment-activity-interval button:hover { transform: none; filter: none; color: var(--text); background: var(--surface-hover); }
.deployment-activity-interval button.active { color: var(--text); font-weight: 650; }
.deployment-activity-interval button.active::after { background: var(--text); }
.deployment-activity-interval button:active, .deployment-activity-error .button-ghost:active { transform: translateY(1px); }
.deployment-activity-interval button:disabled, .deployment-activity-error .button-ghost:disabled { color: var(--muted); background: var(--surface-muted); opacity: .65; cursor: not-allowed; }
.deployment-activity-interval button:focus-visible, .deployment-activity-error .button-ghost:focus-visible { outline: 2px solid var(--accent-focus); outline-offset: 2px; }
.deployment-activity-heading .overview-panel-title { white-space: nowrap; }
.deployment-activity-legend { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 8px 16px; margin: 0; padding: 0; list-style: none; color: var(--muted); font-size: .76rem; }
.deployment-activity-legend li { display: inline-flex; align-items: center; gap: 4px; }
.deployment-activity-dot { width: 7px; height: 7px; border-radius: 50%; }
.deployment-activity-chart { width: 100%; height: 190px; min-width: 0; }
.deployment-activity-state { min-height: 118px; display: flex; align-items: center; justify-content: center; gap: 8px; margin: 0; color: var(--muted); font-size: .82rem; font-variant-numeric: tabular-nums; }
.deployment-activity-error { color: var(--error); }
.deployment-activity-summary { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); margin: 0; border-top: 1px solid var(--border-subtle); }
.deployment-activity-summary > div { min-width: 0; display: grid; gap: 4px; padding: 12px 12px 0; }
.deployment-activity-summary > div + div { border-left: 1px solid var(--border-subtle); }
.deployment-activity-summary dt { color: var(--muted); font-size: .75rem; white-space: nowrap; }
.deployment-activity-summary dd { grid-row: 1; margin: 0; color: var(--text); font-size: .95rem; font-weight: 650; font-variant-numeric: tabular-nums; }
@media (max-width: 900px) {
  .deployment-activity-toolbar { align-items: flex-start; flex-direction: column; }
  .deployment-activity-legend { justify-content: flex-start; }
}
@media (max-width: 560px) {
  .deployment-activity-heading { padding: 15px 13px 13px; }
  .deployment-activity-chart { height: 170px; }
  .deployment-activity-summary { grid-template-columns: repeat(3, minmax(0, 1fr)); }
}
@media (prefers-reduced-motion: reduce) {
  .deployment-activity button { transition-duration: 0s; }
}
</style>
