<script setup lang="ts">
import { computed, ref } from "vue";

const model = defineModel<string>({ required: true });
const props = defineProps<{
  label: string;
  placeholder: string;
  searchLabel: string;
  emptyMessage: string;
  options: { value: string; label: string }[];
}>();

const dropdown = ref<HTMLDetailsElement | null>(null);
const search = ref("");
const selection = computed(() => props.options.find((option) => option.value === model.value)?.label
  ?? (model.value || props.placeholder));
const matches = computed(() => {
  const query = search.value.trim().toLocaleLowerCase();
  return props.options.filter((option) => option.label.toLocaleLowerCase().includes(query));
});

function close(): void {
  search.value = "";
  if (dropdown.value) dropdown.value.open = false;
}

function select(value: string): void {
  model.value = value;
  close();
}

defineExpose({ close });
</script>

<template>
  <div class="events-filter-field">
    <details ref="dropdown" class="events-search-filter events-filter-dropdown">
      <summary class="events-search-filter-trigger" :aria-label="label">
        <slot name="icon" />
        <span class="events-filter-selection">{{ selection }}</span>
      </summary>
      <div class="events-search-filter-menu">
        <input v-model="search" type="search" :placeholder="searchLabel" :aria-label="searchLabel" />
        <button type="button" :class="{ selected: !model }" @click="select('')">{{ placeholder }}</button>
        <button
          v-for="option in matches"
          :key="option.value"
          type="button"
          :class="{ selected: model === option.value }"
          @click="select(option.value)"
        >
          {{ option.label }}
        </button>
        <p v-if="matches.length === 0" class="events-filter-empty">{{ emptyMessage }}</p>
      </div>
    </details>
  </div>
</template>
