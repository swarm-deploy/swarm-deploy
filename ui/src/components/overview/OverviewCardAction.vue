<script setup lang="ts">
import type { RouteLocationRaw } from "vue-router";

defineProps<{
  to: RouteLocationRaw;
}>();
</script>

<template>
  <RouterLink :to="to" class="overview-card-action">View all</RouterLink>
</template>
