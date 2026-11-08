<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from "vue";

import { copyText } from "../../utils/clipboard";

const props = defineProps<{
  value: string | null;
  label: string;
  title?: string;
  copiedLabel?: string;
  color?: string;
}>();

const copied = ref(false);
let resetTimer: ReturnType<typeof setTimeout> | undefined;
let attempt = 0;

function resetCopied(): void {
  attempt++;
  clearTimeout(resetTimer);
  copied.value = false;
}

async function copyValue(): Promise<void> {
  const value = props.value;
  if (value === null) return;
  const currentAttempt = ++attempt;
  await copyText(value);
  if (currentAttempt !== attempt) return;

  clearTimeout(resetTimer);
  copied.value = true;
  resetTimer = setTimeout(() => { copied.value = false; }, 1600);
}

watch(() => props.value, resetCopied);
onBeforeUnmount(resetCopied);
</script>

<template>
  <button
    type="button"
    class="app-copy-button"
    :style="{ color }"
    :disabled="value === null"
    :aria-label="copied ? (copiedLabel ?? 'Copied') : label"
    :title="copied ? 'Copied' : (title ?? label)"
    @click="copyValue"
  >
    <svg v-if="copied" class="app-copy-button-check" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path d="m5 12.5 4.2 4.2L19 7" />
    </svg>
    <svg v-else viewBox="0 0 16 16" aria-hidden="true" focusable="false">
      <path
        d="M5 1.5A1.5 1.5 0 0 0 3.5 3v7A1.5 1.5 0 0 0 5 11.5h7A1.5 1.5 0 0 0 13.5 10V3A1.5 1.5 0 0 0 12 1.5H5Zm0 1h7a.5.5 0 0 1 .5.5v7a.5.5 0 0 1-.5.5H5a.5.5 0 0 1-.5-.5V3a.5.5 0 0 1 .5-.5Z"
        fill="currentColor"
      />
      <path
        d="M2 4.5a.5.5 0 0 1 .5.5V12A1.5 1.5 0 0 0 4 13.5h7a.5.5 0 0 1 0 1H4A2.5 2.5 0 0 1 1.5 12V5a.5.5 0 0 1 .5-.5Z"
        fill="currentColor"
      />
    </svg>
  </button>
</template>

<style scoped>
.app-copy-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: none;
  width: 28px;
  height: 28px;
  padding: 0;
  border: 1px solid transparent;
  border-radius: 4px;
  background: transparent;
  color: var(--control-primary);
  transform: none;
  filter: none;
}

.app-copy-button:hover:not(:disabled) {
  background: var(--surface-hover);
}

.app-copy-button:focus-visible {
  outline: 2px solid var(--accent-focus);
  outline-offset: 1px;
}

.app-copy-button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.app-copy-button svg {
  width: 14px;
  height: 14px;
}

.app-copy-button-check {
  fill: none;
  stroke: var(--success);
  stroke-width: 2;
  stroke-linecap: round;
  stroke-linejoin: round;
}
</style>
