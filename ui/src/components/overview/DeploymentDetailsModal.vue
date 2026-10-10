<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import type { DeploymentChange } from "../../api/types";
import { useOverviewStore } from "../../stores/overview";
import { formatDate, shortCommitHash } from "../../utils/format";

const store = useOverviewStore();
const dialog = ref<HTMLElement | null>(null);
const closeButton = ref<HTMLButtonElement | null>(null);
let returnFocus: HTMLElement | null = null;
const focusableSelector = [
  "a[href]",
  "button:not([disabled])",
  "input:not([disabled])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  "[tabindex]:not([tabindex=\"-1\"])",
].join(", ");
const detail = computed(() => store.deploymentDetailsData);
const initialSnapshot = computed(() => detail.value?.comparison_basis === "none");
const groups = computed(() => {
  const result = new Map<string, { type: string; name: string; changes: DeploymentChange[] }>();
  for (const change of detail.value?.changes ?? []) {
    const type = change.resourceType || "stack";
    const name = change.resourceName || "definition";
    const key = JSON.stringify([type, name]);
    if (!result.has(key)) result.set(key, { type, name, changes: [] });
    result.get(key)!.changes.push(change);
  }
  return [...result.values()];
});
const comparison = computed(() => {
  switch (detail.value?.comparison_basis) {
    case "successful_baseline": return "Compared with the last successful desired snapshot.";
    case "last_attempt": return "Compared with the last attempted desired snapshot; the actual state may differ.";
    case "observed_state": return "Compared with an observed snapshot; live convergence is not verified.";
    default: return "No previous desired snapshot is available for comparison.";
  }
});
const outcome = computed(() => {
  if (detail.value?.status === "succeeded") {
    return "Apply pipeline completed. Service health and live convergence have not been verified.";
  }
  return initialSnapshot.value
    ? "The apply attempt did not complete successfully; the actual Swarm state may differ."
    : "These are planned desired changes; the actual Swarm state may differ.";
});
const total = computed(() => {
  const s = detail.value?.summary;
  return s ? (s.added ?? 0) + (s.changed ?? 0) + (s.removed ?? 0) : 0;
});
const duration = computed(() => {
  const ms = detail.value?.duration_ms;
  return ms == null ? "—" : ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)} s`;
});
const safeReasons: Record<string, string> = {
  policy_rejected: "Policy rejected", init_failed: "Init job failed",
  apply_failed: "Apply failed", prune_failed: "Prune failed",
  verification_unknown: "Live state read failed", process_interrupted: "Process interrupted",
};
function value(raw: string | null | undefined, redacted: boolean): string {
  if (raw == null) return "—";
  if (redacted) return "****";
  if (raw === "") return '"" (empty)';
  return raw;
}
function snapshotValue(change: DeploymentChange): string {
  return value(change.after ?? change.before, change.redacted);
}
function focusableElements(): HTMLElement[] {
  if (!dialog.value) return [];
  return [...dialog.value.querySelectorAll<HTMLElement>(focusableSelector)];
}
function close() { store.closeDeploymentDetailsModal(); }
function openCommit() {
  const commit = detail.value?.commit;
  if (commit) {
    close();
    void store.openCommitDetailsModal(commit);
  }
}
function onKeydown(event: KeyboardEvent) {
  if (!store.deploymentDetailsModalOpen) return;
  if (event.key === "Escape") {
    event.preventDefault();
    close();
    return;
  }
  if (event.key !== "Tab") return;

  const focusable = focusableElements();
  if (focusable.length === 0) {
    event.preventDefault();
    dialog.value?.focus();
    return;
  }

  const first = focusable[0];
  const last = focusable[focusable.length - 1];
  const activeIndex = focusable.indexOf(document.activeElement as HTMLElement);
  if (activeIndex === -1 || (event.shiftKey && activeIndex === 0) || (!event.shiftKey && activeIndex === focusable.length - 1)) {
    event.preventDefault();
    (event.shiftKey ? last : first).focus();
  }
}
watch(() => store.deploymentDetailsModalOpen, async (open, wasOpen) => {
  if (open && !wasOpen) {
    returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    await nextTick();
    closeButton.value?.focus();
  } else if (!open && wasOpen) {
    if (returnFocus?.isConnected) returnFocus.focus();
    returnFocus = null;
  }
});
onMounted(() => document.addEventListener("keydown", onKeydown));
onUnmounted(() => document.removeEventListener("keydown", onKeydown));
</script>

<template>
  <div v-if="store.deploymentDetailsModalOpen" class="modal">
    <div class="modal-overlay" @click="close" />
    <div ref="dialog" class="modal-card deployment-modal" role="dialog" aria-modal="true" aria-labelledby="deployment-details-title" tabindex="-1">
      <div class="modal-header">
        <h2 id="deployment-details-title">Deployment · {{ detail?.stack || "Details" }}</h2>
        <button ref="closeButton" class="modal-close" type="button" aria-label="Close deployment details" @click="close">×</button>
      </div>
      <div class="modal-body">
        <p v-if="store.deploymentDetailsLoading" role="status" class="meta">Loading deployment details...</p>
        <div v-else-if="store.deploymentDetailsError" role="alert">
          <p>Could not load deployment details: {{ store.deploymentDetailsError }}</p>
          <button type="button" @click="store.openDeploymentDetailsModal(store.deploymentDetailsID)">Retry</button>
        </div>
        <p v-else-if="!detail" class="meta">Deployment details are unavailable.</p>
        <template v-else>
          <div class="deployment-meta">
            <strong class="deployment-status" :class="`deployment-status--${detail.status}`">{{ detail.status }}</strong>
            <span>{{ shortCommitHash(detail.commit) || "No commit" }}</span>
            <span>Started {{ formatDate(detail.started_at) }}</span>
            <span>Finished {{ detail.finished_at ? formatDate(detail.finished_at) : "—" }}</span>
            <span>Duration {{ duration }}</span>
          </div>
          <p v-if="detail.reason" class="deployment-note" role="status">
            Reason: {{ safeReasons[detail.reason] || "Deployment ended with an unspecified reason" }}
          </p>
          <p class="deployment-note">{{ outcome }}</p>
          <template v-if="initialSnapshot">
            <h3>Initial Deployment Snapshot</h3>
            <p class="deployment-note">First recorded deployment. No previous desired snapshot is available for comparison.</p>
            <p class="deployment-note">This is the recorded target configuration, not proof that these resources were newly created in Swarm.</p>
            <p v-if="detail.summary?.redacted" class="meta">{{ detail.summary.redacted }} values masked</p>
            <p v-if="!groups.length" class="meta">No safe configuration fields are available for this snapshot.</p>
            <section v-for="group in groups" :key="`${group.type}/${group.name}`" class="deployment-resource">
              <h4>{{ group.type }} · {{ group.name }}</h4>
              <div v-for="(change, index) in group.changes" :key="`${change.field}-${index}`"
                class="deployment-snapshot-field">
                <span class="deployment-field">{{ change.field || "definition" }}</span>
                <span class="deployment-value">{{ snapshotValue(change) }}</span>
              </div>
            </section>
          </template>
          <template v-else>
            <p class="deployment-note">{{ comparison }}
              <span v-if="detail.comparison_status !== 'known'">Comparison with live state is unknown.</span>
            </p>
            <h3>Desired changes · {{ total }} fields</h3>
            <p class="meta">
              {{ detail.summary?.added ?? 0 }} added · {{ detail.summary?.changed ?? 0 }} changed ·
              {{ detail.summary?.removed ?? 0 }} removed
              <template v-if="detail.summary?.redacted"> · {{ detail.summary.redacted }} masked</template>
            </p>
            <p v-if="!groups.length" class="meta">No field changes are available for this attempt.</p>
            <section v-for="group in groups" :key="`${group.type}/${group.name}`" class="deployment-resource">
              <h4>{{ group.type }} · {{ group.name }}</h4>
              <div v-for="(change, index) in group.changes" :key="`${change.field}-${index}`"
                class="deployment-change" :class="`deployment-change--${change.operation}`">
                <span class="deployment-field">{{ change.field || "definition" }}</span>
                <span class="deployment-operation">{{ change.operation }}</span>
                <span class="deployment-value">{{ value(change.before, change.redacted) }}</span>
                <span aria-hidden="true">→</span>
                <span class="deployment-value">{{ value(change.after, change.redacted) }}</span>
              </div>
            </section>
          </template>
          <button v-if="detail.commit" type="button" class="button-ghost" @click="openCommit">Git commit details</button>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped>
.deployment-modal { width: min(860px, calc(100vw - 32px)); max-height: min(90vh, 900px); overflow: auto; }
.deployment-meta { display: flex; gap: 8px 16px; flex-wrap: wrap; align-items: center; font-size: .85rem; }
.deployment-status { text-transform: capitalize; padding: 3px 8px; border-radius: 6px; background: var(--surface-muted); }
.deployment-status--succeeded { color: var(--success); background: var(--success-muted); }
.deployment-status--running { color: var(--info); background: var(--info-muted); }
.deployment-status--failed { color: var(--error); background: var(--error-muted); }
.deployment-status--interrupted { color: var(--warning); background: var(--warning-muted); }
.deployment-note { color: var(--muted); line-height: 1.4; }
.deployment-resource { border: 1px solid var(--line); border-radius: 8px; margin: 12px 0; padding: 12px; }
.deployment-resource h4 { margin: 0 0 8px; text-transform: capitalize; }
.deployment-change { display: grid; grid-template-columns: minmax(140px, 1.2fr) auto minmax(0, 1fr) auto minmax(0, 1fr); gap: 8px; align-items: start; padding: 7px 4px; border-top: 1px solid var(--line); font-size: .85rem; }
.deployment-snapshot-field { display: grid; grid-template-columns: minmax(140px, 1fr) minmax(0, 2fr); gap: 12px; padding: 7px 4px; border-top: 1px solid var(--line); font-size: .85rem; }
.deployment-change--added { border-left: 3px solid var(--success, #26845a); }
.deployment-change--removed { border-left: 3px solid var(--error, #c24b4b); }
.deployment-change--changed { border-left: 3px solid var(--accent); }
.deployment-field { font-weight: 600; overflow-wrap: anywhere; }
.deployment-operation { color: var(--muted); }
.deployment-value { overflow-wrap: anywhere; white-space: pre-wrap; }
@media (max-width: 640px) {
  .deployment-change { grid-template-columns: minmax(0, 1fr) auto; }
  .deployment-snapshot-field { grid-template-columns: minmax(0, 1fr); gap: 4px; }
  .deployment-field { grid-column: 1; }
  .deployment-operation { grid-column: 2; }
  .deployment-value { grid-column: 1; }
}
</style>
