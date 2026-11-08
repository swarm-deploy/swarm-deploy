<script setup lang="ts" generic="T extends string | number">
defineProps<{ label: string }>();

const model = defineModel<T>({ required: true });
</script>

<template>
  <span class="app-select" :class="{ 'app-select--with-icon': $slots.icon }">
    <span v-if="$slots.icon" class="app-select-icon" aria-hidden="true"><slot name="icon" /></span>
    <select v-model="model" :aria-label="label"><slot /></select>
    <svg class="app-select-chevron" viewBox="0 0 24 24" aria-hidden="true"><path d="m6 9 6 6 6-6" /></svg>
  </span>
</template>

<style scoped>
.app-select {
  position: relative;
  display: inline-grid;
  min-width: 0;
}

.app-select select {
  width: 100%;
  min-width: 0;
  height: var(--app-select-height, 38px);
  appearance: none;
  border: 1px solid var(--border-default);
  border-radius: 8px;
  background: var(--surface-input);
  color: var(--text);
  padding: 0 30px 0 10px;
  font: inherit;
  font-size: 0.875rem;
  cursor: pointer;
}

.app-select select:focus-visible {
  outline: 2px solid var(--accent-focus);
  outline-offset: 2px;
}

.app-select--with-icon select {
  padding-left: 36px;
}

.app-select-icon,
.app-select-chevron {
  position: absolute;
  top: 50%;
  transform: translateY(-50%);
  color: var(--muted);
  pointer-events: none;
}

.app-select-icon {
  left: 11px;
  display: grid;
  place-items: center;
}

.app-select .app-select-chevron {
  right: 8px;
  width: 14px;
  height: 14px;
  fill: none;
  stroke: currentColor;
  stroke-width: 2;
  stroke-linecap: round;
  stroke-linejoin: round;
}

@media (max-width: 650px) {
  .app-select select {
    font-size: 16px;
    padding-right: 24px;
  }

  .app-select--with-icon select {
    padding-left: 29px;
  }

  .app-select-icon {
    left: 8px;
  }

  .app-select .app-select-chevron {
    right: 6px;
  }
}
</style>
