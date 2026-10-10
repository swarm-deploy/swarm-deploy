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
const loading = ref(false);
const nextCursor = ref("");
const cursors = ref<string[]>([""]);
const page = ref(0);
let request = 0;
async function load(index: number, cursor: string) {
  const current = ++request;
  loading.value = true;
  error.value = "";
  try {
    const result = await fetchDeployments({ limit: 20, cursor: cursor || undefined });
    if (current !== request) return;
    deployments.value = Array.isArray(result.deployments) ? result.deployments : [];
    nextCursor.value = result.next_cursor ?? "";
    page.value = index;
    cursors.value[index] = cursor;
  } catch (e) {
    if (current === request) error.value = e instanceof Error ? e.message : "Failed to load deployments";
  } finally {
    if (current === request) loading.value = false;
  }
}
function next() { if (nextCursor.value) void load(page.value + 1, nextCursor.value); }
function previous() { if (page.value > 0) void load(page.value - 1, cursors.value[page.value - 1] ?? ""); }
function totalChanges(d: DeploymentSummary) {
  return (d.summary?.added ?? 0) + (d.summary?.changed ?? 0) + (d.summary?.removed ?? 0);
}
onMounted(() => { void load(0, ""); });
</script>

<template>
  <section class="services-page">
    <header class="services-header"><h2>Deployments</h2></header>
    <p class="meta">Apply attempts and their desired changes. Service convergence is not verified.</p>
    <p v-if="loading" role="status">Loading deployments...</p>
    <p v-else-if="error" role="alert">{{ error }} <button type="button" @click="load(page, cursors[page] ?? '')">Retry</button></p>
    <p v-else-if="!deployments.length">No deployments on this page.</p>
    <AppTable v-else table-class="deployments-table">
      <template #head><tr><th>Stack</th><th>Status</th><th>Started</th><th>Commit</th><th>Desired changes</th></tr></template>
      <tr v-for="deployment in deployments" :key="deployment.id">
        <td><button type="button" :aria-label="`Open deployment details for ${deployment.stack}`"
          @click="store.openDeploymentDetailsModal(deployment.id)">{{ deployment.stack }}</button></td>
        <td>{{ deployment.status }}</td>
        <td>{{ formatDate(deployment.started_at) }}</td>
        <td><button v-if="deployment.commit" type="button" :aria-label="`Open Git commit ${deployment.commit}`"
          @click.stop="store.openCommitDetailsModal(deployment.commit)">{{ shortCommitHash(deployment.commit) }}</button><span v-else>—</span></td>
        <td>{{ totalChanges(deployment) }} fields · {{ deployment.resources?.services ?? 0 }} services,
          {{ deployment.resources?.configs ?? 0 }} configs, {{ deployment.resources?.secrets ?? 0 }} secrets
          <button type="button" :aria-label="`View changes for ${deployment.stack}`"
            @click="store.openDeploymentDetailsModal(deployment.id)">View changes</button></td>
      </tr>
    </AppTable>
    <nav class="deployment-pages" aria-label="Deployment pages">
      <button type="button" :disabled="loading || page === 0" @click="previous">Previous</button>
      <span>Page {{ page + 1 }}</span>
      <button type="button" :disabled="loading || !nextCursor" @click="next">Next</button>
    </nav>
  </section>
</template>

<style scoped>
.deployment-pages { display: flex; align-items: center; gap: 12px; margin-top: 12px; }
</style>
