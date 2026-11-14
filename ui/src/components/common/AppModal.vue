<script lang="ts">
import { shallowRef } from "vue";

// The last opened dialog owns keyboard input, including dialogs nested over another modal.
const modalStack = shallowRef<symbol[]>([]);
</script>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";

const props = withDefaults(defineProps<{
  open: boolean;
  labelledby: string;
  cardClass?: string | Record<string, boolean> | (string | Record<string, boolean>)[];
  headerClass?: string;
  closeClass?: string;
  closeLabel?: string;
  closeDisabled?: boolean;
  closeOnEscape?: boolean;
  keepMounted?: boolean;
  as?: "div" | "form";
  busy?: boolean;
  initialFocus?: string;
}>(), { closeLabel: "Close modal", closeOnEscape: true, as: "div" });

const emit = defineEmits<{ close: []; escape: [event: KeyboardEvent] }>();
const dialog = ref<HTMLElement | null>(null);
const closeButton = ref<HTMLButtonElement | null>(null);
const id = Symbol("modal");
const topmost = computed(() => modalStack.value.at(-1) === id);
const inactive = computed(() => props.open && !topmost.value);
let returnFocus: HTMLElement | null = null;
let disposed = false;
const focusableSelector = 'a[href], button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), summary, [tabindex]:not([tabindex="-1"])';

function close() {
  if (props.open && topmost.value && !props.closeDisabled) emit("close");
}

function onKeydown(event: KeyboardEvent) {
  if (!props.open || !topmost.value || event.defaultPrevented) return;
  if (event.key === "Escape") {
    const shouldClose = props.closeOnEscape;
    emit("escape", event);
    event.stopImmediatePropagation();
    if (event.defaultPrevented) return;
    event.preventDefault();
    if (shouldClose) close();
  } else if (event.key === "Tab") {
    const controls = [...(dialog.value?.querySelectorAll<HTMLElement>(focusableSelector) ?? [])]
      .filter((element) =>
        !element.closest('[hidden], .hidden, [inert], [aria-hidden="true"]') &&
        (typeof element.checkVisibility !== "function" || element.checkVisibility()),
      );
    const index = controls.indexOf(document.activeElement as HTMLElement);
    if (index === -1 || (event.shiftKey && index === 0) || (!event.shiftKey && index === controls.length - 1)) {
      event.preventDefault();
      (event.shiftKey ? controls.at(-1) : controls[0])?.focus();
      if (!controls.length) dialog.value?.focus();
    }
  }
}

function deactivate() {
  const wasTopmost = topmost.value;
  modalStack.value = modalStack.value.filter((entry) => entry !== id);
  const target = returnFocus;
  returnFocus = null;
  if (wasTopmost && target?.isConnected) {
    // Wait for the underlying dialog to become interactive again when closing a nested modal.
    if (target.closest("[inert]")) void nextTick(() => target.isConnected && target.focus());
    else target.focus();
  }
}

watch(() => props.open, async (open) => {
  if (!open) { deactivate(); return; }
  returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  modalStack.value = [...modalStack.value.filter((entry) => entry !== id), id];
  await nextTick();
  if (disposed || !props.open || !topmost.value) return;
  (props.initialFocus ? dialog.value?.querySelector<HTMLElement>(props.initialFocus) : closeButton.value)?.focus();
  if (!dialog.value?.contains(document.activeElement)) dialog.value?.focus();
}, { immediate: true });

onMounted(() => document.addEventListener("keydown", onKeydown, true));
onBeforeUnmount(() => {
  disposed = true;
  document.removeEventListener("keydown", onKeydown, true);
  deactivate();
});

defineExpose({ forgetReturnFocus: () => { returnFocus = null; } });
</script>

<template>
  <div v-if="open || keepMounted" class="modal" :class="{ hidden: !open }" :aria-hidden="!open || inactive" :inert="inactive || undefined">
    <div class="modal-overlay" @click="close" />
    <component :is="as" ref="dialog" class="modal-card" :class="cardClass" role="dialog" aria-modal="true" :aria-labelledby="labelledby" :aria-busy="busy" tabindex="-1">
      <div :class="$slots.title ? ['modal-header', headerClass] : 'modal-headerless'">
        <slot name="title" />
        <slot name="actions" />
        <button ref="closeButton" class="modal-close" :class="closeClass" type="button" :aria-label="closeLabel" :disabled="closeDisabled" @click="close">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" aria-hidden="true"><path d="m7 7 10 10M17 7 7 17" /></svg>
        </button>
      </div>
      <slot />
    </component>
  </div>
</template>

<style scoped>
.modal-headerless { display: contents; }
</style>
