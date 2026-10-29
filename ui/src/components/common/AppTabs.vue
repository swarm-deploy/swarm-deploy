<script setup lang="ts">
import { ref } from "vue";

export interface AppTab {
  value: string;
  label: string;
  id?: string;
  panelId?: string;
  disabled?: boolean;
}

const props = withDefaults(defineProps<{
  modelValue: string;
  tabs: readonly AppTab[];
  label: string;
  density?: "default" | "compact";
}>(), {
  density: "default",
});

const emit = defineEmits<{ "update:modelValue": [value: string] }>();
defineSlots<{
  tab(props: { tab: AppTab; active: boolean }): unknown;
}>();

const root = ref<HTMLElement | null>(null);

function select(tab: AppTab): void {
  if (!tab.disabled) emit("update:modelValue", tab.value);
}

function handleKeydown(event: KeyboardEvent, index: number): void {
  const enabledIndexes = props.tabs
    .map((tab, tabIndex) => ({ tab, tabIndex }))
    .filter(({ tab }) => !tab.disabled)
    .map(({ tabIndex }) => tabIndex);
  const currentIndex = enabledIndexes.indexOf(index);
  if (currentIndex === -1) return;

  let targetIndex: number | undefined;
  if (event.key === "ArrowRight") targetIndex = enabledIndexes[(currentIndex + 1) % enabledIndexes.length];
  if (event.key === "ArrowLeft") targetIndex = enabledIndexes[(currentIndex - 1 + enabledIndexes.length) % enabledIndexes.length];
  if (event.key === "Home") targetIndex = enabledIndexes[0];
  if (event.key === "End") targetIndex = enabledIndexes[enabledIndexes.length - 1];
  if (targetIndex === undefined) return;

  event.preventDefault();
  select(props.tabs[targetIndex]);
  root.value?.querySelector<HTMLElement>(`[data-tab-index="${targetIndex}"]`)?.focus();
}
</script>

<template>
  <div
    ref="root"
    class="app-tabs"
    :class="`app-tabs--${density}`"
    role="tablist"
    :aria-label="label"
  >
    <button
      v-for="(tab, index) in tabs"
      :id="tab.id"
      :key="tab.value"
      type="button"
      role="tab"
      class="app-tabs-tab"
      :class="{ 'app-tabs-tab--active': modelValue === tab.value }"
      :aria-controls="tab.panelId"
      :aria-selected="modelValue === tab.value"
      :disabled="tab.disabled"
      :tabindex="modelValue === tab.value ? 0 : -1"
      :data-tab-index="index"
      @click="select(tab)"
      @keydown="handleKeydown($event, index)"
    >
      <slot name="tab" :tab="tab" :active="modelValue === tab.value">{{ tab.label }}</slot>
    </button>
  </div>
</template>

<style scoped>
.app-tabs {
  display: flex;
  gap: 8px;
  border-bottom: 1px solid var(--line);
}

.app-tabs--compact {
  gap: 6px;
}

.app-tabs-tab {
  border: 0;
  border-bottom: 3px solid transparent;
  border-radius: 0;
  background: transparent;
  color: var(--muted);
  padding: 10px 10px 12px;
}

.app-tabs--compact .app-tabs-tab {
  padding: 8px 8px 9px;
}

.app-tabs-tab:hover:not(:disabled) {
  color: var(--text);
  transform: none;
  filter: none;
}

.app-tabs-tab:focus-visible {
  outline: 2px solid var(--accent-focus);
  outline-offset: 2px;
}

.app-tabs-tab--active {
  border-bottom-color: var(--control-primary);
  color: var(--control-primary);
}

:global(:root[data-theme="dark"]) .app-tabs-tab--active {
  color: var(--text);
}
</style>
