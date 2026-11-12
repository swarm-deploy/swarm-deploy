<script setup lang="ts">
import type { AlertStatus } from "../../api/types";
import AppStatusBadge from "../common/AppStatusBadge.vue";

defineProps<{ status: AlertStatus }>();
</script>

<template>
  <AppStatusBadge :variant="status === 'resolved' ? 'neutral' : 'error'" dot>
    {{ status === "resolved" ? "Resolved" : "Active" }}
  </AppStatusBadge>
</template>
