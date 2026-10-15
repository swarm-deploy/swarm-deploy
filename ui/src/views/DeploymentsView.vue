<script setup lang="ts">
import { onMounted, ref } from "vue";
import { fetchDeployments } from "../api/overview";
import type { DeploymentSummary } from "../api/types";
import AppTable from "../components/common/AppTable.vue";
import AppPagination from "../components/common/AppPagination.vue";
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

function startedAtTimestamp(deployment: DeploymentSummary): number {
  const timestamp = Date.parse(deployment.started_at);
  return Number.isNaN(timestamp) ? 0 : timestamp;
}

async function load(index: number, cursor: string) {
  const current = ++request;
  loading.value = true;
  error.value = "";
  try {
    const result = await fetchDeployments({ limit: 20, cursor: cursor || undefined });
    if (current !== request) return;
    deployments.value = Array.isArray(result.deployments)
      ? [...result.deployments].sort((left, right) => startedAtTimestamp(right) - startedAtTimestamp(left))
      : [];
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

function statusClass(status: unknown): string {
  switch (status) {
    case "succeeded": return "success";
    case "failed": return "failed";
    case "running": return "deployment-status-badge--running";
    default: return "unknown";
  }
}

function statusLabel(status: unknown): string {
  const value = typeof status === "string" ? status.trim() : "";
  if (!value) return "Unknown";
  return value.charAt(0).toUpperCase() + value.slice(1);
}

function openDeployment(deploymentID: string): void {
  store.openDeploymentDetailsModal(deploymentID);
}

function openDeploymentFromKeyboard(event: KeyboardEvent, deploymentID: string): void {
  if (event.target !== event.currentTarget || (event.key !== "Enter" && event.key !== " ")) return;
  event.preventDefault();
  openDeployment(deploymentID);
}

function openCommit(commit: string): void {
  void store.openCommitDetailsModal(commit);
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
    <AppTable v-else fixed min-width="620px" table-class="deployments-table" wrap-class="deployments-table-wrap">
      <template #head><tr><th>Stack</th><th>Status</th><th>Started</th><th>Commit</th></tr></template>
      <tr
        v-for="deployment in deployments"
        :key="deployment.id"
        class="app-table-row--clickable deployment-row"
        tabindex="0"
        :aria-label="`Open deployment details for ${deployment.stack}`"
        @click="openDeployment(deployment.id)"
        @keydown="openDeploymentFromKeyboard($event, deployment.id)"
      >
        <td class="deployment-stack" :title="deployment.stack">{{ deployment.stack }}</td>
        <td>
          <span class="status deployment-status-badge" :class="statusClass(deployment.status)">
            {{ statusLabel(deployment.status) }}
          </span>
        </td>
        <td class="deployment-started"><time :datetime="deployment.started_at">{{ formatDate(deployment.started_at) }}</time></td>
        <td>
          <a
            v-if="deployment.commit"
            class="deployment-commit-link"
            href="#"
            :aria-label="`Open Git commit ${deployment.commit}`"
            :title="deployment.commit"
            @click.prevent.stop="openCommit(deployment.commit)"
            @keydown.enter.stop
            @keydown.space.prevent.stop="openCommit(deployment.commit)"
          >{{ shortCommitHash(deployment.commit) }}</a>
          <span v-else>—</span>
        </td>
      </tr>
    </AppTable>
    <AppPagination class="deployment-pages" label="Deployment pages" :page="page" :loading="loading" :has-next="Boolean(nextCursor)" @previous="previous" @next="next" />
  </section>
</template>

<style scoped>
:deep(.deployments-table th:nth-child(1)),
:deep(.deployments-table td:nth-child(1)) { width: 42%; }
:deep(.deployments-table th:nth-child(2)),
:deep(.deployments-table td:nth-child(2)) { width: 18%; }
:deep(.deployments-table th:nth-child(3)),
:deep(.deployments-table td:nth-child(3)) { width: 26%; }
:deep(.deployments-table th:nth-child(4)),
:deep(.deployments-table td:nth-child(4)) { width: 14%; }

.deployment-stack {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.deployment-started,
.deployment-status-badge,
.deployment-commit-link { white-space: nowrap; }

.deployment-status-badge {
  display: inline-flex;
  align-items: center;
  background: var(--surface-muted);
  line-height: 1.2;
}

.deployment-status-badge.success { background: var(--success-muted); }
.deployment-status-badge.failed { background: var(--error-muted); }
.deployment-status-badge--running {
  color: var(--info);
  border-color: var(--accent-border);
  background: var(--info-muted);
}

.deployment-commit-link {
  display: inline-block;
  border-radius: 3px;
  color: var(--accent-strong);
  padding: 2px 3px;
  font-family: "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 0.8rem;
  text-decoration: none;
}

.deployment-commit-link:hover { text-decoration: underline; }
.deployment-commit-link:focus-visible {
  outline: 2px solid var(--accent-focus);
  outline-offset: 2px;
}
</style>
