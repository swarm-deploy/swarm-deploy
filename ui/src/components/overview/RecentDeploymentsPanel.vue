<script setup lang="ts">
import type { RouteLocationRaw } from "vue-router";

import { useOverviewStore } from "../../stores/overview";
import { formatDate, shortCommitHash } from "../../utils/format";
import DeploymentIcon from "./DeploymentIcon.vue";

interface RecentDeploymentItem {
  id: string;
  status: string;
  startedAt: string;
  commit?: string;
  phase?: string;
  summary: string;
}

defineProps<{
  id: string;
  title: string;
  link: RouteLocationRaw;
  deployments: RecentDeploymentItem[];
  loading?: boolean;
  error?: string;
}>();

const store = useOverviewStore();

function deploymentStatusClass(status: string): string {
  switch (status) {
    case "succeeded": return "stack-deployment--success";
    case "failed": return "stack-deployment--failed";
    case "running": return "stack-deployment--running";
    default: return "stack-deployment--unknown";
  }
}

function deploymentIcon(status: string): "check" | "failed" | "running" | "warning" {
  switch (status) {
    case "succeeded": return "check";
    case "failed": return "failed";
    case "running": return "running";
    default: return "warning";
  }
}

function deploymentTitle(status: string): string {
  switch (status) {
    case "succeeded": return "Deploy completed";
    case "failed": return "Deploy failed";
    case "running": return "Deploy started";
    case "interrupted": return "Deploy interrupted";
    case "superseded": return "Deploy superseded";
    default: return "Deployment updated";
  }
}

function openDeploymentFromKeyboard(event: KeyboardEvent, id: string) {
  if (event.target !== event.currentTarget || (event.key !== "Enter" && event.key !== " ")) return;
  event.preventDefault();
  void store.openDeploymentDetailsModal(id);
}
</script>

<template>
  <section class="stack-panel" :aria-labelledby="id">
    <header class="overview-summary-panel-header">
      <RouterLink class="overview-summary-panel-header-link" :to="link">
        <span class="overview-summary-panel-icon" aria-hidden="true">
          <svg viewBox="0 0 24 24" fill="none"><path d="M12 3v11m0 0 4-4m-4 4-4-4M5 17v3h14v-3" /></svg>
        </span>
        <h3 :id="id" class="overview-panel-title">{{ title }}</h3>
      </RouterLink>
    </header>
    <p v-if="loading" class="stack-panel-state" role="status">Loading deployments...</p>
    <p v-else-if="error" class="stack-panel-state" role="alert">Failed to load deployments: {{ error }}</p>
    <p v-else-if="!deployments.length" class="stack-panel-state">No deployments yet.</p>
    <ol v-else class="stack-deployment-list">
      <li
        v-for="deployment in deployments"
        :key="deployment.id"
        class="stack-deployment"
        :class="deploymentStatusClass(deployment.status)"
        tabindex="0"
        :aria-label="`Open ${deployment.status} deployment details`"
        @click="store.openDeploymentDetailsModal(deployment.id)"
        @keydown="openDeploymentFromKeyboard($event, deployment.id)"
      >
        <span class="stack-deployment-icon"><DeploymentIcon :name="deploymentIcon(deployment.status)" /></span>
        <div class="stack-deployment-copy">
          <div class="stack-deployment-heading">
            <strong>{{ deploymentTitle(deployment.status) }}</strong>
            <time :datetime="deployment.startedAt">{{ formatDate(deployment.startedAt) }}</time>
          </div>
          <p class="stack-deployment-meta">
            <span>Commit
              <button
                v-if="deployment.commit"
                type="button"
                class="stack-commit-button"
                :aria-label="`Open deployment commit ${deployment.commit}`"
                @click.stop="store.openCommitDetailsModal(deployment.commit)"
                @keydown.stop
              >
                {{ shortCommitHash(deployment.commit) }}
              </button>
              <span v-else>not available</span>
            </span>
            <span v-if="deployment.phase">Phase <strong>{{ deployment.phase }}</strong></span>
          </p>
          <p class="stack-deployment-summary">{{ deployment.summary }}</p>
        </div>
      </li>
    </ol>
  </section>
</template>

<style scoped>
.stack-panel { min-width: 0; border: 1px solid var(--border-subtle); border-radius: 10px; background: var(--surface-card-soft); overflow: hidden; }
.stack-panel-state { margin: 0; padding: 20px 16px; color: var(--muted); font-size: .86rem; font-variant-numeric: tabular-nums; }
.stack-deployment-list { list-style: none; margin: 0; padding: 0 16px 8px; }
.stack-deployment { position: relative; display: grid; grid-template-columns: 32px minmax(0, 1fr); gap: 12px; padding: 16px 0; cursor: pointer; }
.stack-deployment + .stack-deployment { border-top: 1px solid var(--border-subtle); }
.stack-deployment::before { content: ""; position: absolute; left: 16px; top: 50px; bottom: -16px; width: 1px; background: var(--border-subtle); }
.stack-deployment:last-child::before { display: none; }
.stack-deployment:hover .stack-deployment-copy strong { color: var(--accent-strong); }
.stack-deployment:focus-visible { outline: 2px solid var(--accent-focus); outline-offset: -2px; }
.stack-deployment-icon { position: relative; z-index: 1; width: 29px; height: 29px; display: grid; place-items: center; border: 1px solid var(--border-default); border-radius: 50%; color: var(--muted); background: var(--surface-muted); }
.stack-deployment-icon :deep(svg) { width: 15px; height: 15px; }
.stack-deployment--success .stack-deployment-icon { color: var(--success); border-color: var(--success-border); background: var(--success-muted); }
.stack-deployment--failed .stack-deployment-icon { color: var(--error); border-color: var(--error-border); background: var(--error-muted); }
.stack-deployment--running .stack-deployment-icon { color: var(--info); border-color: var(--accent-border); background: var(--info-muted); }
.stack-deployment-copy { min-width: 0; display: grid; gap: 4px; }
.stack-deployment-heading { display: flex; align-items: baseline; justify-content: space-between; gap: 12px; }
.stack-deployment-heading strong { font-size: .86rem; transition: color 120ms ease; }
.stack-deployment-heading time { color: var(--muted); font-size: .75rem; white-space: nowrap; }
.stack-deployment-meta, .stack-deployment-summary { margin: 0; }
.stack-deployment-meta { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 16px; color: var(--muted); font-size: .76rem; }
.stack-deployment-meta > span { display: inline-flex; gap: 4px; }
.stack-deployment-meta strong { color: var(--text); font-weight: 500; }
.stack-deployment-summary { color: var(--muted); font-size: .78rem; line-height: 1.4; overflow-wrap: anywhere; }
.stack-deployment--failed .stack-deployment-summary { color: var(--error); }
.stack-commit-button { border: 0; background: transparent; color: var(--control-primary); padding: 0; border-radius: 3px; font: inherit; font-weight: 650; box-shadow: none; white-space: nowrap; }
.stack-commit-button:hover { color: var(--control-primary-hover); transform: none; filter: none; }
.stack-commit-button:focus-visible { outline: 2px solid var(--accent-focus); outline-offset: 2px; }
.stack-commit-button:active { transform: translateY(1px); }
.stack-commit-button:disabled { color: var(--muted); background: var(--surface-muted); opacity: .65; cursor: not-allowed; }
:global(:root[data-theme="light"]) .stack-panel { border-color: var(--border-default); }
@media (max-width: 720px) {
  .stack-deployment-heading { align-items: flex-start; flex-direction: column; gap: 4px; }
  .stack-deployment-meta { align-items: flex-start; flex-direction: column; gap: 4px; }
}
@media (prefers-reduced-motion: reduce) {
  .stack-deployment-heading strong { transition-duration: 0s; }
}
</style>
