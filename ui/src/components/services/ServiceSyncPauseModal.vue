<script setup lang="ts">
import { syncPauseDurationOptions } from "../../utils/syncPause";
import { t } from "../../i18n";
import { computed, onMounted, ref } from "vue";

import { expirationForDuration, getServiceSyncPause, removeServiceSyncPause, setServiceSyncPause } from "../../api/syncPauses";
import type { ServiceSyncPauseResponse } from "../../api/types";
import AppButton from "../common/AppButton.vue";
import AppModal from "../common/AppModal.vue";
import AppSelect from "../common/AppSelect.vue";
import { formatDate } from "../../utils/format";

const props = defineProps<{ stack: string; service: string }>();
const emit = defineEmits<{ close: [] }>();
const state = ref<ServiceSyncPauseResponse>();
const duration = ref("1");
const loading = ref(true);
const pending = ref(false);
const error = ref("");
const options = syncPauseDurationOptions();
const servicePause = computed(() => state.value?.service_pause);

onMounted(async () => {
  try {
    state.value = await getServiceSyncPause(props.stack, props.service);
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : t("syncPause.loadFailed");
  } finally {
    loading.value = false;
  }
});

async function submit() {
  if (!state.value || pending.value) return;
  pending.value = true;
  error.value = "";
  try {
    if (servicePause.value) {
      await removeServiceSyncPause(props.stack, props.service);
    } else {
      await setServiceSyncPause(props.stack, props.service, expirationForDuration(duration.value));
    }
    emit("close");
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : t("syncPause.updateFailed");
  } finally {
    pending.value = false;
  }
}
</script>

<template>
  <AppModal
    open
    labelledby="service-pause-title"
    card-class="service-pause-modal-card"
    as="form"
    :busy="pending"
    :close-disabled="pending"
    @submit.prevent="submit"
    @close="emit('close')"
  >
    <template #title><h2 id="service-pause-title">{{ t('syncPause.title', { resource: service }) }}</h2></template>
    <div class="modal-body service-pause-modal-body">
      <p v-if="loading" class="meta" role="status">{{ t('syncPause.loading') }}</p>
      <p v-if="state?.stack_pause" class="meta">{{ t('syncPause.stackPauseApplies') }}</p>
      <p v-if="servicePause">{{ servicePause.expires_at ? t('syncPause.serviceExpires', { expiration: formatDate(servicePause.expires_at) }) : t('syncPause.serviceIndefinite') }}</p>
      <AppSelect v-else-if="state" v-model="duration" :options="options" :label="t('syncPause.pauseDuration')" />
      <p v-if="error" role="alert" class="error">{{ error }}</p>
      <div class="service-pause-actions">
        <AppButton variant="secondary" type="button" :disabled="pending" @click="emit('close')">{{ t('common.cancel') }}</AppButton>
        <AppButton type="submit" :disabled="!state || pending">{{ servicePause ? t('syncPause.removeServicePause') : t('common.pauseSync') }}</AppButton>
      </div>
    </div>
  </AppModal>
</template>

<style scoped>
:deep(.service-pause-modal-card) {
  width: min(680px, 94vw);
}

.service-pause-modal-body {
  gap: 14px;
}

.service-pause-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
}
</style>
