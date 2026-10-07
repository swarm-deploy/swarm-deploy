<script setup lang="ts">
import type { RouteLocationRaw } from "vue-router";

defineProps<{
  interactive?: boolean;
  to?: RouteLocationRaw;
  ariaLabel?: string;
}>();

defineEmits<{
  activate: [];
}>();
</script>

<template>
  <RouterLink v-if="to" :to="to" class="overview-summary-row" :aria-label="ariaLabel">
    <slot />
  </RouterLink>
  <button
    v-else-if="interactive"
    type="button"
    class="overview-summary-row"
    :aria-label="ariaLabel"
    @click="$emit('activate')"
  >
    <slot />
  </button>
  <div v-else class="overview-summary-row">
    <slot />
  </div>
</template>
