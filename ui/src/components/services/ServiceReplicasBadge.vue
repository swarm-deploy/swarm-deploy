<script setup lang="ts">
import { computed } from "vue";

import type { SwarmServiceReplicas } from "../../api/types";
import { formatNumber, t } from "../../i18n";
import AppStatusBadge from "../common/AppStatusBadge.vue";

const props = defineProps<{
  replicas?: SwarmServiceReplicas;
  unavailable?: boolean;
}>();

const label = computed(() => {
  if (!props.replicas) return t(props.unavailable ? "common.unavailable" : "common.unknown");
  const { running, desired } = props.replicas;
  return t("service.replicaCount", { running: formatNumber(running), desired: formatNumber(desired) });
});

const variant = computed(() => {
  if (!props.replicas) return props.unavailable ? "error" : "neutral";
  const { running, desired } = props.replicas;
  if (running === desired) return "success";
  if (desired > 0 && running === 0) return "error";
  return "warning";
});
</script>

<template>
  <AppStatusBadge :variant="variant" filled>{{ label }}</AppStatusBadge>
</template>
