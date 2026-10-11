<script setup lang="ts">
import yaml from "@speed-highlight/core/languages/yaml.js";
import { tokenizeWith } from "@speed-highlight/core/tokenize";
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import type { DeploymentChange } from "../../api/types";
import { useOverviewStore } from "../../stores/overview";
import { formatDate, shortCommitHash } from "../../utils/format";

type DetailsTab = "changes" | "configuration";
type ChangeGroup = {
  type: string;
  name: string;
  operation: "added" | "modified" | "removed";
  sections: Array<{ name: string; changes: DeploymentChange[] }>;
};

const store = useOverviewStore();
const dialog = ref<HTMLElement | null>(null);
const closeButton = ref<HTMLButtonElement | null>(null);
const activeTab = ref<DetailsTab>("changes");
const copyLabel = ref("Copy YAML");
let returnFocus: HTMLElement | null = null;
const focusableSelector = [
  "a[href]", "button:not([disabled])", "input:not([disabled])", "select:not([disabled])",
  "textarea:not([disabled])", "[tabindex]:not([tabindex=\"-1\"])",
].join(", ");
const detail = computed(() => store.deploymentDetailsData);
const initialSnapshot = computed(() => detail.value?.comparison_basis === "none");
const changeState = computed<"initial" | "unavailable" | "none" | "changes">(() => {
  if (initialSnapshot.value) return "initial";
  if (!detail.value?.diff_available) return "unavailable";
  return (detail.value.changes?.length ?? 0) === 0 ? "none" : "changes";
});

function sectionFor(field: string): string {
  const root = field.split(/[.[]/, 1)[0] || "General";
  return root === field && !["environment", "networks", "ports", "volumes", "secrets", "configs", "deploy"].includes(root)
    ? "General"
    : root;
}

function resourceOperation(changes: DeploymentChange[]): ChangeGroup["operation"] {
  const hasIdentity = changes.some((change) => change.field === "name" || change.field === "definition");
  if (hasIdentity && changes.length > 0 && changes.every((change) => change.operation === "added")) return "added";
  if (hasIdentity && changes.length > 0 && changes.every((change) => change.operation === "removed")) return "removed";
  return "modified";
}

const groups = computed<ChangeGroup[]>(() => {
  const resources = new Map<string, { type: string; name: string; changes: DeploymentChange[] }>();
  for (const change of detail.value?.changes ?? []) {
    const type = change.resourceType || "stack";
    const name = change.resourceName || "definition";
    const key = JSON.stringify([type, name]);
    if (!resources.has(key)) resources.set(key, { type, name, changes: [] });
    resources.get(key)!.changes.push(change);
  }
  return [...resources.values()].map((resource) => {
    const sections = new Map<string, DeploymentChange[]>();
    for (const change of resource.changes) {
      const section = sectionFor(change.field);
      if (!sections.has(section)) sections.set(section, []);
      sections.get(section)!.push(change);
    }
    return { ...resource, operation: resourceOperation(resource.changes),
      sections: [...sections].map(([name, changes]) => ({ name, changes })) };
  });
});

const initialServices = computed(() => groups.value.filter((group) => group.type === "service").map((group) => {
  const changes = group.sections.flatMap((section) => section.changes);
  const field = (name: string) => changes.find((change) => change.field === name)?.after;
  return { name: group.name, image: field("image") || "—", replicas: field("deploy.replicas") || "—" };
}));

const comparison = computed(() => {
  switch (detail.value?.comparison_basis) {
    case "successful_baseline": return "Compared with the last successful desired snapshot.";
    case "last_attempt": return "Compared with the last attempted desired snapshot; the actual state may differ.";
    case "observed_state": return "Compared with an observed snapshot; live convergence is not verified.";
    default: return "No previous desired snapshot is available for comparison.";
  }
});
const outcome = computed(() => {
  switch (detail.value?.status) {
    case "running": return "Apply attempt is still in progress. Live convergence has not been verified.";
    case "succeeded": return "Apply pipeline completed. Service health and live convergence have not been verified.";
    case "failed": return "Apply attempt failed. The actual Swarm state may differ from this desired configuration.";
    case "interrupted": return "Apply outcome is unknown. The actual Swarm state may differ from this desired configuration.";
    default: return "";
  }
});
const total = computed(() => detail.value?.changes?.length ?? 0);
const duration = computed(() => {
  const ms = detail.value?.duration_ms;
  return ms == null ? (detail.value?.status === "running" ? "In progress" : "—") : ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)} s`;
});
const statusLabel = computed(() => {
  switch (detail.value?.status) {
    case "running": return "Running";
    case "succeeded": return "Succeeded";
    case "failed": return "Failed";
    case "interrupted": return "Interrupted";
    default: return "Unknown";
  }
});
const reasonLabels: Record<string, string> = {
  policy_rejected: "Policy check failed", init_failed: "Init job failed", apply_failed: "Apply failed",
  prune_failed: "Cleanup failed", verification_unknown: "Live state read failed", process_interrupted: "Process interrupted",
};
const failureReason = computed(() => detail.value?.diagnostic || reasonLabels[detail.value?.reason ?? ""] || "No detailed diagnostic was recorded.");
const configuration = computed(() => detail.value?.configuration_yaml ?? "");
const configurationHtml = computed(() => highlightYaml(configuration.value));

function escapeHtml(value: string): string {
  return value.replaceAll("&", "&#38;").replaceAll("<", "&lt;").replaceAll(">", "&gt;");
}
function highlightYaml(source: string): string {
  if (!source.trim()) return "";
  let highlighted = "";
  tokenizeWith(source, yaml, (text, token) => {
    const safe = escapeHtml(text);
    highlighted += token ? `<span class="shj-syn-${token}">${safe}</span>` : safe;
  });
  const lines = "<div></div>".repeat(source.split(/\r\n|\r|\n/).length);
  return `<div><div class="shj-numbers" aria-hidden="true">${lines}</div><div>${highlighted}</div></div>`;
}
function fieldLabel(change: DeploymentChange, section: string): string {
  let field = change.field || "definition";
  if (section !== "General" && field.startsWith(section)) field = field.slice(section.length).replace(/^\./, "");
  if (field === "" || field === "definition") return "Definition";
  if (field === "deploy.replicas") return "Replicas";
  return field.replaceAll("_", " ");
}
function sectionLabel(section: string): string {
  const label = section.replaceAll("_", " ");
  return label.charAt(0).toUpperCase() + label.slice(1);
}
function value(raw: string | null | undefined, redacted: boolean): string {
  if (raw == null) return "—";
  if (redacted) return "****";
  if (raw === "") return '"" (empty)';
  return raw;
}
function focusableElements(): HTMLElement[] {
  return dialog.value ? [...dialog.value.querySelectorAll<HTMLElement>(focusableSelector)] : [];
}
function close() { store.closeDeploymentDetailsModal(); }
function openCommit() {
  const commit = detail.value?.commit;
  if (commit) void store.openCommitDetailsModal(commit);
}
async function copyConfiguration() {
  if (!configuration.value) return;
  await navigator.clipboard?.writeText(configuration.value);
  copyLabel.value = "Copied";
  window.setTimeout(() => { copyLabel.value = "Copy YAML"; }, 1200);
}
function onKeydown(event: KeyboardEvent) {
  if (!store.deploymentDetailsModalOpen || store.commitDetailsModalOpen) return;
  if (event.key === "Escape") { event.preventDefault(); close(); return; }
  if (event.key !== "Tab") return;
  const focusable = focusableElements();
  if (focusable.length === 0) { event.preventDefault(); dialog.value?.focus(); return; }
  const first = focusable[0];
  const last = focusable[focusable.length - 1];
  const activeIndex = focusable.indexOf(document.activeElement as HTMLElement);
  if (activeIndex === -1 || (event.shiftKey && activeIndex === 0) || (!event.shiftKey && activeIndex === focusable.length - 1)) {
    event.preventDefault(); (event.shiftKey ? last : first).focus();
  }
}
watch(() => store.deploymentDetailsModalOpen, async (open, wasOpen) => {
  if (open && !wasOpen) {
    returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    activeTab.value = "changes";
    await nextTick(); closeButton.value?.focus();
  } else if (!open && wasOpen) {
    if (returnFocus?.isConnected) returnFocus.focus();
    returnFocus = null;
  }
});
onMounted(() => document.addEventListener("keydown", onKeydown));
onUnmounted(() => document.removeEventListener("keydown", onKeydown));
</script>

<template>
  <div
    v-if="store.deploymentDetailsModalOpen"
    class="modal"
    :aria-hidden="store.commitDetailsModalOpen"
    :inert="store.commitDetailsModalOpen || undefined"
  >
    <div class="modal-overlay" @click="close" />
    <div ref="dialog" class="modal-card deployment-modal" role="dialog" aria-modal="true" aria-labelledby="deployment-details-title" tabindex="-1">
      <div class="modal-header deployment-modal-header">
        <div><span class="deployment-modal-eyebrow">Deployment details</span><h2 id="deployment-details-title">{{ detail?.stack || "Deployment" }}</h2></div>
        <button ref="closeButton" class="modal-close" type="button" aria-label="Close deployment details" @click="close">×</button>
      </div>
      <div class="modal-body deployment-modal-body">
        <p v-if="store.deploymentDetailsLoading" role="status" class="meta">Loading deployment details...</p>
        <div v-else-if="store.deploymentDetailsError" role="alert">
          <p>Could not load deployment details: {{ store.deploymentDetailsError }}</p>
          <button type="button" @click="store.openDeploymentDetailsModal(store.deploymentDetailsID)">Retry</button>
        </div>
        <p v-else-if="!detail" class="meta">Deployment details are unavailable.</p>
        <template v-else>
          <section class="deployment-summary" aria-labelledby="deployment-summary-title">
            <div class="deployment-summary-heading">
              <h3 id="deployment-summary-title">Summary</h3>
              <strong class="deployment-status" :class="`deployment-status--${detail.status}`">{{ statusLabel }}</strong>
            </div>
            <dl class="deployment-summary-grid">
              <div><dt>Stack</dt><dd>{{ detail.stack }}</dd></div>
              <div><dt>Commit</dt><dd><button v-if="detail.commit" type="button" class="deployment-commit-link" @click="openCommit">{{ shortCommitHash(detail.commit) }}</button><span v-else>—</span></dd></div>
              <div><dt>Started</dt><dd>{{ formatDate(detail.started_at) }}</dd></div>
              <div><dt>Finished</dt><dd>{{ detail.finished_at ? formatDate(detail.finished_at) : detail.status === "running" ? "In progress" : "—" }}</dd></div>
              <div><dt>Duration</dt><dd>{{ duration }}</dd></div>
              <div><dt>Stage</dt><dd>{{ detail.phase }}</dd></div>
            </dl>
            <div v-if="detail.status === 'failed' || detail.status === 'interrupted'" class="deployment-diagnostic" role="status">
              <strong>{{ detail.status === "failed" ? "Failure reason" : "Outcome detail" }}</strong>
              <p>{{ failureReason }}</p>
              <span v-if="detail.reason" class="deployment-code">Code: {{ detail.reason }}</span>
            </div>
            <p class="deployment-note">{{ outcome }}</p>
          </section>

          <div class="deployment-tabs" role="tablist" aria-label="Deployment detail views">
            <button type="button" role="tab" :aria-selected="activeTab === 'changes'" :class="{ active: activeTab === 'changes' }" @click="activeTab = 'changes'">Changes</button>
            <button type="button" role="tab" :aria-selected="activeTab === 'configuration'" :class="{ active: activeTab === 'configuration' }" @click="activeTab = 'configuration'">Configuration</button>
          </div>

          <section v-if="activeTab === 'changes'" class="deployment-tab-panel" role="tabpanel">
            <template v-if="changeState === 'initial'">
              <h3>Initial snapshot</h3>
              <p class="deployment-note">First recorded desired-state snapshot. No previous snapshot is available for comparison.</p>
              <div class="deployment-counts" aria-label="Recorded resource counts">
                <span><strong>{{ detail.resources.services }}</strong> services</span><span><strong>{{ detail.resources.networks }}</strong> networks</span>
                <span><strong>{{ detail.resources.secrets }}</strong> secrets</span><span><strong>{{ detail.resources.configs }}</strong> configs</span>
                <span v-if="detail.resources.volumes"><strong>{{ detail.resources.volumes }}</strong> volumes</span>
              </div>
              <div v-if="initialServices.length" class="deployment-service-overview">
                <h4>Services</h4>
                <div v-for="service in initialServices" :key="service.name" class="deployment-service-row">
                  <strong>{{ service.name }}</strong><span>{{ service.image }}</span><span>{{ service.replicas }} replicas</span>
                </div>
              </div>
              <p v-else class="meta">No service summary is available for this snapshot.</p>
            </template>
            <template v-else-if="changeState === 'unavailable'">
              <h3>Changes unavailable</h3>
              <p class="deployment-note">A previous desired snapshot could not be used for this comparison. Inspect the recorded configuration instead.</p>
            </template>
            <template v-else-if="changeState === 'none'">
              <h3>No configuration changes</h3>
              <p class="deployment-note">The recorded desired state matches its comparison snapshot.</p>
              <p class="meta">{{ comparison }}</p>
            </template>
            <template v-else>
              <div class="deployment-changes-heading"><div><h3>Desired-state changes</h3><p class="deployment-note">{{ comparison }} These entries do not confirm runtime convergence.</p></div><span>{{ total }} fields</span></div>
              <p class="meta">{{ detail.summary.added }} added · {{ detail.summary.changed }} changed · {{ detail.summary.removed }} removed<template v-if="detail.summary.redacted"> · {{ detail.summary.redacted }} masked</template></p>
              <details v-for="group in groups" :key="`${group.type}/${group.name}`" class="deployment-resource" open>
                <summary><span><span class="deployment-resource-type">{{ group.type }}</span><strong>{{ group.name }}</strong></span><span class="deployment-resource-operation" :class="`deployment-resource-operation--${group.operation}`">{{ group.operation }}</span></summary>
                <section v-for="section in group.sections" :key="section.name" class="deployment-change-section">
                  <h4>{{ sectionLabel(section.name) }}</h4>
                  <div v-for="(change, index) in section.changes" :key="`${change.field}-${index}`" class="deployment-change">
                    <strong class="deployment-field">{{ fieldLabel(change, section.name) }}</strong>
                    <div class="deployment-diff-values">
                      <div v-if="change.operation !== 'added'" class="deployment-diff-line deployment-diff-line--before"><span aria-hidden="true">−</span><code>{{ value(change.before, change.redacted) }}</code></div>
                      <div v-if="change.operation !== 'removed'" class="deployment-diff-line deployment-diff-line--after"><span aria-hidden="true">+</span><code>{{ value(change.after, change.redacted) }}</code></div>
                    </div>
                  </div>
                </section>
              </details>
            </template>
          </section>

          <section v-else class="deployment-tab-panel" role="tabpanel">
            <div class="deployment-configuration-heading"><div><h3>Recorded desired configuration</h3><p class="deployment-note">Sensitive values are masked before storage.</p></div><button v-if="configuration" type="button" class="button-ghost" @click="copyConfiguration">{{ copyLabel }}</button></div>
            <p v-if="!configuration" class="meta">Recorded configuration is unavailable.</p>
            <div v-else class="deployment-yaml manifest-code shj-lang-yaml shj-block" aria-label="Recorded desired configuration YAML" v-html="configurationHtml" />
          </section>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped>
.deployment-modal { width: min(960px, calc(100vw - 32px)); max-width: calc(100vw - 32px); min-width: 0; max-height: min(calc(100dvh - 32px), 940px); margin: max(16px, 4dvh) auto; display: flex; flex-direction: column; overflow: hidden; }
.deployment-modal-header { flex: 0 0 auto; align-items: flex-start; }
.deployment-modal-header > div { min-width: 0; }
.deployment-modal-header h2 { margin: 2px 0 0; overflow-wrap: anywhere; }
.deployment-modal-eyebrow { color: var(--muted); font-size: .72rem; font-weight: 700; letter-spacing: .08em; text-transform: uppercase; }
.deployment-modal-body { min-width: 0; min-height: 0; overflow: auto; }
.deployment-summary { border: 1px solid var(--line); border-radius: 10px; padding: 14px; background: var(--surface-card-soft); }
.deployment-summary-heading, .deployment-changes-heading, .deployment-configuration-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.deployment-summary-heading h3, .deployment-changes-heading h3, .deployment-configuration-heading h3 { margin: 0; }
.deployment-status { text-transform: capitalize; padding: 4px 9px; border-radius: 999px; background: var(--surface-muted); }
.deployment-status--succeeded { color: var(--success); background: var(--success-muted); }
.deployment-status--running { color: var(--info); background: var(--info-muted); }
.deployment-status--failed { color: var(--error); background: var(--error-muted); }
.deployment-status--interrupted { color: var(--warning); background: var(--warning-muted); }
.deployment-summary-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px 20px; margin: 14px 0 0; }
.deployment-summary-grid div { min-width: 0; }
.deployment-summary-grid dt { color: var(--muted); font-size: .72rem; text-transform: uppercase; }
.deployment-summary-grid dd { margin: 3px 0 0; overflow-wrap: anywhere; }
.deployment-commit-link { border: 0; background: transparent; color: var(--accent-strong); padding: 0; font: inherit; font-family: ui-monospace, monospace; }
.deployment-diagnostic { margin-top: 14px; border-left: 3px solid var(--error); border-radius: 5px; background: var(--error-muted); padding: 10px 12px; }
.deployment-diagnostic p { margin: 5px 0; white-space: pre-wrap; overflow-wrap: anywhere; }
.deployment-code { color: var(--muted); font-family: ui-monospace, monospace; font-size: .75rem; }
.deployment-note { color: var(--muted); line-height: 1.45; }
.deployment-tabs { display: flex; gap: 4px; margin-top: 16px; border-bottom: 1px solid var(--line); }
.deployment-tabs button { border: 0; border-bottom: 2px solid transparent; border-radius: 0; background: transparent; color: var(--muted); padding: 9px 12px; }
.deployment-tabs button.active { border-bottom-color: var(--accent); color: var(--text); }
.deployment-tab-panel { min-width: 0; padding-top: 16px; }
.deployment-tab-panel > h3 { margin-top: 0; }
.deployment-counts { display: flex; flex-wrap: wrap; gap: 8px; margin: 14px 0; }
.deployment-counts span { border: 1px solid var(--line); border-radius: 7px; padding: 7px 10px; color: var(--muted); }
.deployment-counts strong { color: var(--text); }
.deployment-service-overview { margin-top: 16px; }
.deployment-service-overview h4 { margin-bottom: 8px; }
.deployment-service-row { display: grid; grid-template-columns: minmax(120px, .8fr) minmax(0, 2fr) max-content; gap: 12px; border-top: 1px solid var(--line); padding: 9px 4px; font-size: .85rem; }
.deployment-service-row > * { min-width: 0; overflow-wrap: anywhere; }
.deployment-resource { border: 1px solid var(--line); border-radius: 8px; margin: 12px 0; overflow: hidden; }
.deployment-resource summary { display: flex; align-items: center; justify-content: space-between; gap: 12px; cursor: pointer; padding: 11px 12px; background: var(--surface-card-soft); }
.deployment-resource summary > span:first-child { display: flex; gap: 8px; min-width: 0; overflow-wrap: anywhere; }
.deployment-resource-type { color: var(--muted); text-transform: capitalize; }
.deployment-resource-operation { border-radius: 999px; padding: 2px 7px; font-size: .72rem; text-transform: capitalize; }
.deployment-resource-operation--added { color: var(--success); background: var(--success-muted); }
.deployment-resource-operation--removed { color: var(--error); background: var(--error-muted); }
.deployment-resource-operation--modified { color: var(--accent-strong); background: var(--surface-muted); }
.deployment-change-section { padding: 10px 12px; }
.deployment-change-section + .deployment-change-section { border-top: 1px solid var(--line); }
.deployment-change-section h4 { margin: 0 0 7px; color: var(--muted); font-size: .76rem; text-transform: capitalize; }
.deployment-change { display: grid; grid-template-columns: minmax(130px, .7fr) minmax(0, 2fr); gap: 12px; align-items: start; padding: 8px 0; border-top: 1px solid var(--line); }
.deployment-change:first-of-type { border-top: 0; }
.deployment-field { overflow-wrap: anywhere; font-size: .84rem; }
.deployment-diff-values { min-width: 0; display: grid; gap: 3px; }
.deployment-diff-line { min-width: 0; display: grid; grid-template-columns: 14px minmax(0, 1fr); gap: 5px; padding: 3px 6px; border-radius: 4px; }
.deployment-diff-line code { white-space: pre-wrap; overflow-wrap: anywhere; }
.deployment-diff-line--before { color: var(--error); background: var(--error-muted); }
.deployment-diff-line--after { color: var(--success); background: var(--success-muted); }
.deployment-configuration-heading > div { min-width: 0; }
.deployment-yaml { box-sizing: border-box; width: 100%; max-width: 100%; min-width: 0; max-height: 55vh; overflow: auto; border: 1px solid var(--line); border-radius: 7px; }
@media (max-width: 640px) {
  .deployment-modal { width: calc(100vw - 16px); max-width: calc(100vw - 16px); max-height: calc(100dvh - 16px); margin: 8px auto; }
  .deployment-summary-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .deployment-service-row, .deployment-change { grid-template-columns: minmax(0, 1fr); gap: 5px; }
  .deployment-service-row > :last-child { color: var(--muted); }
  .deployment-changes-heading, .deployment-configuration-heading { align-items: flex-start; flex-wrap: wrap; }
}
</style>
