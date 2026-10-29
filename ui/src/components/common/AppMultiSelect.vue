<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from "vue";

interface AppMultiSelectOption {
  value: string;
  label: string;
  description?: string;
  searchText?: string;
}

const props = withDefaults(defineProps<{
  modelValue: string[];
  options: AppMultiSelectOption[];
  label: string;
  placeholder: string;
  heading: string;
  selectionLabel?: string;
  searchable?: boolean;
  searchLabel?: string;
  searchPlaceholder?: string;
  emptyMessage?: string;
  descriptive?: boolean;
}>(), {
  selectionLabel: "",
  searchable: false,
  searchLabel: "Search options",
  searchPlaceholder: "Search",
  emptyMessage: "No matching options",
  descriptive: false,
});

const emit = defineEmits<{ "update:modelValue": [value: string[]] }>();
const root = ref<HTMLDetailsElement | null>(null);
const searchInput = ref<HTMLInputElement | null>(null);
const search = ref("");

const filteredOptions = computed(() => {
  const query = search.value.trim().toLocaleLowerCase();
  if (!query) return props.options;
  return props.options.filter((option) => (
    option.searchText ?? `${option.label} ${option.description ?? ""}`
  ).toLocaleLowerCase().includes(query));
});

const summary = computed(() => {
  if (props.selectionLabel) return props.selectionLabel;
  if (props.modelValue.length === 0) return props.placeholder;
  const labels = props.modelValue.map((value) => (
    props.options.find((option) => option.value === value)?.label ?? value
  ));
  return labels.length <= 2 ? labels.join(", ") : `${labels.length} selected`;
});

function toggle(value: string): void {
  const selected = props.modelValue.includes(value)
    ? props.modelValue.filter((item) => item !== value)
    : [...props.modelValue, value];
  emit("update:modelValue", selected);
}

function clear(): void {
  emit("update:modelValue", []);
}

function handleDocumentClick(event: MouseEvent): void {
  if (!(event.target instanceof Node) || root.value?.contains(event.target)) return;
  close();
}

function handleKeydown(event: KeyboardEvent): void {
  if (event.key !== "Escape" || !root.value?.open) return;
  event.preventDefault();
  close();
  root.value.querySelector("summary")?.focus();
}

function handleToggle(): void {
  if (!root.value?.open) {
    search.value = "";
    return;
  }
  if (props.searchable) void nextTick(() => searchInput.value?.focus());
}

function close(): void {
  if (root.value) root.value.open = false;
  search.value = "";
}

onMounted(() => document.addEventListener("click", handleDocumentClick));
onBeforeUnmount(() => document.removeEventListener("click", handleDocumentClick));
</script>

<template>
  <details
    ref="root"
    class="app-multi-select"
    :class="{ 'app-multi-select--descriptive': descriptive }"
    @toggle="handleToggle"
    @keydown="handleKeydown"
  >
    <summary :aria-label="label">
      <slot name="icon" />
      <span class="app-multi-select-selection" :title="summary">{{ summary }}</span>
      <span class="app-multi-select-chevron" aria-hidden="true">⌄</span>
    </summary>
    <div class="app-multi-select-menu">
      <div class="app-multi-select-header">
        <strong>{{ heading }}</strong>
        <button type="button" :disabled="modelValue.length === 0" @click="clear">Clear</button>
      </div>
      <input
        v-if="searchable"
        ref="searchInput"
        v-model="search"
        type="search"
        :aria-label="searchLabel"
        :placeholder="searchPlaceholder"
      />
      <label v-for="option in filteredOptions" :key="option.value" class="app-multi-select-option">
        <input
          type="checkbox"
          :value="option.value"
          :checked="modelValue.includes(option.value)"
          @change="toggle(option.value)"
        />
        <span class="app-multi-select-option-text">
          <span class="app-multi-select-option-label">{{ option.label }}</span>
          <small v-if="option.description">{{ option.description }}</small>
        </span>
      </label>
      <p v-if="filteredOptions.length === 0" class="app-multi-select-empty">{{ emptyMessage }}</p>
    </div>
  </details>
</template>

<style scoped>
.app-multi-select { position: relative; min-width: 0; }
.app-multi-select > summary {
  box-sizing: border-box;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  width: 100%;
  height: 38px;
  border: 1px solid var(--border-default);
  border-radius: 7px;
  background: var(--surface-input);
  color: var(--text);
  padding: 7px 10px;
  font: inherit;
  font-size: .83rem;
  cursor: pointer;
  list-style: none;
}
.app-multi-select > summary::-webkit-details-marker { display: none; }
.app-multi-select > summary:focus-visible,
.app-multi-select-menu input[type="search"]:focus-visible,
.app-multi-select-header button:focus-visible,
.app-multi-select-option:has(input:focus-visible) {
  outline: 2px solid var(--accent-focus);
  outline-offset: 2px;
}
.app-multi-select-selection { flex: 1 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.app-multi-select-chevron { flex: 0 0 auto; color: var(--muted); }
.app-multi-select-menu {
  position: absolute;
  z-index: 30;
  top: calc(100% + 6px);
  left: 0;
  box-sizing: border-box;
  width: max(100%, 260px);
  max-width: calc(100vw - 32px);
  max-height: min(320px, calc(100dvh - 128px));
  overflow-y: auto;
  border: 1px solid var(--border-default);
  border-radius: 8px;
  background: var(--surface-elevated);
  box-shadow: var(--shadow-popover);
  padding: 7px;
}
.app-multi-select-header { display: flex; align-items: center; justify-content: space-between; padding: 5px 6px 8px; }
.app-multi-select-header button { border: 0; background: transparent; color: var(--control-primary); padding: 0; font: inherit; }
.app-multi-select-header button:disabled { cursor: not-allowed; opacity: .55; }
.app-multi-select-menu > input[type="search"] {
  box-sizing: border-box;
  width: 100%;
  min-width: 0;
  margin-bottom: 4px;
  border: 1px solid var(--border-default);
  border-radius: 6px;
  background: var(--surface-input);
  color: var(--text);
  padding: 7px;
  font: inherit;
  font-size: .82rem;
}
.app-multi-select-option { display: flex; align-items: flex-start; gap: 9px; border-radius: 6px; padding: 7px 6px; color: var(--text); font-size: .82rem; }
.app-multi-select-option:hover { background: var(--surface-hover-soft); }
.app-multi-select-option input {
  flex: 0 0 16px;
  width: 16px;
  min-width: 16px;
  height: 16px;
  margin: 1px 0 0;
  padding: 0;
  accent-color: var(--control-primary);
}
.app-multi-select-option-text { min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.app-multi-select-option-label { overflow-wrap: anywhere; }
.app-multi-select-option small { color: var(--muted); font-size: .7rem; line-height: 1.3; overflow-wrap: anywhere; }
.app-multi-select-empty { margin: 8px 6px; color: var(--muted); font-size: .82rem; }
.app-multi-select--descriptive .app-multi-select-menu { width: max(100%, 380px); }
.app-multi-select--descriptive .app-multi-select-option-label {
  overflow: hidden;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (max-width: 720px) {
  .app-multi-select--descriptive .app-multi-select-menu { width: 100%; max-width: 100%; }
}

@media (max-width: 460px) {
  .app-multi-select-menu > input[type="search"] { font-size: 16px; }
}
</style>
