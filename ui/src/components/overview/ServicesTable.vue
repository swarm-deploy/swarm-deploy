<script setup lang="ts">
import { useRouter } from "vue-router";
import type { ServiceInfo, ServiceSyncStatus } from "../../api/types";
import AppTable from "../common/AppTable.vue";

withDefaults(defineProps<{ services: ServiceInfo[]; minWidth?: string }>(), { minWidth: "720px" });
const router = useRouter();
async function openServiceDetails(stack: string, service: string) {
  if (stack && service) await router.push({ name: "service-details", params: { stack, service } });
}

function syncStatusClass(status: ServiceSyncStatus | string | undefined): string {
  const normalizedStatus = String(status || "").trim().toLowerCase();
  if (normalizedStatus === "synced") {
    return "success";
  }
  if (normalizedStatus === "outofsync") {
    return "failed";
  }

  return "unknown";
}

function syncStatusLabel(status: ServiceSyncStatus | string | undefined): string {
  const normalizedStatus = String(status || "").trim().toLowerCase();
  if (normalizedStatus === "synced") {
    return "Synced";
  }
  if (normalizedStatus === "outofsync") {
    return "OutOfSync";
  }

  return "unknown";
}

function syncErrorText(syncError: string | undefined): string {
  return String(syncError || "").trim();
}
</script>

<template>
  <AppTable fixed table-class="services-stack-table" :min-width="minWidth" wrap-class="stack-services-table-wrap">
    <template #colgroup>
      <colgroup>
        <col class="services-col-name" />
        <col class="services-col-sync-status" />
        <col class="services-col-type" />
        <col class="services-col-version" />
      </colgroup>
    </template>
    <template #head>
      <tr>
        <th>Name</th>
        <th>Sync Status</th>
        <th>Type</th>
        <th>Version</th>
      </tr>
    </template>
    <tr v-if="services.length === 0">
      <td colspan="4" class="app-table-empty-cell">No services captured yet.</td>
    </tr>
    <tr
      v-for="service in services"
      :key="`${service.stack}-${service.name}`"
      class="app-table-row--clickable"
      tabindex="0"
      role="button"
      :aria-label="`Open details for ${service.name || 'service'}`"
      @click="openServiceDetails(service.stack, service.name)"
      @keydown.enter="openServiceDetails(service.stack, service.name)"
      @keydown.space.prevent="openServiceDetails(service.stack, service.name)"
    >
      <td class="services-cell-name" :title="service.name || undefined">
        <strong class="stack-service-name">{{ service.name || "unknown" }}</strong>
      </td>
      <td class="services-cell-sync-status">
        <div class="stack-service-sync-status">
          <span class="status" :class="syncStatusClass(service.sync_status)">
            {{ syncStatusLabel(service.sync_status) }}
          </span>
          <p
            v-if="syncErrorText(service.sync_error)"
            class="stack-service-sync-error"
            :title="syncErrorText(service.sync_error)"
          >
            {{ syncErrorText(service.sync_error) }}
          </p>
        </div>
      </td>
      <td class="services-cell-type">{{ service.type_title || service.type }}</td>
      <td class="services-cell-version" :title="service.image">{{ service.image_version || "—" }}</td>
    </tr>
  </AppTable>
</template>
