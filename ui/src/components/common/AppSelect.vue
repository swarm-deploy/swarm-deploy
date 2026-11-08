<script setup lang="ts" generic="T extends string | number, Multiple extends boolean = false">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId } from "vue";

interface AppSelectOption {
  value: T;
  label: string;
  description?: string;
  searchText?: string;
}

type ModelValue = Multiple extends true ? T[] : T;

const props = withDefaults(defineProps<{
  modelValue: ModelValue;
  multiple?: Multiple & boolean;
  options: AppSelectOption[];
  label: string;
  placeholder?: string;
  heading?: string;
  selectionLabel?: string;
  searchable?: boolean;
  searchLabel?: string;
  searchPlaceholder?: string;
  emptyMessage?: string;
  descriptive?: boolean;
}>(), {
  placeholder: "",
  selectionLabel: "",
  searchable: false,
  searchLabel: "Search options",
  searchPlaceholder: "Search",
  emptyMessage: "No matching options",
  descriptive: false,
});

const emit = defineEmits<{ "update:modelValue": [value: ModelValue] }>();
const root = ref<HTMLDetailsElement | null>(null);
const trigger = ref<HTMLElement | null>(null);
const searchInput = ref<HTMLInputElement | null>(null);
const search = ref("");
const selectionId = useId();
const selectedValues = computed<T[]>(() => Array.isArray(props.modelValue) ? props.modelValue : [props.modelValue as T]);

const filteredOptions = computed(() => {
  const query = search.value.trim().toLocaleLowerCase();
  if (!query) return props.options;
  return props.options.filter((option) => (
    option.searchText ?? `${option.label} ${option.description ?? ""}`
  ).toLocaleLowerCase().includes(query));
});

const summary = computed(() => {
  if (props.selectionLabel) return props.selectionLabel;
  if (selectedValues.value.length === 0) return props.placeholder;
  const labels = selectedValues.value.map((value) => (
    props.options.find((option) => option.value === value)?.label ?? value
  ));
  return labels.length <= 2 ? labels.join(", ") : `${labels.length} selected`;
});

function select(value: T): void {
  if (!props.multiple) {
    if (props.modelValue !== value) emit("update:modelValue", value as ModelValue);
    dismiss();
    return;
  }

  const selected = selectedValues.value.includes(value)
    ? selectedValues.value.filter((item) => item !== value)
    : [...selectedValues.value, value];
  emit("update:modelValue", selected as ModelValue);
}

function clear(): void {
  const empty: T[] = [];
  emit("update:modelValue", empty as ModelValue);
}

function handleDocumentClick(event: MouseEvent): void {
  if (!(event.target instanceof Node) || root.value?.contains(event.target)) return;
  close();
}

function handleKeydown(event: KeyboardEvent): void {
  if (event.key === "Escape") {
    if (dismiss()) event.preventDefault();
    return;
  }
  const { key } = event;
  if (props.multiple || !root.value || event.target === searchInput.value) return;
  if (key !== "ArrowDown" && key !== "ArrowUp" && key !== "Home" && key !== "End") return;
  if (!root.value.open && (key === "Home" || key === "End")) return;

  const options = [...root.value.querySelectorAll<HTMLButtonElement>("button.app-select-option")];
  const current = options.findIndex((option) => option === event.target);
  const last = options.length - 1;
  let next: number;
  switch (key) {
    case "ArrowDown": next = Math.min(current + 1, last); break;
    case "ArrowUp": next = current < 0 ? last : Math.max(current - 1, 0); break;
    case "Home": next = 0; break;
    case "End": next = last; break;
  }

  event.preventDefault();
  root.value.open = true;
  options[next]?.focus();
}

function handleToggle(): void {
  if (!root.value?.open) {
    search.value = "";
    return;
  }
  if (props.searchable && (props.multiple || document.activeElement === trigger.value)) {
    void nextTick(() => {
      if (root.value?.open) searchInput.value?.focus();
    });
  }
}

function close(): void {
  if (root.value) root.value.open = false;
  search.value = "";
}

function dismiss(): boolean {
  if (!root.value?.open) return false;
  close();
  trigger.value?.focus();
  return true;
}

defineExpose({ dismiss });

onMounted(() => document.addEventListener("click", handleDocumentClick));
onBeforeUnmount(() => document.removeEventListener("click", handleDocumentClick));
</script>

<template>
  <details
    ref="root"
    class="app-select"
    :class="{ 'app-select--descriptive': descriptive }"
    @toggle="handleToggle"
    @keydown="handleKeydown"
  >
    <summary ref="trigger" :aria-label="label" :aria-describedby="selectionId">
      <span v-if="$slots.icon" class="app-select-icon" aria-hidden="true"><slot name="icon" /></span>
      <span :id="selectionId" class="app-select-selection" :title="summary">{{ summary }}</span>
      <svg class="app-select-chevron" viewBox="0 0 16 16" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
        <path d="m4 6 4 4 4-4" />
      </svg>
    </summary>
    <div class="app-select-menu">
      <div v-if="heading || multiple" class="app-select-header">
        <strong>{{ heading }}</strong>
        <button v-if="multiple" type="button" :disabled="selectedValues.length === 0" @click="clear">Clear</button>
      </div>
      <input
        v-if="searchable"
        ref="searchInput"
        v-model="search"
        type="search"
        :aria-label="searchLabel"
        :placeholder="searchPlaceholder"
      />
      <component
        :is="multiple ? 'label' : 'button'"
        v-for="option in filteredOptions"
        :key="option.value"
        class="app-select-option"
        :type="multiple ? undefined : 'button'"
        :aria-pressed="multiple ? undefined : selectedValues.includes(option.value)"
        @click="!multiple && select(option.value)"
      >
        <input
          v-if="multiple"
          type="checkbox"
          :value="option.value"
          :checked="selectedValues.includes(option.value)"
          @change="select(option.value)"
        />
        <svg v-else class="app-select-check" viewBox="0 0 16 16" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="m3 8 3 3 7-7" />
        </svg>
        <span class="app-select-option-text">
          <span class="app-select-option-label">{{ option.label }}</span>
          <small v-if="option.description">{{ option.description }}</small>
        </span>
      </component>
      <p v-if="filteredOptions.length === 0" class="app-select-empty">{{ emptyMessage }}</p>
    </div>
  </details>
</template>

<style scoped>
.app-select { position: relative; min-width: 0; }
.app-select > summary {
  box-sizing: border-box;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  width: 100%;
  height: var(--app-select-height, 38px);
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
.app-select > summary::-webkit-details-marker { display: none; }
.app-select > summary:focus-visible,
.app-select-menu input[type="search"]:focus-visible,
.app-select-header button:focus-visible,
.app-select-option:focus-visible,
.app-select-option:has(input:focus-visible) {
  outline: 2px solid var(--accent-focus);
  outline-offset: 2px;
}
.app-select-icon { display: grid; place-items: center; flex: none; }
.app-select-selection { flex: 1 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.app-select-chevron { flex: none; width: 14px; height: 14px; color: var(--muted); }
.app-select-menu {
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
.app-select-header { display: flex; align-items: center; justify-content: space-between; padding: 5px 6px 8px; }
.app-select-header button { border: 0; background: transparent; color: var(--control-primary); padding: 0; font: inherit; }
.app-select-header button:disabled { cursor: not-allowed; opacity: .55; }
.app-select-menu > input[type="search"] {
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
.app-select-option { display: flex; align-items: flex-start; gap: 9px; border-radius: 6px; padding: 7px 6px; color: var(--text); font-size: .82rem; }
button.app-select-option { width: 100%; border: 0; background: transparent; font-family: inherit; text-align: left; cursor: pointer; }
.app-select-option:hover { background: var(--surface-hover-soft); }
.app-select-check { flex: 0 0 16px; width: 16px; height: 16px; color: var(--control-primary); visibility: hidden; }
.app-select-option[aria-pressed="true"] .app-select-check { visibility: visible; }
.app-select-option input {
  flex: 0 0 16px;
  width: 16px;
  min-width: 16px;
  height: 16px;
  margin: 1px 0 0;
  padding: 0;
  accent-color: var(--control-primary);
}
.app-select-option-text { min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.app-select-option-label { overflow-wrap: anywhere; }
.app-select-option small { color: var(--muted); font-size: .7rem; line-height: 1.3; overflow-wrap: anywhere; }
.app-select-empty { margin: 8px 6px; color: var(--muted); font-size: .82rem; }
.app-select--descriptive .app-select-menu { width: max(100%, 380px); }
.app-select--descriptive .app-select-option-label {
  overflow: hidden;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (max-width: 720px) {
  .app-select--descriptive .app-select-menu { width: 100%; max-width: 100%; }
}

@media (max-width: 460px) {
  .app-select-menu > input[type="search"] { font-size: 16px; }
}
</style>
