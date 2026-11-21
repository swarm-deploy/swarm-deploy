<script setup lang="ts">
import { computed, onMounted, ref } from "vue";

import { expirationForDuration, getServiceSyncPause, removeServiceSyncPause, setServiceSyncPause, syncPauseDurations } from "../../api/syncPauses";
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
const options: { value: string; label: string }[] = syncPauseDurations.map(({ value, label }) => ({ value, label }));
const servicePause = computed(() => state.value?.service_pause);

onMounted(async () => {
  try {
    state.value = await getServiceSyncPause(props.stack, props.service);
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : "Failed to load Pause Sync state";
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
    error.value = reason instanceof Error ? reason.message : "Failed to update Pause Sync";
  } finally {
    pending.value = false;
  }
}
</script>

<template>
  <AppModal open labelledby="service-pause-title" as="form" :busy="pending" :close-disabled="pending"
    @submit.prevent="submit" @close="emit('close')">
    <template #title><h2 id="service-pause-title">Pause Sync — {{ service }}</h2></template>
    <div class="modal-body">
      <p v-if="loading" class="meta" role="status">Loading Pause Sync state...</p>
      <p v-if="state?.stack_pause" class="meta">A stack-level pause also applies to this service.</p>
      <p v-if="servicePause">Service pause expires {{ servicePause.expires_at ? formatDate(servicePause.expires_at) : "never" }}.</p>
      <AppSelect v-else-if="state" v-model="duration" :options="options" label="Pause duration" />
      <p v-if="error" role="alert" class="error">{{ error }}</p>
    </div>
    <div class="modal-footer">
      <AppButton type="submit" :disabled="!state || pending">{{ servicePause ? "Remove service pause" : "Pause sync" }}</AppButton>
    </div>
  </AppModal>
</template>
