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

use([BarChart, GridComponent, TooltipComponent, SVGRenderer]);

const props = defineProps<{ stack: string }>();

const statuses = [
  { key: "succeeded", label: "Succeeded", color: "#3fb950" },
  { key: "failed", label: "Failed", color: "#f85149" },
  { key: "running", label: "Running", color: "#d29922" },
  { key: "interrupted", label: "Interrupted", color: "#8b949e" },
  { key: "superseded", label: "Superseded", color: "#58a6ff" },
] as const;

const expanded = ref(true);
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
const average = computed(() => buckets.value.length ? total.value / buckets.value.length : 0);
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
    backgroundColor: "#161b22",
    borderColor: "#30363d",
    textStyle: { color: "#f0f6fc", fontSize: 12 },
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
    axisLine: { lineStyle: { color: "rgba(128, 143, 163, .35)" } },
    axisTick: { show: false },
    axisLabel: { color: "#8b949e", fontSize: 11, hideOverlap: true },
  },
  yAxis: {
    type: "value",
    minInterval: 1,
    axisLabel: { color: "#8b949e", fontSize: 11 },
    splitLine: { lineStyle: { color: "rgba(128, 143, 163, .18)" } },
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
  if (!expanded.value || !chartElement.value || !response.value || total.value === 0) return;
  chart ??= init(chartElement.value, undefined, { renderer: "svg" });
  chart.setOption(chartOption.value, true);
  chart.resize();
}

watch([chartOption, expanded], async () => {
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
    <button
      type="button"
      class="deployment-activity-toggle"
      :aria-expanded="expanded"
      aria-controls="deployment-activity-content"
      @click="expanded = !expanded"
    >
      <span id="deployment-activity-title">Deployment Activity</span>
      <span v-if="!expanded" class="deployment-activity-compact">{{ compactSummary }}</span>
      <svg class="deployment-activity-chevron" :class="{ 'is-expanded': expanded }" viewBox="0 0 20 20" aria-hidden="true">
        <path d="m6 8 4 4 4-4" />
      </svg>
    </button>

    <div v-show="expanded" id="deployment-activity-content" class="deployment-activity-content">
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
        <div><dt>Average / {{ interval === "daily" ? "day" : "week" }}</dt><dd>{{ average.toFixed(1) }}</dd></div>
        <div v-for="status in statuses" :key="status.key"><dt>{{ status.label }}</dt><dd>{{ totals[status.key] }}</dd></div>
      </dl>
    </div>
  </section>
</template>

<style scoped>
.deployment-activity { min-width: 0; border: 1px solid var(--border-subtle); border-radius: 10px; background: var(--surface-card-soft); overflow: hidden; }
.deployment-activity-toggle { width: 100%; min-height: 50px; display: grid; grid-template-columns: auto minmax(0, 1fr) auto; align-items: center; gap: 12px; padding: 10px 14px; border: 0; border-radius: 0; background: transparent; color: var(--text); box-shadow: none; text-align: left; }
.deployment-activity-toggle:hover { transform: none; background: var(--surface-hover); filter: none; }
.deployment-activity-toggle:focus-visible { outline: 2px solid var(--accent-focus); outline-offset: -2px; }
#deployment-activity-title { font-size: .94rem; font-weight: 650; }
.deployment-activity-compact { color: var(--muted); font-size: .78rem; }
.deployment-activity-chevron { width: 18px; height: 18px; color: var(--muted); transition: none; }
.deployment-activity-chevron path { fill: none; stroke: currentColor; stroke-width: 1.8; stroke-linecap: round; stroke-linejoin: round; }
.deployment-activity-chevron.is-expanded { transform: rotate(180deg); }
.deployment-activity-content { display: grid; gap: 12px; padding: 0 14px 13px; border-top: 1px solid var(--border-subtle); }
.deployment-activity-toolbar { min-width: 0; display: flex; align-items: center; justify-content: space-between; gap: 14px; padding-top: 12px; }
.deployment-activity-interval { display: inline-flex; padding: 2px; border: 1px solid var(--border-default); border-radius: 7px; background: var(--surface-muted); }
.deployment-activity-interval button { min-height: 28px; padding: 4px 10px; border: 0; border-radius: 5px; background: transparent; color: var(--muted); box-shadow: none; font-size: .74rem; }
.deployment-activity-interval button:hover { transform: none; filter: none; color: var(--text); }
.deployment-activity-interval button.active { background: var(--surface-control); color: var(--text); }
.deployment-activity-legend { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 7px 13px; margin: 0; padding: 0; list-style: none; color: var(--muted); font-size: .72rem; }
.deployment-activity-legend li { display: inline-flex; align-items: center; gap: 5px; }
.deployment-activity-dot { width: 7px; height: 7px; border-radius: 50%; }
.deployment-activity-chart { width: 100%; height: 190px; min-width: 0; }
.deployment-activity-state { min-height: 118px; display: flex; align-items: center; justify-content: center; gap: 8px; margin: 0; color: var(--muted); font-size: .82rem; }
.deployment-activity-error { color: var(--error); }
.deployment-activity-summary { display: grid; grid-template-columns: repeat(7, minmax(0, 1fr)); margin: 0; border-top: 1px solid var(--border-subtle); }
.deployment-activity-summary > div { min-width: 0; display: grid; gap: 2px; padding: 10px 12px 0; }
.deployment-activity-summary > div + div { border-left: 1px solid var(--border-subtle); }
.deployment-activity-summary dt { color: var(--muted); font-size: .68rem; white-space: nowrap; }
.deployment-activity-summary dd { grid-row: 1; margin: 0; color: var(--text); font-size: .9rem; font-weight: 650; }
@media (max-width: 900px) {
  .deployment-activity-toolbar { align-items: flex-start; flex-direction: column; }
  .deployment-activity-legend { justify-content: flex-start; }
  .deployment-activity-summary { grid-template-columns: repeat(4, minmax(0, 1fr)); }
  .deployment-activity-summary > div:nth-child(5) { border-left: 0; }
  .deployment-activity-summary > div:nth-child(n + 5) { padding-top: 10px; border-top: 1px solid var(--border-subtle); }
}
@media (max-width: 560px) {
  .deployment-activity-toggle { grid-template-columns: minmax(0, 1fr) auto; }
  .deployment-activity-compact { grid-column: 1 / -1; grid-row: 2; }
  .deployment-activity-chart { height: 170px; }
  .deployment-activity-summary { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .deployment-activity-summary > div:nth-child(n) { border-left: 0; border-top: 0; }
  .deployment-activity-summary > div:nth-child(even) { border-left: 1px solid var(--border-subtle); }
  .deployment-activity-summary > div:nth-child(n + 3) { padding-top: 10px; border-top: 1px solid var(--border-subtle); }
}
</style>
