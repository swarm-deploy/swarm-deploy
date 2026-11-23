<script setup lang="ts">
import { enumLabel, t } from "../../i18n";
import { useRouter } from "vue-router";
import type { ServiceInfo, ServiceSyncStatus } from "../../api/types";
import AppStatusBadge from "../common/AppStatusBadge.vue";
import AppTable from "../common/AppTable.vue";

const props = withDefaults(defineProps<{
  services: ServiceInfo[];
  minWidth?: string;
  variant?: "default" | "stack-detail";
  emptyMessage?: string;
}>(), {
  minWidth: "720px",
  variant: "default",
  emptyMessage: t("stack.noCapturedServices"),
});
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

function syncStatusVariant(status: ServiceSyncStatus | string | undefined): "success" | "error" | "neutral" {
  const normalizedStatus = String(status || "").trim().toLowerCase();
  if (normalizedStatus === "synced") {
    return "success";
  }
  if (normalizedStatus === "outofsync") {
    return "error";
  }

  return "neutral";
}

function syncStatusLabel(status: ServiceSyncStatus | string | undefined): string {
  const normalizedStatus = String(status || "").trim().toLowerCase();
  if (normalizedStatus === "synced") {
    return t("stack.synced");
  }
  if (normalizedStatus === "outofsync") {
    return t("stack.serviceOutOfSync");
  }

  return t("stack.serviceSyncUnknown");
}

function syncErrorText(syncError: string | undefined): string {
  return syncError ?? "";
}
</script>

<template>
  <AppTable fixed :table-class="props.variant === 'stack-detail' ? 'services-stack-table services-stack-table--detail' : 'services-stack-table'" :min-width="minWidth" wrap-class="stack-services-table-wrap">
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
        <th>{{ t('common.name') }}</th>
        <th>{{ variant === "stack-detail" ? t('common.version') : t('stack.syncStatus') }}</th>
        <th>{{ t('common.type') }}</th>
        <th>{{ variant === "stack-detail" ? t('common.status') : t('common.version') }}</th>
      </tr>
    </template>
    <tr v-if="services.length === 0">
      <td colspan="4" class="app-table-empty-cell">{{ emptyMessage }}</td>
    </tr>
    <tr
      v-for="service in services"
      :key="`${service.stack}-${service.name}`"
      class="app-table-row--clickable"
      tabindex="0"
      role="button"
      :aria-label="t('stack.openService', { service: service.name || t('common.service').toLocaleLowerCase() })"
      @click="openServiceDetails(service.stack, service.name)"
      @keydown.enter="openServiceDetails(service.stack, service.name)"
      @keydown.space.prevent="openServiceDetails(service.stack, service.name)"
    >
      <td class="services-cell-name" :title="service.name || undefined">
        <span class="stack-service-name-wrap">
          <strong class="stack-service-name">{{ service.name || t('stack.serviceSyncUnknown') }}</strong>
        </span>
      </td>
      <td v-if="variant === 'stack-detail'" class="services-cell-image" :title="service.image">{{ service.image_version || "—" }}</td>
      <td v-else class="services-cell-sync-status">
        <div class="stack-service-sync-status">
          <AppStatusBadge
            class="status"
            :class="syncStatusClass(service.sync_status)"
            :variant="syncStatusVariant(service.sync_status)"
          >
            {{ syncStatusLabel(service.sync_status) }}
          </AppStatusBadge>
          <p
            v-if="syncErrorText(service.sync_error)"
            class="stack-service-sync-error"
            :title="syncErrorText(service.sync_error)"
          >
            {{ syncErrorText(service.sync_error) }}
          </p>
        </div>
      </td>
      <td class="services-cell-type">{{ enumLabel('serviceTypes', service.type, service.type_title || service.type || t('common.notApplicable')) }}</td>
      <td v-if="variant === 'stack-detail'" class="services-cell-sync-status">
        <div class="stack-service-sync-status">
          <AppStatusBadge
            class="status"
            :class="syncStatusClass(service.sync_status)"
            :variant="syncStatusVariant(service.sync_status)"
          >
            {{ syncStatusLabel(service.sync_status) }}
          </AppStatusBadge>
          <p
            v-if="syncErrorText(service.sync_error)"
            class="stack-service-sync-error"
            :title="syncErrorText(service.sync_error)"
          >
            {{ syncErrorText(service.sync_error) }}
          </p>
        </div>
      </td>
      <td v-else class="services-cell-version" :title="service.image">{{ service.image_version || "—" }}</td>
    </tr>
  </AppTable>
</template>
