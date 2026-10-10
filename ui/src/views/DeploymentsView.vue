<script setup lang="ts">
import { onMounted, ref } from "vue";
import { fetchDeployments } from "../api/overview";
import type { DeploymentSummary } from "../api/types";
import AppTable from "../components/common/AppTable.vue";
import { useOverviewStore } from "../stores/overview";
import { formatDate, shortCommitHash } from "../utils/format";

const store = useOverviewStore();
const deployments = ref<DeploymentSummary[]>([]);
const error = ref("");
const loading = ref(true);
onMounted(async () => {
  try { deployments.value = (await fetchDeployments({ limit: 100 })).deployments; }
  catch (e) { error.value = e instanceof Error ? e.message : "Failed to load deployments"; }
  finally { loading.value = false; }
});

function totalChanges(deployment: DeploymentSummary): number {
  return deployment.summary.added + deployment.summary.changed + deployment.summary.removed;
}
</script>

<template>
  <section class="services-page">
    <header class="services-header"><h2>Deployments</h2></header>
    <p class="meta">Latest 100 apply attempts. Stage outcomes and comparison confidence are reported independently from service health.</p>
    <p v-if="loading">Loading deployments...</p>
    <p v-else-if="error" role="alert">{{ error }}</p>
    <p v-else-if="!deployments.length">No deployments yet. Legacy events are preserved separately.</p>
    <AppTable v-else table-class="deployments-table">
      <template #head><tr><th>Stack</th><th>Status</th><th>Started</th><th>Commit</th><th>Change summary</th></tr></template>
      <tr v-for="deployment in deployments" :key="deployment.id">
        <td>{{ deployment.stack }}</td>
        <td>
          {{ deployment.status }} · {{ deployment.phase }}
          <span v-if="deployment.reason"> · {{ deployment.reason }}</span>
          <span v-if="deployment.actual_state_status === 'unknown'"> · actual state unknown</span>
        </td>
        <td>{{ formatDate(deployment.started_at) }}</td>
        <td><button type="button" @click="store.openCommitDetailsModal(deployment.commit)">{{ shortCommitHash(deployment.commit) }}</button></td>
        <td>
          {{ totalChanges(deployment) }} fields
          ({{ deployment.summary.added }} added, {{ deployment.summary.changed }} changed,
          {{ deployment.summary.removed }} removed<span v-if="deployment.summary.redacted">,
          {{ deployment.summary.redacted }} redacted</span>) ·
          {{ deployment.resources.services }} services,
          {{ deployment.resources.configs }} configs,
          {{ deployment.resources.secrets }} secrets ·
          {{ deployment.comparison_basis }} / {{ deployment.comparison_status }}
        </td>
      </tr>
    </AppTable>
  </section>
</template>
