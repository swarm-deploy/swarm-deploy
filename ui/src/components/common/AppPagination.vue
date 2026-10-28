<script setup lang="ts">
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
    <button type="button" :disabled="loading || page === 0" @click="$emit('previous')">Previous</button>
    <span>Page {{ page + 1 }}</span>
    <button type="button" :disabled="loading || !hasNext" @click="$emit('next')">Next</button>
  </nav>
</template>

<style scoped>
.app-pagination {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 12px;
}

.app-pagination button {
  border: 1px solid var(--control-primary);
  border-radius: 6px;
  background: transparent;
  color: var(--accent-strong);
  font-weight: 600;
  box-shadow: none;
}

.app-pagination button:hover:not(:disabled) {
  border-color: var(--control-primary-hover);
  background: var(--accent-muted);
  filter: none;
}

.app-pagination button:focus-visible {
  outline: 2px solid var(--accent-focus);
  outline-offset: 2px;
}

.app-pagination button:disabled {
  cursor: not-allowed;
  opacity: 0.65;
}
</style>
