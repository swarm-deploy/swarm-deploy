<script setup lang="ts">
withDefaults(
  defineProps<{
    message?: string;
  }>(),
  {
    message: "No data available.",
  },
);
</script>

<template>
  <div class="app-table-empty">
    <p class="meta"><slot>{{ message }}</slot></p>
  </div>
</template>
