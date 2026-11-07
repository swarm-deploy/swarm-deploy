<script setup lang="ts">
import { useAttrs } from "vue";
import type { RouteLocationRaw } from "vue-router";

defineOptions({ inheritAttrs: false });

withDefaults(
  defineProps<{
    type?: "button" | "submit" | "reset";
    variant?: "primary" | "secondary" | "danger";
    compact?: boolean;
    to?: RouteLocationRaw;
  }>(),
  {
    type: "button",
    variant: "primary",
    compact: false,
  },
);

const attrs = useAttrs();
</script>

<template>
  <RouterLink
    v-if="to"
    v-bind="attrs"
    :to="to"
    class="app-button"
    :class="[`app-button--${variant}`, { 'app-button--compact': compact }]"
  >
    <slot />
  </RouterLink>
  <button
    v-else
    v-bind="attrs"
    :type="type"
    class="app-button"
    :class="[`app-button--${variant}`, { 'app-button--compact': compact }]"
  >
    <slot />
  </button>
</template>

<style scoped>
.app-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 7px;
  border: 1px solid;
  border-radius: 8px;
  padding: 10px 16px;
  font-weight: 700;
  line-height: normal;
  cursor: pointer;
  box-shadow: none;
  text-decoration: none;
  transition: transform 120ms ease, filter 120ms ease, background-color 120ms ease, border-color 120ms ease;
}

.app-button--compact {
  padding: 8px 13px;
}

.app-button--primary {
  border-color: var(--accent-border);
  border-radius: 6px;
  color: var(--text-on-accent);
  background: var(--control-primary);
  font-weight: 600;
}

.app-button--primary:hover:not(:disabled) {
  background: var(--control-primary-hover);
  filter: none;
}

.app-button--secondary {
  border-color: var(--border-default);
  color: var(--text);
  background: var(--surface-control);
}

.app-button--secondary:hover:not(:disabled) {
  filter: brightness(1.08);
}

.app-button--danger {
  border-color: rgba(248, 113, 113, 0.36);
  color: var(--error);
  background: var(--error-muted);
}

.app-button:hover:not(:disabled) {
  transform: translateY(-1px);
}

.app-button:active:not(:disabled) {
  transform: translateY(1px);
}

.app-button:focus-visible {
  outline: 2px solid var(--accent-focus);
  outline-offset: 2px;
}

.app-button:disabled {
  cursor: not-allowed;
  opacity: 0.65;
}

.app-button:disabled:hover {
  transform: none;
  filter: none;
}
</style>
