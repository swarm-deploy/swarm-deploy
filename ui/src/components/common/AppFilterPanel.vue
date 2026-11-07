<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from "vue";

defineProps<{
  panelId: string;
  activeCount: number;
  columns: number;
}>();

const open = ref(false);
const panel = ref<HTMLElement | null>(null);

function closeDropdownsOnOutsideClick(event: MouseEvent): void {
  if (!(event.target instanceof Node) || panel.value?.contains(event.target)) return;
  for (const filter of panel.value?.querySelectorAll<HTMLDetailsElement>(".events-filter-dropdown[open]") ?? []) {
    filter.open = false;
  }
}

onMounted(() => document.addEventListener("click", closeDropdownsOnOutsideClick));
onBeforeUnmount(() => document.removeEventListener("click", closeDropdownsOnOutsideClick));
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
      ref="panel"
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
.events-filter-bar :deep(.events-filter-field) {
  position: relative;
  min-width: 0;
}
.events-filter-bar :deep(.events-filter-field select),
.events-filter-bar :deep(.events-filter-dropdown > summary) {
  box-sizing: border-box;
  width: 100%;
  height: 38px;
  border: 1px solid var(--border-default);
  border-radius: 7px;
  background: var(--surface-input);
  color: var(--text);
  font: inherit;
  font-size: 0.83rem;
}
.events-filter-bar :deep(.events-filter-field select) {
  min-width: 0;
  appearance: none;
  padding: 7px 30px 7px 36px;
}
.events-filter-bar :deep(.events-filter-field--select)::after,
.events-filter-bar :deep(.events-search-filter-trigger)::after {
  content: "";
  position: absolute;
  top: 50%;
  right: 13px;
  width: 6px;
  height: 6px;
  border: solid var(--muted);
  border-width: 0 1.5px 1.5px 0;
  transform: translateY(-70%) rotate(45deg);
  pointer-events: none;
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
.events-filter-bar :deep(.events-filter-field--select > .events-filter-icon) {
  position: absolute;
  z-index: 1;
  top: 50%;
  left: 11px;
  transform: translateY(-50%);
}
.events-filter-bar :deep(.events-filter-field select:focus-visible),
.events-filter-bar :deep(.events-filter-dropdown > summary:focus-visible) {
  outline: 2px solid var(--accent-focus);
  outline-offset: 2px;
}
.events-filter-bar :deep(.events-filter-dropdown) {
  position: relative;
  min-width: 0;
}
.events-filter-bar :deep(.events-filter-dropdown > summary) {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 7px 13px 7px 10px;
  cursor: pointer;
  list-style: none;
}
.events-filter-bar :deep(.events-filter-dropdown > .events-search-filter-trigger) {
  position: relative;
  padding-right: 30px;
}
.events-filter-bar :deep(.events-filter-dropdown > summary::-webkit-details-marker) {
  display: none;
}
.events-filter-bar :deep(.events-filter-selection) {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  color: var(--text);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.events-filter-bar :deep(.events-filter-placeholder) {
  color: var(--text);
}
.events-filter-bar :deep(.events-filter-chevron) {
  flex: 0 0 auto;
  color: var(--muted);
}
.events-filter-bar :deep(.events-search-filter-menu) {
  position: absolute;
  z-index: 30;
  top: calc(100% + 0.35rem);
  left: 0;
  box-sizing: border-box;
  width: max(100%, 260px);
  max-width: calc(100vw - 2rem);
  max-height: min(320px, calc(100dvh - 8rem));
  overflow-x: hidden;
  overflow-y: auto;
  border: 1px solid var(--border-default);
  border-radius: 0.5rem;
  background: var(--surface-elevated);
  box-shadow: var(--shadow-popover);
  padding: 0.45rem;
}
.events-filter-bar :deep(.events-search-filter-menu button:hover),
.events-filter-bar :deep(.events-search-filter-menu button.selected) {
  background: var(--surface-hover-soft);
}
.events-filter-bar :deep(.events-search-filter-menu small) {
  color: var(--muted);
  font-size: 0.7rem;
}
.events-filter-bar :deep(.events-search-filter-menu input) {
  box-sizing: border-box;
  width: 100%;
  margin-bottom: 0.35rem;
  border: 1px solid var(--border-default);
  border-radius: 0.35rem;
  background: var(--surface);
  color: var(--text);
  padding: 0.5rem 0.6rem;
  font: inherit;
  font-size: 0.82rem;
}
.events-filter-bar :deep(.events-search-filter-menu button) {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  width: 100%;
  border: 0;
  border-radius: 0.35rem;
  background: transparent;
  color: var(--text);
  padding: 0.5rem 0.55rem;
  text-align: left;
  font: inherit;
  font-size: 0.82rem;
}
.events-filter-bar :deep(.events-filter-empty) {
  margin: 0;
  padding: 0.65rem 0.55rem;
  color: var(--muted);
  font-size: 0.78rem;
}
@media (max-width: 980px) {
  .events-filter-bar {
    grid-template-columns: repeat(3, minmax(0, 1fr));
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
  .events-filter-bar :deep(.events-filter-field select) {
    font-size: 16px;
  }
  .events-filter-bar :deep(.events-search-filter-menu) {
    width: 100%;
    max-width: 100%;
  }
}
</style>
