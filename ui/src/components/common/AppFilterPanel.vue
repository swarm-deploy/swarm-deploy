<script setup lang="ts">
import { ref } from "vue";

defineProps<{
  panelId: string;
  activeCount: number;
  columns: number;
}>();

const open = ref(false);
</script>

<template>
  <header class="services-header events-header">
    <button
      class="events-mobile-filter-toggle"
      type="button"
      :aria-expanded="open"
      :aria-controls="panelId"
      @click="open = !open"
    >
      Filters ({{ activeCount }})
    </button>
    <div
      :id="panelId"
      class="events-filter-bar"
      :class="{ 'events-filter-bar--open': open }"
      :style="{ '--filter-columns': columns }"
    >
      <slot />
    </div>
    <slot name="after" />
  </header>
</template>

<style scoped>
.events-header {
  display: block;
}
.events-mobile-filter-toggle {
  display: none;
}
.events-filter-bar {
  display: grid;
  grid-template-columns: repeat(var(--filter-columns), minmax(0, 1fr));
  gap: 10px;
  align-items: center;
  box-sizing: border-box;
  width: 100%;
  min-width: 0;
  border: 1px solid var(--border-subtle);
  border-radius: 10px;
  background: var(--surface-card-soft);
  padding: 12px;
}
.events-filter-bar :deep(.events-filter-icon) {
  width: 17px;
  height: 17px;
  flex: 0 0 17px;
  color: var(--muted);
  fill: none;
  stroke: currentColor;
  stroke-width: 1.6;
  stroke-linecap: round;
  stroke-linejoin: round;
  pointer-events: none;
}
.events-filter-bar :deep(.app-select:last-child .app-select-menu) {
  left: auto;
  right: 0;
}
@media (max-width: 980px) {
  .events-filter-bar {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
  .events-filter-bar :deep(.app-select:nth-child(3n) .app-select-menu) {
    left: auto;
    right: 0;
  }
}
@media (max-width: 720px) {
  .events-mobile-filter-toggle {
    display: inline-flex;
    align-items: center;
    width: max-content;
    min-height: 2.3rem;
    border: 1px solid var(--border-default);
    border-radius: 0.45rem;
    background: var(--surface-elevated);
    color: var(--text);
    padding: 0.45rem 0.75rem;
    font: inherit;
    font-size: 0.84rem;
  }
  .events-filter-bar {
    display: none;
    grid-template-columns: 1fr;
    padding: 10px;
  }
  .events-filter-bar--open {
    display: grid;
  }
  .events-filter-bar :deep(.app-select-menu) {
    width: 100%;
    max-width: 100%;
  }
}
</style>
