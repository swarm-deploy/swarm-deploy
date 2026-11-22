<script setup lang="ts">
import { syncPauseDurationOptions } from "../../utils/syncPause";
import { t } from "../../i18n";
import { computed, ref, watch } from "vue";

import {
  expirationForDuration,
  getStackSyncPauses,
  removeServiceSyncPause,
  resumeStackSync,
  setStackSyncPause,
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
const canSubmit = computed(() => state.value !== null && !loading.value && !pending.value && !error.value);
const durationOptions = syncPauseDurationOptions();
let loadRequest = 0;

async function load(): Promise<void> {
  if (!props.open || !props.stack) return;
  const request = ++loadRequest;
  const stack = props.stack;
  loading.value = true;
  state.value = null;
  error.value = "";
  notice.value = "";
  try {
    const response = await getStackSyncPauses(stack);
    if (request !== loadRequest || !props.open || props.stack !== stack) return;
    state.value = response;
  } catch (reason) {
    if (request !== loadRequest || !props.open || props.stack !== stack) return;
    error.value = reason instanceof Error ? reason.message : t("syncPause.loadFailed");
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
    error.value = reason instanceof Error ? reason.message : t("syncPause.updateFailed");
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
      ? t("syncPause.servicePauseRemovedButStackPaused")
      : t("syncPause.servicePauseRemoved");
    emit("changed");
    state.value = await getStackSyncPauses(props.stack);
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : t("syncPause.removeServiceFailed");
  } finally {
    pending.value = false;
  }
}

function pauseUntil(expiresAt: string | null): string {
  return expiresAt ? formatDate(expiresAt) : t("syncPause.noExpiration");
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
    <template #title><h2 id="sync-pause-title">{{ t('syncPause.title', { resource: stack }) }}</h2></template>
    <div class="modal-body sync-pause-modal-body">
      <p v-if="loading" class="meta" role="status">{{ t('syncPause.loading') }}</p>
      <template v-else>
        <template v-if="hasStackPause">
          <dl class="sync-pause-summary">
            <dt>{{ t('syncPause.stackPause') }}</dt><dd>{{ t('syncPause.active') }}</dd>
            <dt>{{ t('syncPause.pauseUntil') }}</dt><dd>{{ pauseUntil(state?.stack_pause?.expires_at ?? null) }}</dd>
          </dl>
          <p class="form-helper" role="note">{{ t('syncPause.resumeStackHint') }}</p>
        </template>
        <template v-else>
          <label class="sync-pause-duration-label">{{ t('syncPause.duration') }}</label>
          <AppSelect v-model="duration" :options="durationOptions" :label="t('syncPause.pauseDuration')" />
          <p class="form-helper" role="note">{{ t('syncPause.pauseStackHint') }}</p>
        </template>

        <section v-if="servicePauses.length" aria-labelledby="service-pauses-title">
          <h3 id="service-pauses-title">{{ t('syncPause.directServicePauses') }}</h3>
          <AppTable fixed :aria-label="t('syncPause.directServicePauses')">
            <template #head><tr><th>{{ t('common.service') }}</th><th>{{ t('syncPause.pauseUntil') }}</th><th><span class="sr-only">{{ t('common.action') }}</span></th></tr></template>
            <tr v-for="pause in servicePauses" :key="pause.service">
              <td>{{ pause.service }}</td>
              <td>{{ pauseUntil(pause.expires_at) }}</td>
              <td class="sync-pause-table-action"><AppButton variant="secondary" compact type="button" :disabled="pending" @click="removeService(pause.service ?? '')">{{ t('syncPause.removeServicePause') }}</AppButton></td>
            </tr>
          </AppTable>
        </section>
        <p v-if="notice" class="sync-pause-notice" role="status">{{ notice }}</p>
        <p v-if="error" class="sync-pause-error" role="alert">{{ error }}</p>
        <div class="sync-pause-actions">
          <AppButton variant="secondary" type="button" :disabled="pending" @click="emit('close')">{{ t('common.cancel') }}</AppButton>
          <AppButton type="submit" :disabled="!canSubmit">{{ hasStackPause ? t('syncPause.resumeStack') : t('syncPause.pauseStack') }}</AppButton>
        </div>
      </template>
    </div>
  </AppModal>
</template>

<style scoped>
:deep(.sync-pause-modal-card) { width: min(680px, 94vw); }
.sync-pause-modal-body { display: grid; grid-template-columns: minmax(0, 1fr); gap: 14px; }
.sync-pause-summary { display: grid; grid-template-columns: auto 1fr; gap: 6px 16px; margin: 0; font-size: .86rem; }
.sync-pause-summary dt { color: var(--muted); }
.sync-pause-summary dd { margin: 0; }
.sync-pause-duration-label, h3 { margin: 0; font-size: .82rem; }
.sync-pause-modal-body :deep(.app-table td) { overflow-wrap: anywhere; }
.sync-pause-table-action { text-align: right; }
.sync-pause-table-action .app-button { white-space: normal; overflow-wrap: anywhere; }
.sync-pause-notice { margin: 0; color: var(--muted); }
.sync-pause-error { margin: 0; color: var(--error); }
.sync-pause-actions { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 8px; }
</style>
