<script setup lang="ts">
import { computed, ref, watch } from "vue";

import {
  expirationForDuration,
  getStackSyncPauses,
  removeServiceSyncPause,
  resumeStackSync,
  setStackSyncPause,
  syncPauseDurations,
} from "../../api/syncPauses";
import type { StackSyncPausesResponse } from "../../api/types";
import { formatDate } from "../../utils/format";
import AppButton from "../common/AppButton.vue";
import AppModal from "../common/AppModal.vue";
import AppSelect from "../common/AppSelect.vue";
import AppTable from "../common/AppTable.vue";

const props = defineProps<{ open: boolean; stack: string }>();
const emit = defineEmits<{ close: []; changed: [] }>();
const state = ref<StackSyncPausesResponse | null>(null);
const duration = ref("1");
const loading = ref(false);
const pending = ref(false);
const error = ref("");
const notice = ref("");
const hasStackPause = computed(() => Boolean(state.value?.stack_pause));
const servicePauses = computed(() => state.value?.service_pauses ?? []);
const durationOptions: { value: string; label: string }[] = syncPauseDurations.map(({ value, label }) => ({ value, label }));

async function load(): Promise<void> {
  if (!props.open || !props.stack) return;
  loading.value = true;
  error.value = "";
  notice.value = "";
  try {
    state.value = await getStackSyncPauses(props.stack);
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : "Failed to load Pause Sync state";
  } finally {
    loading.value = false;
  }
}

async function submit(): Promise<void> {
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

async function removeService(service: string): Promise<void> {
  pending.value = true;
  error.value = "";
  notice.value = "";
  try {
    await removeServiceSyncPause(props.stack, service);
    notice.value = hasStackPause.value
      ? "Service pause removed. The service remains paused by the stack-level pause."
      : "Service pause removed.";
    emit("changed");
    state.value = await getStackSyncPauses(props.stack);
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : "Failed to remove service pause";
  } finally {
    pending.value = false;
  }
}

function pauseUntil(expiresAt: string | null): string {
  return expiresAt ? formatDate(expiresAt) : "No expiration";
}

watch(() => [props.open, props.stack], () => { if (props.open) void load(); }, { immediate: true });
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
          </dl>
          <p class="form-helper" role="note">Removing the stack pause does not remove independent service pauses. Listed services remain paused.</p>
        </template>
        <template v-else>
          <label class="sync-pause-duration-label">Duration</label>
          <AppSelect v-model="duration" :options="durationOptions" label="Pause duration" />
          <p class="form-helper" role="note">Independent service pauses remain active when the entire stack is paused.</p>
        </template>

        <section v-if="servicePauses.length" aria-labelledby="service-pauses-title">
          <h3 id="service-pauses-title">Direct service pauses</h3>
          <AppTable fixed min-width="520px" aria-label="Direct service pauses">
            <template #head><tr><th>Service</th><th>Pause until</th><th><span class="sr-only">Actions</span></th></tr></template>
            <tr v-for="pause in servicePauses" :key="pause.service">
              <td>{{ pause.service }}</td>
              <td>{{ pauseUntil(pause.expires_at) }}</td>
              <td class="sync-pause-table-action"><AppButton variant="secondary" compact type="button" :disabled="pending" @click="removeService(pause.service ?? '')">Remove service pause</AppButton></td>
            </tr>
          </AppTable>
        </section>
        <p v-if="notice" class="sync-pause-notice" role="status">{{ notice }}</p>
        <p v-if="error" class="sync-pause-error" role="alert">{{ error }}</p>
        <div class="sync-pause-actions">
          <AppButton variant="secondary" type="button" :disabled="pending" @click="emit('close')">Cancel</AppButton>
          <AppButton type="submit" :disabled="pending">{{ hasStackPause ? "Resume stack sync" : "Pause entire stack" }}</AppButton>
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
.sync-pause-duration-label, h3 { margin: 0; font-size: .82rem; }
.sync-pause-table-action { text-align: right; }
.sync-pause-notice { margin: 0; color: var(--muted); }
.sync-pause-error { margin: 0; color: var(--error); }
.sync-pause-actions { display: flex; justify-content: flex-end; gap: 8px; }
</style>
