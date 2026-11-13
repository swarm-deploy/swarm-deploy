<script setup lang="ts">
import { computed, ref, watch } from "vue";

import {
  expirationForDuration,
  getStackSyncPauses,
  resumeStackSync,
  setStackSyncPause,
  syncPauseDurations,
} from "../../api/syncPauses";
import type { StackSyncPausesResponse } from "../../api/types";
import { formatDate } from "../../utils/format";
import AppButton from "../common/AppButton.vue";
import AppModal from "../common/AppModal.vue";
import AppSelect from "../common/AppSelect.vue";

const props = defineProps<{ open: boolean; stack: string }>();
const emit = defineEmits<{ close: []; changed: [] }>();
const state = ref<StackSyncPausesResponse | null>(null);
const duration = ref("1");
const loading = ref(false);
const pending = ref(false);
const error = ref("");
const hasStackPause = computed(() => Boolean(state.value?.stack_pause));
const canSubmit = computed(() => state.value !== null && !loading.value && !pending.value && !error.value);
const durationOptions: { value: string; label: string }[] = syncPauseDurations.map(({ value, label }) => ({ value, label }));
let loadRequest = 0;

async function load(): Promise<void> {
  if (!props.open || !props.stack) return;
  const request = ++loadRequest;
  const stack = props.stack;
  loading.value = true;
  state.value = null;
  error.value = "";
  try {
    const response = await getStackSyncPauses(stack);
    if (request !== loadRequest || !props.open || props.stack !== stack) return;
    state.value = response;
  } catch (reason) {
    if (request !== loadRequest || !props.open || props.stack !== stack) return;
    error.value = reason instanceof Error ? reason.message : "Failed to load Pause Sync state";
  } finally {
    if (request === loadRequest && props.open && props.stack === stack) loading.value = false;
  }
}

async function submit(): Promise<void> {
  if (!canSubmit.value) return;
  pending.value = true;
  error.value = "";
  try {
    if (hasStackPause.value) {
      await resumeStackSync(props.stack);
    } else {
      await setStackSyncPause(props.stack, expirationForDuration(duration.value));
    }
    emit("changed");
    await load();
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : "Failed to update Pause Sync";
  } finally {
    pending.value = false;
  }
}

function pauseUntil(expiresAt: string | null): string {
  return expiresAt ? formatDate(expiresAt) : "No expiration";
}

watch(() => [props.open, props.stack] as const, ([open]) => {
  if (open) {
    void load();
    return;
  }
  loadRequest++;
  loading.value = false;
  state.value = null;
}, { immediate: true });
</script>

<template>
  <AppModal
    :open="open"
    labelledby="sync-pause-title"
    card-class="sync-pause-modal-card"
    as="form"
    :busy="pending"
    :close-disabled="pending"
    @submit.prevent="submit"
    @close="emit('close')"
  >
    <template #title><h2 id="sync-pause-title">Pause Sync — {{ stack }}</h2></template>
    <div class="modal-body sync-pause-modal-body">
      <p v-if="loading" class="meta" role="status">Loading Pause Sync state...</p>
      <template v-else>
        <template v-if="hasStackPause">
          <dl class="sync-pause-summary">
            <dt>Stack pause</dt><dd>Active</dd>
            <dt>Pause until</dt><dd>{{ pauseUntil(state?.stack_pause?.expires_at ?? null) }}</dd>
            <dt>Paused services</dt><dd>{{ state?.effectively_paused_services ?? 0 }}</dd>
          </dl>
          <p class="form-helper" role="note">Resuming sync lets GitOps reconcile every service in this stack again.</p>
        </template>
        <template v-else>
          <label class="sync-pause-duration-label">Duration</label>
          <AppSelect v-model="duration" :options="durationOptions" label="Pause duration" />
          <p class="form-helper" role="note">Pause Sync preserves every service in the stack during GitOps reconciliation.</p>
        </template>
        <p v-if="error" class="sync-pause-error" role="alert">{{ error }}</p>
        <div class="sync-pause-actions">
          <AppButton variant="secondary" type="button" :disabled="pending" @click="emit('close')">Cancel</AppButton>
          <AppButton type="submit" :disabled="!canSubmit">{{ hasStackPause ? "Resume stack sync" : "Pause entire stack" }}</AppButton>
        </div>
      </template>
    </div>
  </AppModal>
</template>

<style scoped>
:deep(.sync-pause-modal-card) { width: min(680px, 94vw); }
.sync-pause-modal-body { display: grid; gap: 14px; }
.sync-pause-summary { display: grid; grid-template-columns: auto 1fr; gap: 6px 16px; margin: 0; font-size: .86rem; }
.sync-pause-summary dt { color: var(--muted); }
.sync-pause-summary dd { margin: 0; }
.sync-pause-duration-label { margin: 0; font-size: .82rem; }
.sync-pause-error { margin: 0; color: var(--error); }
.sync-pause-actions { display: flex; justify-content: flex-end; gap: 8px; }
</style>
