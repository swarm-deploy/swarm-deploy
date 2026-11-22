<script setup lang="ts">
import { t } from "../i18n";
import { computed, ref, watch } from "vue";
import { useRoute } from "vue-router";

import { fetchServiceStatus } from "../api/overview";
import type { ServiceSpecResponse } from "../api/types";
import AppCopyButton from "../components/common/AppCopyButton.vue";
import AppTable from "../components/common/AppTable.vue";
import AppTableEmpty from "../components/common/AppTableEmpty.vue";

const route = useRoute();
const stack = computed(() => String(route.params.stack ?? ""));
const service = computed(() => String(route.params.service ?? ""));
const configID = computed(() => String(route.params.config ?? ""));
const sourceRoute = computed(() => ({ name: "service-details", params: { stack: stack.value, service: service.value } }));
const config = ref<NonNullable<ServiceSpecResponse["configs"]>[number]>();
const loading = ref(true);
const error = ref("");

watch([stack, service, configID, () => route.query.target], async ([stackName, serviceName, id, target], _, onCleanup) => {
  let current = true;
  onCleanup(() => { current = false; });
  config.value = undefined;
  loading.value = true;
  error.value = "";
  try {
    const response = await fetchServiceStatus(stackName, serviceName);
    if (!current) return;
    config.value = response.spec.configs?.find((item) =>
      (item.config_id || item.config_name) === id && (typeof target !== "string" || item.target === target));
    if (!config.value) error.value = t("service.configNotFound");
  } catch (reason) {
    if (current) error.value = reason instanceof Error ? reason.message : t("service.loadConfigFailed");
  } finally {
    if (current) loading.value = false;
  }
}, { immediate: true });
</script>

<template>
  <section class="services-page">
    <header>
      <RouterLink :to="sourceRoute" class="assistant-md-link">{{ stack }}/{{ service }}</RouterLink>
      <h2>{{ t('service.configDetails') }}</h2>
    </header>
    <AppTableEmpty v-if="loading" :message="t('service.loadingConfig')" />
    <AppTableEmpty v-else-if="error" role="alert">{{ error }}</AppTableEmpty>
    <AppTable v-else-if="config" summary fixed :aria-label="t('service.configDetails')">
      <tr><th scope="row">{{ t('common.name') }}</th><td>{{ config.config_name }}</td></tr>
      <tr>
        <th scope="row">ID</th>
        <td>
          <code>{{ config.config_id || t('common.notApplicable') }}</code>
          <AppCopyButton v-if="config.config_id" :value="config.config_id" color="var(--text)" :label="t('service.copyConfigId')" />
        </td>
      </tr>
      <tr><th scope="row">{{ t('service.mountPath') }}</th><td>{{ config.target || t('common.notApplicable') }}</td></tr>
    </AppTable>
  </section>
</template>
