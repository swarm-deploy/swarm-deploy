<script setup lang="ts">
import { onMounted, ref } from "vue";
import { fetchDeployments } from "../api/overview";
import type { Deployment } from "../api/types";
import AppTable from "../components/common/AppTable.vue";
import { useOverviewStore } from "../stores/overview";
import { formatDate, shortCommitHash } from "../utils/format";

const store = useOverviewStore();
const deployments = ref<Deployment[]>([]);
const error = ref("");
const loading = ref(true);
onMounted(async () => {
  try { deployments.value = (await fetchDeployments({ limit: 100 })).deployments; }
  catch (e) { error.value = e instanceof Error ? e.message : "Failed to load deployments"; }
  finally { loading.value = false; }
});
</script>

<template>
  <section class="services-page">
    <header class="services-header"><h2>Deployments</h2></header>
    <p class="meta">Latest 100 apply attempts. Succeeded means apply completed, not that Swarm services are healthy. Changes are planned relative to the last successful deployment.</p>
    <p v-if="loading">Loading deployments...</p>
    <p v-else-if="error" role="alert">{{ error }}</p>
    <p v-else-if="!deployments.length">No deployments yet. Legacy events are preserved separately.</p>
    <AppTable v-else table-class="deployments-table">
      <template #head><tr><th>Stack</th><th>Status</th><th>Started</th><th>Commit</th><th>Planned changes</th></tr></template>
      <tr v-for="deployment in deployments" :key="deployment.id">
        <td>{{ deployment.stack }}</td>
        <td>{{ deployment.status }}<span v-if="deployment.error_code"> · {{ deployment.error_code }}</span></td>
        <td>{{ formatDate(deployment.started_at) }}</td>
        <td><button type="button" @click="store.openCommitDetailsModal(deployment.commit)">{{ shortCommitHash(deployment.commit) }}</button></td>
        <td>
          <details><summary>{{ deployment.changes.length }} changed fields</summary>
            <ul><li v-for="change in deployment.changes" :key="change.path">
              <code>{{ change.path }}</code>: {{ change.before ?? "(absent)" }} → {{ change.after ?? "(removed)" }}
              <span v-if="change.redacted"> (sensitive value hidden)</span>
            </li></ul>
          </details>
        </td>
      </tr>
    </AppTable>
  </section>
</template>
