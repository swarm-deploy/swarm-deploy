<script setup lang="ts">
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
    if (!config.value) error.value = "Config attachment not found.";
  } catch (reason) {
    if (current) error.value = reason instanceof Error ? reason.message : "Failed to load config attachment";
  } finally {
    if (current) loading.value = false;
  }
}, { immediate: true });
</script>

<template>
  <section class="services-page">
    <header>
      <RouterLink :to="sourceRoute" class="assistant-md-link">{{ stack }}/{{ service }}</RouterLink>
      <h2>Config details</h2>
    </header>
    <AppTableEmpty v-if="loading" message="Loading config details..." />
    <AppTableEmpty v-else-if="error" role="alert">{{ error }}</AppTableEmpty>
    <AppTable v-else-if="config" summary fixed aria-label="Config details">
      <tr><th scope="row">Name</th><td>{{ config.config_name }}</td></tr>
      <tr>
        <th scope="row">ID</th>
        <td>
          <code>{{ config.config_id || "n/a" }}</code>
          <AppCopyButton v-if="config.config_id" :value="config.config_id" color="var(--text)" label="Copy config ID" />
        </td>
      </tr>
      <tr><th scope="row">Mount path</th><td>{{ config.target || "n/a" }}</td></tr>
    </AppTable>
  </section>
</template>
