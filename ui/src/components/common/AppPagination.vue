<script setup lang="ts">
import AppButton from "./AppButton.vue";

defineProps<{
  page: number;
  loading: boolean;
  hasNext: boolean;
  label: string;
}>();

defineEmits<{ previous: []; next: [] }>();
</script>

<template>
  <nav class="app-pagination" :aria-label="label">
    <AppButton variant="secondary" compact :disabled="loading || page === 0" @click="$emit('previous')">Previous</AppButton>
    <span>Page {{ page + 1 }}</span>
    <AppButton variant="secondary" compact :disabled="loading || !hasNext" @click="$emit('next')">Next</AppButton>
  </nav>
</template>

<style scoped>
.app-pagination {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 12px;
}

</style>
