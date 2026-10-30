<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";

const props = withDefaults(defineProps<{
  modelValue: string;
  disabled?: boolean;
  required?: boolean;
  placeholder?: string;
}>(), {
  disabled: false,
  required: false,
  placeholder: "Path to secret",
});

const emit = defineEmits<{
  "update:modelValue": [value: string];
}>();

const root = ref<HTMLElement | null>(null);
const parts = ref(props.modelValue.split("/"));
const activeIndex = ref(Math.max(0, parts.value.length - 1));
const activeValue = computed(() => parts.value[activeIndex.value] ?? "");
const canonicalValue = computed(() => parts.value.join("/"));

function focusInput(select = false): void {
  void nextTick(() => {
    const currentInput = root.value?.querySelector<HTMLInputElement>('input[aria-label="Path"]');
    currentInput?.focus();
    if (select) {
      currentInput?.select();
      return;
    }
    const position = currentInput?.value.length ?? 0;
    currentInput?.setSelectionRange(position, position);
  });
}

function commit(nextParts: string[], nextActiveIndex: number): void {
  parts.value = nextParts.length > 0 ? nextParts : [""];
  activeIndex.value = Math.min(Math.max(0, nextActiveIndex), parts.value.length - 1);
  emit("update:modelValue", canonicalValue.value);
}

function updateValue(event: Event): void {
  const nextValue = (event.currentTarget as HTMLInputElement).value;
  const replacements = nextValue.split("/");
  const nextParts = [...parts.value];
  nextParts.splice(activeIndex.value, 1, ...replacements);
  const nextActiveIndex = activeIndex.value + replacements.length - 1;
  commit(nextParts, nextActiveIndex);
  if (replacements.length > 1) focusInput();
}

function editSegment(index: number): void {
  if (props.disabled || index === activeIndex.value) return;
  activeIndex.value = index;
  focusInput(true);
}

function handleBackspace(event: KeyboardEvent): void {
  const currentInput = event.currentTarget as HTMLInputElement;
  if (
    event.key !== "Backspace"
    || activeValue.value !== ""
    || currentInput.selectionStart !== 0
    || currentInput.selectionEnd !== 0
    || activeIndex.value === 0
  ) return;

  event.preventDefault();
  const nextParts = [...parts.value];
  nextParts.splice(activeIndex.value, 1);
  commit(nextParts, activeIndex.value - 1);
  focusInput();
}

watch(() => props.modelValue, (value) => {
  if (value === canonicalValue.value) return;
  parts.value = value.split("/");
  activeIndex.value = Math.max(0, parts.value.length - 1);
});
</script>

<template>
  <div ref="root" class="create-secret-control path-input" :class="{ 'path-input--disabled': disabled }" @click.self="focusInput()">
    <span class="path-input-icon" title="Path" aria-hidden="true">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><path d="M3.5 6.5h6l2 2h9v10h-17z" /></svg>
    </span>
    <span class="path-input-content">
      <template v-for="(part, index) in parts" :key="index">
        <input
          v-if="index === activeIndex"
          :value="part"
          class="path-input-editor"
          :class="{ 'path-input-editor--tail': index === parts.length - 1 }"
          :style="{ width: `${Math.max(part.length, 1)}ch` }"
          type="text"
          autocomplete="off"
          spellcheck="false"
          aria-label="Path"
          :placeholder="parts.length === 1 ? placeholder : ''"
          :disabled="disabled"
          :required="required && canonicalValue === ''"
          @input="updateValue"
          @keydown="handleBackspace"
        />
        <button
          v-else
          type="button"
          class="path-input-segment"
          :aria-label="`Edit path segment ${part || 'empty'}`"
          :disabled="disabled"
          @click="editSegment(index)"
        >{{ part }}</button>
        <span v-if="index < parts.length - 1" class="path-input-separator" aria-hidden="true">/</span>
      </template>
    </span>
  </div>
</template>

<style scoped>
.path-input--disabled { opacity: .58; }
.path-input-icon { flex: 0 0 auto; width: 42px; display: grid; place-items: center; color: var(--muted); }
.path-input-icon svg { width: 19px; height: 19px; }
.path-input-content { min-width: 0; height: 100%; display: flex; flex: 1; align-items: center; overflow-x: auto; padding-right: 11px; scrollbar-width: none; white-space: nowrap; }
.path-input-content::-webkit-scrollbar { display: none; }
.path-input-editor { min-width: 56px; max-width: 100%; height: 100%; flex: 0 0 auto; border: 0; outline: 0; background: transparent; color: var(--text); padding: 0; font: inherit; font-size: .84rem; }
.path-input-editor--tail { flex-grow: 1; }
.path-input-editor::placeholder { color: var(--muted); opacity: .88; }
.path-input-segment { flex: 0 0 auto; border: 0; border-radius: 2px; background: transparent; color: var(--control-primary); padding: 0; font: inherit; font-size: .84rem; transform: none; filter: none; }
.path-input-segment:hover:not(:disabled) { color: var(--control-primary-hover); text-decoration: underline; background: transparent; transform: none; filter: none; }
.path-input-segment:focus-visible { outline: 0; text-decoration: underline; }
.path-input-segment:disabled { cursor: not-allowed; }
.path-input-separator { flex: 0 0 auto; margin: 0 7px; color: var(--muted); font-size: .84rem; }
</style>
