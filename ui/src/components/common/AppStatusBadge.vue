<script setup lang="ts">
withDefaults(
  defineProps<{
    variant?: "success" | "warning" | "error" | "neutral" | "info";
    filled?: boolean;
    dot?: boolean;
  }>(),
  {
    variant: "neutral",
    filled: false,
    dot: false,
  },
);
</script>

<template>
  <span
    class="app-status-badge"
    :class="[`app-status-badge--${variant}`, { 'app-status-badge--filled': filled }]"
  >
    <span v-if="dot" class="app-status-badge-dot" aria-hidden="true" />
    <slot />
  </span>
</template>
