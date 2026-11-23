<script setup lang="ts">
import { t } from "../../i18n";
import { computed } from "vue";
import { useRouter } from "vue-router";

import AppModal from "../common/AppModal.vue";
import AppTable from "../common/AppTable.vue";
import type { SecretServiceUsage } from "../../api/types";
import { useSecretDetailsStore } from "../../stores/secretDetails";
import { formatDate } from "../../utils/format";

const secretDetailsStore = useSecretDetailsStore();
const router = useRouter();

const secretDescription = computed(() => secretDetailsStore.secret?.labels?.description ?? "");

const secretLabels = computed(() => {
  const labels = secretDetailsStore.secret?.labels;
  if (!labels || typeof labels !== "object") {
    return [];
  }

  return Object.entries(labels)
    .filter(([key]) => key !== "description")
    .sort(([left], [right]) => left.localeCompare(right));
});

const usedByServices = computed(() => {
  const usedBy = secretDetailsStore.secret?.used_by;
  return Array.isArray(usedBy) ? usedBy : [];
});

function closeSecretDetailsModal() {
  secretDetailsStore.closeSecretDetails();
}

async function openService(usage: SecretServiceUsage) {
  closeSecretDetailsModal();
  await router.push({
    name: "service-details",
    params: { stack: usage.stack, service: usage.service },
  });
}
</script>

<template>
  <AppModal
    :open="secretDetailsStore.modalOpen"
    labelledby="secret-details-title"
    keep-mounted
    @close="closeSecretDetailsModal"
  >
    <template #title>
      <h2 id="secret-details-title">
        {{ secretDetailsStore.secret ? secretDetailsStore.secret.name : secretDetailsStore.selectedName || t('secrets.details') }}
      </h2>
    </template>

    <div class="modal-body">
      <p v-if="secretDetailsStore.loading" class="meta">{{ t('secrets.loadingDetails') }}</p>
      <p v-else-if="secretDetailsStore.error" class="meta">
        {{ t('secrets.detailsError', { error: secretDetailsStore.error }) }}
      </p>
      <div v-else-if="secretDetailsStore.secret" class="service-metrics">
        <AppTable summary :aria-label="t('secrets.summary')">
            <tr>
              <th scope="row">ID</th>
              <td><code>{{ secretDetailsStore.secret.id }}</code></td>
            </tr>
            <tr>
              <th scope="row">{{ t('common.name') }}</th>
              <td>{{ secretDetailsStore.secret.name }}</td>
            </tr>
            <tr>
              <th scope="row">{{ t('common.description') }}</th>
              <td>{{ secretDescription || t('common.notApplicable') }}</td>
            </tr>
            <tr>
              <th scope="row">{{ t('secrets.versionId') }}</th>
              <td>{{ secretDetailsStore.secret.version_id }}</td>
            </tr>
            <tr>
              <th scope="row">{{ t('secrets.createdAt') }}</th>
              <td>{{ formatDate(secretDetailsStore.secret.created_at) }}</td>
            </tr>
            <tr>
              <th scope="row">{{ t('secrets.updatedAt') }}</th>
              <td>{{ formatDate(secretDetailsStore.secret.updated_at) }}</td>
            </tr>
            <tr>
              <th scope="row">{{ t('common.driver') }}</th>
              <td>{{ secretDetailsStore.secret.driver || t('common.notApplicable') }}</td>
            </tr>
            <tr>
              <th scope="row">{{ t('secrets.externalPath') }}</th>
              <td>{{ secretDetailsStore.secret.external?.path || t('common.notApplicable') }}</td>
            </tr>
            <tr>
              <th scope="row">{{ t('secrets.externalVersionId') }}</th>
              <td>{{ secretDetailsStore.secret.external?.version_id || t('common.notApplicable') }}</td>
            </tr>
            <tr>
              <th scope="row">{{ t('common.labels') }}</th>
              <td>
                <ul v-if="secretLabels.length > 0" class="event-details">
                  <li v-for="[key, value] in secretLabels" :key="key" class="event-detail">
                    <span class="event-detail-key">{{ key }}</span>
                    <code class="event-detail-value">{{ value }}</code>
                  </li>
                </ul>
                <span v-else class="meta">{{ t('common.noLabels') }}</span>
              </td>
            </tr>
        </AppTable>

        <section class="secret-used-by" aria-labelledby="secret-used-by-title">
          <h3 id="secret-used-by-title">{{ t('secrets.usedByServices') }}</h3>
          <ul v-if="usedByServices.length > 0" class="secret-used-by-list">
            <li v-for="usage in usedByServices" :key="`${usage.stack}/${usage.service}/${usage.target || ''}`">
              <button type="button" class="secret-used-by-service" @click="openService(usage)">
                <span>{{ usage.stack }} / {{ usage.service }}</span>
                <code v-if="usage.target">{{ usage.target }}</code>
              </button>
            </li>
          </ul>
          <p v-else class="meta">{{ t('secrets.unused') }}</p>
        </section>
      </div>
    </div>
  </AppModal>
</template>
