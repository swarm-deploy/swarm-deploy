<script setup lang="ts">
import { enumLabel, t } from "../../i18n";
import { computed } from "vue";

import AppStatusBadge from "../common/AppStatusBadge.vue";

const props = defineProps<{ state: string }>();

const state = computed(() => props.state?.trim().toLowerCase() ?? "");
const label = computed(() => {
  const value = state.value;
  return value ? enumLabel("taskStates", value, value.charAt(0).toUpperCase() + value.slice(1)) : t("common.unknown");
});
const variant = computed(() => {
  if (["running", "complete", "completed"].includes(state.value)) return "success";
  if (["failed", "rejected", "shutdown"].includes(state.value)) return "error";
  return "neutral";
});
</script>

<template>
  <AppStatusBadge class="task-status-badge" :variant="variant" dot>{{ label }}</AppStatusBadge>
</template>

<style scoped>
.task-status-badge { border: 0; padding: 0; }
</style>
