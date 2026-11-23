<script setup lang="ts">
import { formatNumber, t } from "../../i18n";
import { BarChart } from "echarts/charts";
import { GridComponent, TooltipComponent } from "echarts/components";
import { init, use, type EChartsCoreOption } from "echarts/core";
import { SVGRenderer } from "echarts/renderers";
import { computed, ref, watch } from "vue";

import { escapeHtml } from "../../utils/escape";

use([BarChart, GridComponent, TooltipComponent, SVGRenderer]);

const props = defineProps<{
  legendLabel: string;
  chartLabel: string;
  labels: string[];
  tooltipLabels: string[];
  series: { label: string; color: string; values: number[] }[];
  totals: number[];
  summary: { label: string; value: number }[];
  showPlot: boolean;
  formatValue?: (value: number) => string;
  formatAxisValue?: (value: number) => string;
}>();

const chartElement = ref<HTMLElement | null>(null);

function format(value: number): string {
  return props.formatValue?.(value) ?? formatNumber(value);
}

const chartOption = computed<EChartsCoreOption>(() => ({
  animation: false,
  grid: { left: 8, right: 8, top: 8, bottom: 20, outerBoundsMode: "same", outerBoundsContain: "axisLabel" },
  tooltip: {
    trigger: "axis",
    triggerOn: "mousemove|click",
    axisPointer: { type: "shadow" },
    backgroundColor: "var(--surface-elevated)",
    borderColor: "var(--border-default)",
    textStyle: { color: "var(--text)", fontSize: 12 },
    confine: true,
    formatter: (items: Array<{ dataIndex: number }>) => {
      const index = items[0]?.dataIndex;
      if (index === undefined) return "";
      const title = escapeHtml(props.tooltipLabels[index]);
      const total = escapeHtml(t("common.chartTotal", { total: format(props.totals[index]) }));
      const breakdown = props.series.map(({ label, values }) =>
        escapeHtml(t("common.chartSeries", { label, value: format(values[index]) }))
      );
      return [`<strong>${title}</strong>`, total, ...breakdown].join("<br />");
    },
  },
  xAxis: {
    type: "category",
    data: props.labels,
    axisLine: { lineStyle: { color: "var(--border-default)" } },
    axisTick: { show: false },
    axisLabel: { color: "var(--muted)", fontSize: 11, hideOverlap: true },
  },
  yAxis: {
    type: "value",
    minInterval: 1,
    axisLabel: { color: "var(--muted)", fontSize: 11, formatter: props.formatAxisValue ?? ((value: number) => formatNumber(value)) },
    splitLine: { lineStyle: { color: "var(--border-subtle)" } },
  },
  series: props.series.map(({ label, color, values }) => ({
    name: label,
    type: "bar",
    stack: "total",
    data: values,
    itemStyle: { color },
    emphasis: { focus: "series" },
  })),
}));

watch(chartElement, (element, _, onCleanup) => {
  if (!element) return;

  const chart = init(element, undefined, { renderer: "svg" });
  const stopRendering = watch(chartOption, (option) => chart.setOption(option, true), { immediate: true });
  const resizeObserver = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(() => chart.resize());
  resizeObserver?.observe(element);

  onCleanup(() => {
    stopRendering();
    resizeObserver?.disconnect();
    chart.dispose();
  });
}, { flush: "post" });
</script>

<template>
  <div class="app-stacked-chart">
    <div class="app-stacked-chart-toolbar" :class="{ 'has-controls': $slots.controls }">
      <slot name="controls" />
      <ul class="app-stacked-chart-legend" :aria-label="legendLabel">
        <li v-for="item in series" :key="item.label">
          <span aria-hidden="true" :style="{ backgroundColor: item.color }" />{{ item.label }}
        </li>
      </ul>
    </div>

    <slot name="status" />
    <template v-if="showPlot">
      <div ref="chartElement" class="app-stacked-chart-plot" role="img" :aria-label="chartLabel" />
      <dl class="app-stacked-chart-summary">
        <div v-for="item in summary" :key="item.label">
          <dt>{{ item.label }}</dt>
          <dd>{{ format(item.value) }}</dd>
        </div>
      </dl>
    </template>
  </div>
</template>

<style scoped>
.app-stacked-chart { min-width: 0; display: grid; gap: 12px; }
.app-stacked-chart-toolbar { min-width: 0; display: flex; align-items: center; gap: 16px; }
.app-stacked-chart-toolbar.has-controls { justify-content: space-between; }
.app-stacked-chart-legend { display: flex; flex-wrap: wrap; gap: 8px 16px; margin: 0; padding: 0; list-style: none; color: var(--muted); font-size: .76rem; }
.has-controls .app-stacked-chart-legend { justify-content: flex-end; }
.app-stacked-chart-legend li { display: inline-flex; align-items: center; gap: 4px; }
.app-stacked-chart-legend span { width: 7px; height: 7px; border-radius: 50%; }
.app-stacked-chart-plot { width: 100%; height: var(--stacked-chart-height, 190px); min-width: 0; }
.app-stacked-chart-summary { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); margin: 0; border-top: 1px solid var(--border-subtle); }
.app-stacked-chart-summary > div { min-width: 0; display: grid; gap: 4px; padding: 12px 12px 0; }
.app-stacked-chart-summary > div + div { border-left: 1px solid var(--border-subtle); }
.app-stacked-chart-summary dt { color: var(--muted); font-size: .75rem; }
.app-stacked-chart-summary dd { grid-row: 1; margin: 0; color: var(--text); font-size: .95rem; font-weight: 650; font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }

@media (max-width: 900px) {
  .app-stacked-chart-toolbar.has-controls { align-items: flex-start; flex-direction: column; }
  .has-controls .app-stacked-chart-legend { justify-content: flex-start; }
}
@media (max-width: 560px) {
  .app-stacked-chart-plot { height: var(--stacked-chart-height-mobile, 170px); }
  .app-stacked-chart-summary > div { padding-inline: var(--stacked-chart-summary-padding-mobile, 12px); }
}
</style>
