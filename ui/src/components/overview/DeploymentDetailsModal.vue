<script setup lang="ts">
import yaml from "@speed-highlight/core/languages/yaml.js";
import { tokenizeWith } from "@speed-highlight/core/tokenize";
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { useRouter } from "vue-router";
import type { DeploymentChange } from "../../api/types";
import { useOverviewStore } from "../../stores/overview";
import { formatDate, shortCommitHash } from "../../utils/format";
import DeploymentIcon from "./DeploymentIcon.vue";

type DetailsTab = "changes" | "configuration";
type ChangeGroup = {
  type: string;
  name: string;
  operation: "added" | "modified" | "removed";
  sections: Array<{ name: string; changes: DeploymentChange[] }>;
};
type ResourceKey = "service" | "network" | "secret" | "config" | "volume";
type ResourceSection = {
  key: ResourceKey;
  label: string;
  count: number;
  groups: ChangeGroup[];
};

const store = useOverviewStore();
const router = useRouter();
const dialog = ref<HTMLElement | null>(null);
const closeButton = ref<HTMLButtonElement | null>(null);
const activeTab = ref<DetailsTab>("changes");
const copyLabel = ref("Copy YAML");
let returnFocus: HTMLElement | null = null;
const focusableSelector = [
  "a[href]", "button:not([disabled])", "input:not([disabled])", "select:not([disabled])",
  "textarea:not([disabled])", "summary", "[tabindex]:not([tabindex=\"-1\"])",
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

const initialServices = computed(() => detail.value?.services?.length ? detail.value.services.map((service) => ({
  name: service.name, image: service.image || "—", replicas: service.replicas || "—",
})) : groupsForResource("service").map((group) => {
  const changes = group.sections.flatMap((section) => section.changes);
  const field = (name: string) => changes.find((change) => change.field === name)?.after;
  return { name: group.name, image: field("image") || "—", replicas: field("deploy.replicas") || "—" };
}));

function groupsForResource(key: ResourceKey): ChangeGroup[] {
  return groups.value.filter((group) => group.type === key || group.type === `${key}s`);
}

function resourceIcon(type: string): ResourceKey {
  const normalized = type.toLowerCase().replace(/s$/, "");
  return (["service", "network", "secret", "config", "volume"] as ResourceKey[]).includes(normalized as ResourceKey)
    ? normalized as ResourceKey
    : "service";
}

const initialResourceSections = computed<ResourceSection[]>(() => {
  if (!detail.value) return [];
  const sections: Array<Omit<ResourceSection, "count" | "groups"> & { count: number }> = [
    { key: "service", label: "Services", count: detail.value.resources.services },
    { key: "network", label: "Networks", count: detail.value.resources.networks },
    { key: "secret", label: "Secrets", count: detail.value.resources.secrets },
    { key: "config", label: "Configs", count: detail.value.resources.configs },
    { key: "volume", label: "Volumes", count: detail.value.resources.volumes },
  ];
  return sections.filter((section) => section.key !== "volume" || section.count > 0).map((section) => ({
    ...section,
    groups: groupsForResource(section.key),
  }));
});

const comparison = computed(() => {
  switch (detail.value?.comparison_basis) {
    case "successful_baseline": return "Compared with the last successful desired snapshot.";
    case "last_attempt": return "Compared with the last attempted desired snapshot; the actual state may differ.";
    case "observed_state": return "Compared with an observed snapshot; live convergence is not verified.";
    default: return "No previous desired snapshot is available for comparison.";
  }
});
const statusTitle = computed(() => {
  switch (detail.value?.status) {
    case "failed": return "Apply pipeline failed.";
    case "interrupted": return "Apply pipeline was interrupted.";
    default: return "";
  }
});
const statusDescription = computed(() => {
  switch (detail.value?.status) {
    case "failed": return "Apply attempt failed. The actual Swarm state may differ from this desired configuration.";
    case "interrupted": return "Apply outcome is unknown. The actual Swarm state may differ from this desired configuration.";
    default: return "";
  }
});
const statusIcon = computed<"check" | "running" | "failed" | "warning">(() => {
  switch (detail.value?.status) {
    case "succeeded": return "check";
    case "running": return "running";
    case "failed": return "failed";
    default: return "warning";
  }
});
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
function pluralize(count: number, singular: string, plural = `${singular}s`): string {
  return `${count} ${count === 1 ? singular : plural}`;
}
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
function hasLongValue(change: DeploymentChange): boolean {
  return [change.before, change.after].some((item) => {
    const displayValue = value(item, change.redacted);
    return displayValue.length > 48 || displayValue.includes("\n");
  });
}
function focusableElements(): HTMLElement[] {
  return dialog.value ? [...dialog.value.querySelectorAll<HTMLElement>(focusableSelector)] : [];
}
function close() { store.closeDeploymentDetailsModal(); }
async function openStack(event: MouseEvent) {
  if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
  event.preventDefault();
  const stack = detail.value?.stack;
  if (!stack?.trim()) return;
  close();
  // Allow modal focus restoration and deep-link cleanup before changing the route.
  await nextTick();
  await router.push({ name: "stack-details", params: { stack } });
}
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
      <button ref="closeButton" class="modal-close" type="button" aria-label="Close deployment details" @click="close">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" aria-hidden="true"><path d="m7 7 10 10M17 7 7 17" /></svg>
      </button>
      <div class="modal-body deployment-modal-body">
        <header class="deployment-modal-header">
          <div class="deployment-heading-row">
            <div class="deployment-title">
              <div class="deployment-title-topline">
                <span class="deployment-modal-eyebrow">Deployment</span>
                <strong v-if="detail && !store.deploymentDetailsLoading && !store.deploymentDetailsError" class="deployment-status" :class="`deployment-status--${detail.status}`">
                  <span class="deployment-status-dot" aria-hidden="true" />
                  {{ statusLabel }}
                </strong>
              </div>
              <h2 id="deployment-details-title">{{ detail?.stack || "Deployment" }}</h2>
            </div>
          </div>
          <dl v-if="detail && !store.deploymentDetailsLoading && !store.deploymentDetailsError" class="deployment-metadata">
            <div class="deployment-metadata-item"><dt><DeploymentIcon name="stack" />Stack</dt><dd><RouterLink v-if="detail.stack?.trim()" v-slot="{ href }" custom :to="{ name: 'stack-details', params: { stack: detail.stack } }"><a class="deployment-stack-link" :href="href" @click="openStack">{{ detail.stack }}</a></RouterLink><span v-else>—</span></dd></div>
            <div class="deployment-metadata-item"><dt><DeploymentIcon name="commit" />Commit</dt><dd><button v-if="detail.commit" type="button" class="deployment-commit-link" @click="openCommit">{{ shortCommitHash(detail.commit) }}<span aria-hidden="true">↗</span></button><span v-else>—</span></dd></div>
            <div class="deployment-metadata-item"><dt><DeploymentIcon name="calendar" />Started</dt><dd>{{ formatDate(detail.started_at) }}</dd></div>
            <div class="deployment-metadata-item"><dt><DeploymentIcon name="calendar" />Finished</dt><dd>{{ detail.finished_at ? formatDate(detail.finished_at) : detail.status === "running" ? "In progress" : "—" }}</dd></div>
            <div class="deployment-metadata-item"><dt><DeploymentIcon name="clock" />Duration</dt><dd>{{ duration }}</dd></div>
            <div class="deployment-metadata-item"><dt><DeploymentIcon name="config" />Stage</dt><dd>{{ detail.phase }}</dd></div>
          </dl>
        </header>
        <main class="deployment-modal-content" :class="{ 'deployment-modal-content--compact': detail?.status === 'succeeded' || detail?.status === 'running' }">
          <p v-if="store.deploymentDetailsLoading" role="status" class="meta">Loading deployment details...</p>
          <div v-else-if="store.deploymentDetailsError" role="alert" class="deployment-state-message">
            <p>Could not load deployment details: {{ store.deploymentDetailsError }}</p>
            <button type="button" @click="store.openDeploymentDetailsModal(store.deploymentDetailsID)">Retry</button>
          </div>
          <p v-else-if="!detail" class="meta">Deployment details are unavailable.</p>
          <template v-else>
          <section
            v-if="detail.status === 'failed' || detail.status === 'interrupted'"
            class="deployment-outcome"
            :class="`deployment-outcome--${detail.status}`"
            aria-labelledby="deployment-outcome-title"
          >
            <span class="deployment-outcome-icon"><DeploymentIcon :name="statusIcon" /></span>
            <div>
              <strong id="deployment-outcome-title">{{ statusTitle }}</strong>
              <p>{{ statusDescription }}</p>
              <p class="deployment-diagnostic">{{ failureReason }}</p>
              <span v-if="detail.reason" class="deployment-code">Code: {{ detail.reason }}</span>
            </div>
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
                <span><DeploymentIcon name="service" /><strong>{{ pluralize(detail.resources.services, "service") }}</strong></span>
                <span><DeploymentIcon name="network" /><strong>{{ pluralize(detail.resources.networks, "network") }}</strong></span>
                <span><DeploymentIcon name="secret" /><strong>{{ pluralize(detail.resources.secrets, "secret") }}</strong></span>
                <span><DeploymentIcon name="config" /><strong>{{ pluralize(detail.resources.configs, "config") }}</strong></span>
                <span v-if="detail.resources.volumes"><DeploymentIcon name="volume" /><strong>{{ pluralize(detail.resources.volumes, "volume") }}</strong></span>
              </div>
              <div class="deployment-initial-resources">
                <details v-for="section in initialResourceSections" :key="section.key" class="deployment-resource-section" :open="section.key === 'service'">
                  <summary>
                    <svg class="deployment-chevron" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="m7 4 6 6-6 6" /></svg>
                    <DeploymentIcon :name="section.key" />
                    <strong>{{ section.label }}</strong>
                    <span class="deployment-resource-count">{{ section.count }}</span>
                  </summary>
                  <div v-if="section.key === 'service' && initialServices.length" class="deployment-service-table-wrap">
                    <table class="deployment-service-table">
                      <thead><tr><th>Name</th><th>Image</th><th>Replicas</th></tr></thead>
                      <tbody><tr v-for="service in initialServices" :key="service.name"><td><span class="deployment-table-name"><DeploymentIcon name="service" />{{ service.name }}</span></td><td :title="service.image">{{ service.image }}</td><td>{{ service.replicas }}</td></tr></tbody>
                    </table>
                  </div>
                  <ul v-else-if="section.groups.length" class="deployment-resource-list">
                    <li v-for="group in section.groups" :key="group.name"><DeploymentIcon :name="section.key" /><span>{{ group.name }}</span></li>
                  </ul>
                  <p v-else class="deployment-resource-empty">Detailed {{ section.label.toLowerCase() }} data is unavailable for this snapshot.</p>
                </details>
              </div>
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
              <div class="deployment-changes-heading"><div><h3>Desired-state changes</h3><p class="deployment-note">{{ comparison }} These entries do not confirm runtime convergence.</p></div></div>
              <p class="meta">{{ detail.summary.added }} added · {{ detail.summary.changed }} changed · {{ detail.summary.removed }} removed<template v-if="detail.summary.redacted"> · {{ detail.summary.redacted }} masked</template></p>
              <details v-for="group in groups" :key="`${group.type}/${group.name}`" class="deployment-resource" open>
                <summary><span><svg class="deployment-chevron" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="m7 4 6 6-6 6" /></svg><DeploymentIcon :name="resourceIcon(group.type)" /><span><span class="deployment-resource-type">{{ group.type }}</span><strong>{{ group.name }}</strong></span></span><span class="deployment-resource-operation" :class="`deployment-resource-operation--${group.operation}`">{{ group.operation }}</span></summary>
                <section v-for="section in group.sections" :key="section.name" class="deployment-change-section">
                  <h4>{{ sectionLabel(section.name) }}</h4>
                  <div
                    v-for="(change, index) in section.changes"
                    :key="`${change.field}-${index}`"
                    class="deployment-change"
                    :class="{ 'deployment-change--long': hasLongValue(change) }"
                  >
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
        </main>
      </div>
    </div>
  </div>
</template>

<style scoped>
.deployment-modal {
  width: min(1440px, calc(100vw - 64px));
  max-width: calc(100vw - 64px);
  min-width: 0;
  max-height: min(85dvh, calc(100dvh - 64px));
  margin: max(32px, 7.5dvh) auto;
  display: grid;
  grid-template-rows: minmax(0, 1fr);
  overflow: hidden;
  border-color: var(--border-default);
  border-radius: 18px;
  /* An opaque surface prevents underlying page content bleeding through the dialog. */
  background: var(--shell-chrome-bg);
  padding: 0;
  box-shadow: var(--shadow-popover);
}
.deployment-modal-body { min-width: 0; min-height: 0; display: grid; grid-template-rows: auto minmax(0, 1fr); overflow: hidden; padding: 0; }
.deployment-modal-content { min-width: 0; min-height: 0; overflow: auto; padding: 14px 18px 10px; }
/* When there is no diagnostic banner, put the tabs directly below the metadata. */
.deployment-modal-content--compact { padding-top: 6px; }
.deployment-modal-content--compact .deployment-tabs { margin-top: 0; }
.deployment-state-message { padding: 24px 0; }
.deployment-modal-header { padding: 14px 56px 12px 18px; border-bottom: 1px solid var(--line); }
.deployment-heading-row { min-width: 0; }
.deployment-title { min-width: 0; }
.deployment-title-topline { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; min-width: 0; }
.deployment-title h2 { margin: 4px 0 0; font-size: 1.45rem; line-height: 1.2; overflow-wrap: anywhere; }
.deployment-modal-eyebrow { color: var(--muted); font-size: .76rem; font-weight: 700; letter-spacing: .08em; text-transform: uppercase; }
.deployment-status {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex: 0 0 auto;
  padding: 0;
  font-size: .8rem;
  font-weight: 600;
  line-height: 1.25;
}
.deployment-status-dot { width: 7px; height: 7px; flex: 0 0 auto; border-radius: 50%; background: currentColor; }
.deployment-status--succeeded { color: var(--success); }
.deployment-status--running { color: var(--info); }
.deployment-status--failed { color: var(--error); }
.deployment-status--interrupted { color: var(--warning); }
.deployment-metadata {
  display: grid;
  grid-template-columns: minmax(140px, 1fr) minmax(130px, .9fr) minmax(190px, 1.35fr) minmax(190px, 1.35fr) minmax(120px, .75fr) minmax(130px, .8fr);
  margin: 12px 0 0;
}
.deployment-metadata-item { min-width: 0; position: relative; padding: 0 18px 0 52px; border-left: 1px solid var(--line); }
.deployment-metadata-item:first-child { padding-left: 0; border-left: 0; }
.deployment-metadata-item:last-child { padding-right: 0; }
.deployment-metadata-item:first-child dt, .deployment-metadata-item:first-child dd { padding-left: 34px; }
.deployment-metadata dt { color: var(--muted); font-size: .78rem; line-height: 1.2; }
.deployment-metadata dt > :deep(.deployment-icon) { position: absolute; left: 18px; top: 50%; width: 22px; height: 22px; color: var(--muted); transform: translateY(-50%); }
.deployment-metadata-item:first-child dt > :deep(.deployment-icon) { left: 0; }
.deployment-metadata dd { margin: 4px 0 0; overflow-wrap: anywhere; line-height: 1.25; }
.deployment-stack-link { color: var(--accent-strong); text-decoration: none; }
.deployment-stack-link:hover { text-decoration: underline; }
.deployment-commit-link { display: inline-flex; align-items: center; gap: 7px; border: 0; background: transparent; color: var(--control-primary); padding: 0; font: inherit; font-family: ui-monospace, monospace; }
.deployment-commit-link:hover { transform: none; filter: none; text-decoration: underline; }
.deployment-commit-link span { font-family: inherit; font-size: .95rem; }
:global(:root[data-theme="dark"] .deployment-commit-link) { color: var(--text); }
:global(:root[data-theme="dark"] .deployment-commit-link span) { color: var(--accent-strong); }
.deployment-outcome {
  display: grid;
  grid-template-columns: 46px minmax(0, 1fr);
  gap: 12px;
  align-items: start;
  margin-top: 0;
  border: 1px solid var(--line);
  border-radius: 13px;
  padding: 14px 16px;
  background: var(--surface-muted);
}
.deployment-outcome--failed { border-color: rgba(248, 113, 113, .18); background: linear-gradient(90deg, rgba(126, 45, 45, .2), rgba(126, 45, 45, .08)); }
.deployment-outcome--interrupted { border-color: rgba(250, 204, 21, .18); background: linear-gradient(90deg, rgba(161, 98, 7, .18), rgba(161, 98, 7, .07)); }
.deployment-outcome-icon { width: 36px; height: 36px; display: grid; place-items: center; border-radius: 50%; }
.deployment-outcome--failed .deployment-outcome-icon { color: var(--text-on-status); background: var(--status-error-bg); }
.deployment-outcome--interrupted .deployment-outcome-icon { color: var(--text-on-status); background: var(--status-warning-bg); }
.deployment-outcome-icon :deep(.deployment-icon) { width: 22px; height: 22px; stroke-width: 2.2; }
.deployment-outcome strong { display: block; font-size: 1rem; }
.deployment-outcome p { margin: 5px 0 0; color: var(--muted); line-height: 1.4; }
.deployment-outcome .deployment-diagnostic { color: var(--text); white-space: pre-wrap; overflow-wrap: anywhere; }
.deployment-code { display: inline-block; margin-top: 7px; color: var(--muted); font-family: ui-monospace, monospace; font-size: .75rem; }
.deployment-note { color: var(--muted); line-height: 1.45; }
.deployment-tabs { display: flex; gap: 6px; margin-top: 14px; border-bottom: 1px solid var(--line); }
.deployment-tabs button { border: 0; border-bottom: 3px solid transparent; border-radius: 0; background: transparent; color: var(--muted); padding: 8px 8px 9px; }
.deployment-tabs button:hover { color: var(--text); transform: none; filter: none; }
.deployment-tabs button.active { border-bottom-color: var(--control-primary); color: var(--control-primary); }
:global(:root[data-theme="dark"] .deployment-tabs button.active) { color: var(--text); }
.deployment-tab-panel { min-width: 0; padding: 14px 0 4px; }
.deployment-tab-panel > h3 { margin: 0 0 4px; font-size: 1.35rem; }
.deployment-tab-panel > .deployment-note { margin: 0; }
.deployment-counts { display: flex; flex-wrap: wrap; gap: 8px; margin: 10px 0 14px; }
.deployment-counts > span { display: inline-flex; align-items: center; gap: 10px; min-height: 46px; border: 1px solid var(--border-default); border-radius: 10px; padding: 8px 12px; background: var(--surface-card-soft); color: var(--muted); }
.deployment-counts :deep(.deployment-icon) { width: 23px; height: 23px; color: var(--accent-strong); }
.deployment-counts strong { color: var(--text); font-size: .9rem; }
.deployment-initial-resources { border-top: 1px solid var(--line); }
.deployment-resource-section { border-bottom: 1px solid var(--line); }
.deployment-resource-section summary {
  min-height: 44px;
  display: flex;
  align-items: center;
  gap: 11px;
  cursor: pointer;
  list-style: none;
  padding: 8px 2px;
}
.deployment-resource-section summary::-webkit-details-marker, .deployment-resource summary::-webkit-details-marker { display: none; }
.deployment-resource-section summary:hover { background: var(--surface-hover-soft); }
.deployment-resource-section summary > :deep(.deployment-icon) { width: 23px; height: 23px; color: var(--muted); }
.deployment-resource-section summary strong { font-size: .98rem; }
.deployment-chevron { width: 18px; height: 18px; transition: transform 140ms ease; }
details[open] > summary .deployment-chevron { transform: rotate(90deg); }
.deployment-resource-count { min-width: 27px; border-radius: 999px; background: var(--surface-muted-strong); padding: 3px 8px; color: var(--text); font-size: .75rem; text-align: center; }
.deployment-service-table-wrap { margin: 0 0 14px 33px; overflow-x: auto; border: 1px solid var(--border-subtle); border-radius: 8px; }
.deployment-service-table { width: 100%; min-width: 620px; border-collapse: collapse; table-layout: fixed; font-size: .84rem; }
.deployment-service-table th { color: var(--muted); background: var(--surface-panel-header); font-weight: 500; text-align: left; }
.deployment-service-table th, .deployment-service-table td { padding: 8px 10px; border-bottom: 1px solid var(--line); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.deployment-service-table th:first-child { width: 30%; }
.deployment-service-table th:nth-child(2) { width: 58%; }
.deployment-service-table th:last-child, .deployment-service-table td:last-child { width: 12%; text-align: right; }
.deployment-service-table tbody tr:last-child td { border-bottom: 0; }
.deployment-table-name { display: inline-flex; align-items: center; gap: 10px; min-width: 0; }
.deployment-table-name :deep(.deployment-icon) { width: 21px; height: 21px; color: var(--muted); }
.deployment-resource-list { margin: 0 0 12px 33px; padding: 0; list-style: none; border: 1px solid var(--border-subtle); border-radius: 8px; overflow: hidden; }
.deployment-resource-list li { display: flex; align-items: center; gap: 10px; border-top: 1px solid var(--line); padding: 7px 10px; }
.deployment-resource-list li:first-child { border-top: 0; }
.deployment-resource-list :deep(.deployment-icon) { width: 20px; height: 20px; color: var(--muted); }
.deployment-resource-empty { margin: 0 0 12px 33px; color: var(--muted); font-size: .82rem; }
.deployment-changes-heading, .deployment-configuration-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.deployment-changes-heading h3, .deployment-configuration-heading h3 { margin: 0; }
.deployment-resource { border-bottom: 1px solid var(--line); margin: 10px 0; overflow: hidden; }
.deployment-resource summary { display: flex; align-items: center; justify-content: space-between; gap: 12px; cursor: pointer; list-style: none; padding: 8px 4px; }
.deployment-resource summary > span:first-child { display: flex; align-items: center; gap: 10px; min-width: 0; overflow-wrap: anywhere; }
.deployment-resource summary > span:first-child > :deep(.deployment-icon) { width: 22px; height: 22px; color: var(--muted); }
.deployment-resource summary > span:first-child > span { display: flex; align-items: baseline; gap: 8px; min-width: 0; }
.deployment-resource-type { color: var(--muted); text-transform: capitalize; }
.deployment-resource-operation { border-radius: 999px; padding: 3px 8px; font-size: .72rem; text-transform: capitalize; }
.deployment-resource-operation--added { color: var(--success); background: var(--success-muted); }
.deployment-resource-operation--removed { color: var(--error); background: var(--error-muted); }
.deployment-resource-operation--modified { color: var(--accent-strong); background: var(--surface-muted); }
.deployment-change-section { margin-left: 32px; padding: 8px 10px; border: 1px solid var(--border-subtle); border-bottom: 0; }
.deployment-change-section:last-child { border-bottom: 1px solid var(--border-subtle); border-radius: 0 0 8px 8px; margin-bottom: 12px; }
.deployment-change-section h4 { margin: 0 0 7px; color: var(--muted); font-size: .76rem; text-transform: capitalize; }
.deployment-change { display: grid; grid-template-columns: minmax(110px, 180px) minmax(0, 1fr); gap: 12px; align-items: start; padding: 8px 0; border-top: 1px solid var(--line); }
.deployment-change--long { grid-template-columns: minmax(0, 1fr); gap: 10px; }
.deployment-change:first-of-type { border-top: 0; }
.deployment-field { overflow-wrap: anywhere; font-size: .84rem; }
.deployment-diff-values { min-width: 0; display: grid; gap: 3px; }
.deployment-diff-line { min-width: 0; display: grid; grid-template-columns: 14px minmax(0, 1fr); gap: 5px; padding: 4px 7px; border-radius: 4px; }
.deployment-diff-line code { white-space: pre-wrap; overflow-wrap: anywhere; }
.deployment-diff-line--before { color: var(--error); background: var(--error-muted); }
.deployment-diff-line--after { color: var(--success); background: var(--success-muted); }
.deployment-configuration-heading > div { min-width: 0; }
.deployment-yaml { box-sizing: border-box; width: 100%; max-width: 100%; min-width: 0; max-height: 55vh; overflow: auto; border: 1px solid var(--border-default); border-radius: 8px; background: var(--surface-code); }
@media (max-width: 1100px) {
  .deployment-metadata { grid-template-columns: repeat(3, minmax(0, 1fr)); row-gap: 18px; }
  .deployment-metadata-item:nth-child(4) { padding-left: 0; border-left: 0; }
}
@media (max-width: 700px) {
  .deployment-modal {
    width: calc(100vw - 12px);
    max-width: calc(100vw - 12px);
    height: calc(100dvh - 12px);
    max-height: calc(100dvh - 12px);
    margin: 6px auto;
    border-radius: 12px;
  }
  .deployment-modal-content { padding: 10px 12px 12px; }
  .deployment-modal-content--compact { padding-top: 4px; }
  .deployment-modal-header { padding: 12px 12px 10px; }
  .deployment-heading-row { padding-right: 44px; }
  .deployment-title h2 { font-size: 1.25rem; }
  .deployment-status { font-size: .78rem; }

  /* Compact two-column metadata leaves room for the actual deployment diff. */
  .deployment-metadata { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; margin-top: 12px; }
  .deployment-metadata-item,
  .deployment-metadata-item:first-child,
  .deployment-metadata-item:nth-child(4) {
    min-height: 60px;
    padding: 9px 10px;
    border: 1px solid var(--border-subtle);
    border-radius: 9px;
    background: var(--surface-muted);
  }
  .deployment-metadata-item:first-child dt,
  .deployment-metadata-item:first-child dd { padding-left: 0; }
  .deployment-metadata dt { font-size: .72rem; }
  .deployment-metadata dt > :deep(.deployment-icon) { display: none; }
  .deployment-metadata dd { margin-top: 5px; font-size: .82rem; }
  .deployment-metadata-item:nth-child(3) dd,
  .deployment-metadata-item:nth-child(4) dd { font-size: .78rem; font-variant-numeric: tabular-nums; }

  .deployment-outcome { grid-template-columns: 34px minmax(0, 1fr); gap: 12px; padding: 14px; }
  .deployment-outcome-icon { width: 32px; height: 32px; }
  .deployment-tabs { margin-top: 12px; }
  .deployment-tab-panel { padding-top: 12px; }
  .deployment-counts > span { flex: 1 1 calc(50% - 10px); min-width: 0; gap: 8px; padding: 9px 10px; }
  .deployment-service-table-wrap, .deployment-resource-list, .deployment-resource-empty, .deployment-change-section { margin-left: 0; }
  .deployment-change { grid-template-columns: minmax(0, 1fr); gap: 5px; }
  .deployment-changes-heading, .deployment-configuration-heading { align-items: flex-start; flex-wrap: wrap; }
}
</style>
