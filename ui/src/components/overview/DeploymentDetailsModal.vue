<script setup lang="ts">
import yaml from "@speed-highlight/core/languages/yaml.js";
import { tokenizeWith } from "@speed-highlight/core/tokenize";
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { fetchDeploymentInitJobLogs } from "../../api/overview";
import type { DeploymentAttempt, DeploymentChange, DeploymentInitJobExecution } from "../../api/types";
import { useAssistantStore } from "../../stores/assistant";
import { useOverviewStore } from "../../stores/overview";
import { useUIStore } from "../../stores/ui";
import { formatDate, shortCommitHash } from "../../utils/format";
import DeploymentIcon from "./DeploymentIcon.vue";

type DetailsTab = "attempts" | "changes" | "configuration";
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
const assistantStore = useAssistantStore();
const uiStore = useUIStore();
const dialog = ref<HTMLElement | null>(null);
const closeButton = ref<HTMLButtonElement | null>(null);
function defaultTab(status?: string): DetailsTab {
  return status === "succeeded" ? "changes" : "attempts";
}

const activeTab = ref<DetailsTab>(defaultTab(store.deploymentDetailsData?.status));
const tabSelectionInitialized = ref(Boolean(store.deploymentDetailsData));
const tabManuallySelected = ref(false);
const copyLabel = ref("Copy YAML");
const copiedAttemptID = ref("");
type InitJobLogState = { open: boolean; loading: boolean; loaded: boolean; logs: string; error: string };
const initJobLogs = ref<Record<string, InitJobLogState>>({});
const infoOpen = ref<DetailsTab | null>(null);
const infoPinned = ref(false);

function dismissInfo() {
  infoOpen.value = null;
  infoPinned.value = false;
}
function showInfo(tab: DetailsTab, event: PointerEvent) {
  if (event.pointerType === "mouse" && !infoPinned.value) infoOpen.value = tab;
}
function hideInfo(tab: DetailsTab, event: PointerEvent) {
  if (event.pointerType === "mouse" && !infoPinned.value && infoOpen.value === tab) infoOpen.value = null;
}
function toggleInfo(tab: DetailsTab) {
  if (infoOpen.value === tab && infoPinned.value) {
    dismissInfo();
  } else {
    infoOpen.value = tab;
    infoPinned.value = true;
  }
}
function onPointerDown(event: PointerEvent) {
  if (infoOpen.value && !(event.target as Element | null)?.closest(".deployment-info")) dismissInfo();
}

let returnFocus: HTMLElement | null = null;
const focusableSelector = [
  "a[href]", "button:not([disabled])", "input:not([disabled])", "select:not([disabled])",
  "textarea:not([disabled])", "summary", "[tabindex]:not([tabindex=\"-1\"])",
].join(", ");
const detail = computed(() => store.deploymentDetailsData);
const attempts = computed(() => [...(detail.value?.attempts ?? [])].sort((left, right) =>
  right.number - left.number || Date.parse(right.started_at) - Date.parse(left.started_at),
));
const attemptCount = computed(() => detail.value?.attempt_count ?? attempts.value.length);
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
    case "superseded": return "Desired state was superseded.";
    default: return "";
  }
});
const statusDescription = computed(() => {
  switch (detail.value?.status) {
    case "failed": return "Apply attempt failed. The actual Swarm state may differ from this desired configuration.";
    case "interrupted": return "Apply outcome is unknown. The actual Swarm state may differ from this desired configuration.";
    case "superseded": return "A newer desired state replaced this deployment. Its attempt history is preserved below.";
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
// Keep date and time legible within the two-column mobile metadata layout.
function formatCompactDate(raw: string): string {
  const parsed = new Date(raw);
  if (Number.isNaN(parsed.valueOf())) return raw;
  const twoDigits = (value: number) => String(value).padStart(2, "0");
  return `${twoDigits(parsed.getDate())}.${twoDigits(parsed.getMonth() + 1)}.${twoDigits(parsed.getFullYear() % 100)} ${twoDigits(parsed.getHours())}:${twoDigits(parsed.getMinutes())}:${twoDigits(parsed.getSeconds())}`;
}
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
    case "superseded": return "Superseded";
    default: return "Unknown";
  }
});
function attemptDuration(attempt: DeploymentAttempt): string {
  let milliseconds = attempt.duration_ms;
  if (milliseconds == null && attempt.finished_at) {
    const started = Date.parse(attempt.started_at);
    const finished = Date.parse(attempt.finished_at);
    if (!Number.isNaN(started) && !Number.isNaN(finished)) milliseconds = Math.max(0, finished - started);
  }
  if (milliseconds == null) return attempt.status === "running" ? "In progress" : "—";
  return milliseconds < 1000 ? `${milliseconds} ms` : `${(milliseconds / 1000).toFixed(1)} s`;
}
function initJobDuration(execution: DeploymentInitJobExecution): string {
  const started = Date.parse(execution.started_at);
  const finished = Date.parse(execution.finished_at);
  if (Number.isNaN(started) || Number.isNaN(finished)) return "—";
  const milliseconds = Math.max(0, finished - started);
  return milliseconds < 1000 ? `${milliseconds} ms` : `${(milliseconds / 1000).toFixed(1)} s`;
}
function attemptExpandable(attempt: DeploymentAttempt): boolean {
  return attempt.status !== "succeeded" || (attempt.init_jobs?.length ?? 0) > 0;
}
function initJobKey(attempt: DeploymentAttempt, execution: DeploymentInitJobExecution): string {
  return `${attempt.id}:${execution.ordinal}`;
}
function initJobLogState(attempt: DeploymentAttempt, execution: DeploymentInitJobExecution): InitJobLogState | undefined {
  return initJobLogs.value[initJobKey(attempt, execution)];
}
async function toggleInitJobLogs(attempt: DeploymentAttempt, execution: DeploymentInitJobExecution) {
  if (!detail.value || !execution.logs_available) return;
  const key = initJobKey(attempt, execution);
  const current = initJobLogs.value[key];
  if (current?.open) {
    current.open = false;
    return;
  }
  if (current?.loaded) {
    current.open = true;
    return;
  }
  initJobLogs.value[key] = { open: true, loading: true, loaded: false, logs: "", error: "" };
  try {
    const logs = await fetchDeploymentInitJobLogs(detail.value.id, attempt.id, execution.ordinal);
    initJobLogs.value[key] = { open: true, loading: false, loaded: true, logs, error: "" };
  } catch (error) {
    initJobLogs.value[key] = {
      open: true, loading: false, loaded: false, logs: "",
      error: error instanceof Error ? error.message : "Failed to load Init Job logs",
    };
  }
}
function attemptDiagnostic(attempt: DeploymentAttempt): string {
  return attempt.diagnostic || reasonLabels[attempt.reason ?? ""] || "No diagnostics were recorded.";
}
function selectTab(tab: DetailsTab) {
  activeTab.value = tab;
  tabManuallySelected.value = true;
}
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
async function copyAttemptDiagnostic(attempt: DeploymentAttempt) {
  await navigator.clipboard?.writeText(attemptDiagnostic(attempt));
  copiedAttemptID.value = attempt.id;
  window.setTimeout(() => {
    if (copiedAttemptID.value === attempt.id) copiedAttemptID.value = "";
  }, 1200);
}
async function explainAttempt(attempt: DeploymentAttempt) {
  if (!detail.value || !assistantStore.enabled || assistantStore.pending) return;

  const changedServiceNames = detail.value.changes
    .filter((change) => change.resourceType.replace(/s$/, "") === "service")
    .map((change) => change.resourceName)
    .filter(Boolean);
  const snapshotServiceNames = (detail.value.services ?? []).map((service) => service.name).filter(Boolean);
  const serviceNames = [...new Set(changedServiceNames.length > 0 ? changedServiceNames : snapshotServiceNames)];
  const message = [
    "Explain this deployment error.",
    "",
    `deployment_id: ${detail.value.id}`,
    `stack_name: ${detail.value.stack}`,
    `service_name: ${serviceNames.join(", ") || "unknown"}`,
    "error:",
    attemptDiagnostic(attempt),
  ].join("\n");

  returnFocus = null;
  close();
  assistantStore.newChat();
  uiStore.openAssistantDrawer();
  await assistantStore.sendMessage(message);
}
function onKeydown(event: KeyboardEvent) {
  if (!store.deploymentDetailsModalOpen || store.commitDetailsModalOpen) return;
  if (event.key === "Escape") {
    event.preventDefault();
    if (infoOpen.value) { dismissInfo(); return; }
    close();
    return;
  }
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
    tabManuallySelected.value = false;
    tabSelectionInitialized.value = Boolean(detail.value);
    activeTab.value = defaultTab(detail.value?.status);
    copiedAttemptID.value = "";
    initJobLogs.value = {};
    dismissInfo();
    await nextTick(); closeButton.value?.focus();
  } else if (!open && wasOpen) {
    initJobLogs.value = {};
    dismissInfo();
    if (returnFocus?.isConnected) returnFocus.focus();
    returnFocus = null;
  }
});
watch(detail, (current) => {
  if (!store.deploymentDetailsModalOpen || !current || tabSelectionInitialized.value || tabManuallySelected.value) return;
  activeTab.value = defaultTab(current.status);
  tabSelectionInitialized.value = true;
});
watch(activeTab, dismissInfo);
onMounted(() => {
  document.addEventListener("keydown", onKeydown);
  document.addEventListener("pointerdown", onPointerDown);
});
onUnmounted(() => {
  document.removeEventListener("keydown", onKeydown);
  document.removeEventListener("pointerdown", onPointerDown);
});
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
            <div class="deployment-metadata-item"><dt><DeploymentIcon name="stack" />Stack</dt><dd>{{ detail.stack }}</dd></div>
            <div class="deployment-metadata-item"><dt><DeploymentIcon name="commit" />Commit</dt><dd><button v-if="detail.commit" type="button" class="deployment-commit-link" @click="openCommit">{{ shortCommitHash(detail.commit) }}<span aria-hidden="true">↗</span></button><span v-else>—</span></dd></div>
            <div class="deployment-metadata-item"><dt><DeploymentIcon name="calendar" />Started</dt><dd><span class="deployment-date-desktop">{{ formatDate(detail.started_at) }}</span><span class="deployment-date-mobile">{{ formatCompactDate(detail.started_at) }}</span></dd></div>
            <div class="deployment-metadata-item"><dt><DeploymentIcon name="calendar" />Finished</dt><dd><span class="deployment-date-desktop">{{ detail.finished_at ? formatDate(detail.finished_at) : detail.status === "running" ? "In progress" : "—" }}</span><span class="deployment-date-mobile">{{ detail.finished_at ? formatCompactDate(detail.finished_at) : detail.status === "running" ? "In progress" : "—" }}</span></dd></div>
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
            v-if="detail.status === 'failed' || detail.status === 'interrupted' || detail.status === 'superseded'"
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
            <button type="button" role="tab" :aria-selected="activeTab === 'attempts'" :class="{ active: activeTab === 'attempts' }" @click="selectTab('attempts')">Attempts <span class="deployment-tab-count">{{ attemptCount }}</span></button>
            <button type="button" role="tab" :aria-selected="activeTab === 'changes'" :class="{ active: activeTab === 'changes' }" @click="selectTab('changes')">Changes</button>
            <button type="button" role="tab" :aria-selected="activeTab === 'configuration'" :class="{ active: activeTab === 'configuration' }" @click="selectTab('configuration')">Configuration</button>
          </div>

          <section v-if="activeTab === 'attempts'" class="deployment-tab-panel deployment-attempts-panel" role="tabpanel">
            <p v-if="attempts.length === 0" class="meta">No deployment attempts were recorded.</p>
            <template v-else>
              <div class="deployment-attempt-columns" aria-hidden="true">
                <span />
                <span>Started</span>
                <span>Stage</span>
                <span>Duration</span>
                <span />
              </div>
              <div class="deployment-attempt-list">
                <component
                  :is="attemptExpandable(attempt) ? 'details' : 'div'"
                  v-for="attempt in attempts"
                  :key="attempt.id"
                  class="deployment-attempt-row"
                  :class="{ 'deployment-attempt-row--static': !attemptExpandable(attempt) }"
                >
                  <component :is="attemptExpandable(attempt) ? 'summary' : 'div'" class="deployment-attempt-summary">
                    <span class="deployment-attempt-status" :class="`deployment-attempt-status--${attempt.status}`" role="img" :aria-label="`${attempt.status} attempt`" />
                    <time :datetime="attempt.started_at">{{ formatDate(attempt.started_at) }}</time>
                    <span class="deployment-attempt-stage">{{ sectionLabel(attempt.phase) }}</span>
                    <span class="deployment-attempt-duration">{{ attemptDuration(attempt) }}</span>
                    <svg v-if="attemptExpandable(attempt)" class="deployment-chevron" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="m7 4 6 6-6 6" /></svg>
                  </component>
                  <div v-if="attemptExpandable(attempt)" class="deployment-attempt-details">
                    <section v-if="attempt.init_jobs?.length" class="deployment-init-jobs" aria-label="Init Job executions">
                      <h4>Init Jobs</h4>
                      <div v-for="execution in attempt.init_jobs" :key="execution.ordinal" class="deployment-init-job">
                        <div class="deployment-init-job-summary">
                          <span class="deployment-init-job-identity">
                            <span class="deployment-init-job-title">
                              <strong>{{ execution.job_name }}</strong>
                              <span class="deployment-attempt-status" :class="`deployment-attempt-status--${execution.status}`" role="img" :aria-label="`${execution.status} Init Job`" />
                            </span>
                            <small>{{ execution.service_name }}</small>
                          </span>
                          <span class="deployment-attempt-duration">{{ initJobDuration(execution) }}</span>
                          <span v-if="execution.logs_truncated" class="deployment-init-job-badge">Last 64 KiB</span>
                          <button
                            v-if="execution.logs_available"
                            type="button"
                            class="deployment-init-job-logs-button"
                            :aria-label="`${initJobLogState(attempt, execution)?.open ? 'Hide' : 'View'} logs for Init Job ${execution.job_name}`"
                            :aria-expanded="Boolean(initJobLogState(attempt, execution)?.open)"
                            :title="initJobLogState(attempt, execution)?.open ? 'Hide logs' : 'View logs'"
                            @click.stop="toggleInitJobLogs(attempt, execution)"
                          >
                            <svg viewBox="0 0 16 16" aria-hidden="true" focusable="false">
                              <path d="M3 2.5A1.5 1.5 0 0 1 4.5 1h5.8c.4 0 .78.16 1.06.44l1.2 1.2c.28.28.44.66.44 1.06v9.8A1.5 1.5 0 0 1 11.5 15h-7A1.5 1.5 0 0 1 3 13.5v-11Zm1.5-.5a.5.5 0 0 0-.5.5v11a.5.5 0 0 0 .5.5h7a.5.5 0 0 0 .5-.5V4H10.5A1.5 1.5 0 0 1 9 2.5V2H4.5ZM10 2.2v.3a.5.5 0 0 0 .5.5h1.3L10 2.2ZM5.5 6a.5.5 0 0 1 .5-.5h4a.5.5 0 0 1 0 1H6a.5.5 0 0 1-.5-.5Zm0 2.25a.5.5 0 0 1 .5-.5h4a.5.5 0 0 1 0 1H6a.5.5 0 0 1-.5-.5Zm0 2.25A.5.5 0 0 1 6 10h2.5a.5.5 0 0 1 0 1H6a.5.5 0 0 1-.5-.5Z" fill="currentColor" />
                            </svg>
                          </button>
                          <span v-else class="deployment-init-job-unavailable">Logs unavailable</span>
                        </div>
                        <div v-if="initJobLogState(attempt, execution)?.open" class="deployment-init-job-logs">
                          <p v-if="initJobLogState(attempt, execution)?.loading" class="meta">Loading logs...</p>
                          <p v-else-if="initJobLogState(attempt, execution)?.error" role="alert">{{ initJobLogState(attempt, execution)?.error }}</p>
                          <p v-else-if="!initJobLogState(attempt, execution)?.logs" class="meta">Log output is empty.</p>
                          <pre v-else><code>{{ initJobLogState(attempt, execution)?.logs }}</code></pre>
                        </div>
                      </div>
                    </section>
                    <div v-if="attempt.status !== 'succeeded'" class="deployment-attempt-details-heading">
                      <strong>Diagnostics</strong>
                      <div class="deployment-attempt-details-actions">
                        <button type="button" class="button-ghost" :disabled="!assistantStore.enabled || assistantStore.pending" @click="explainAttempt(attempt)">Explain</button>
                        <button type="button" class="button-ghost" @click="copyAttemptDiagnostic(attempt)">{{ copiedAttemptID === attempt.id ? "Copied" : "Copy" }}</button>
                      </div>
                    </div>
                    <pre v-if="attempt.status !== 'succeeded'"><code>{{ attemptDiagnostic(attempt) }}</code></pre>
                  </div>
                </component>
              </div>
            </template>
          </section>

          <section v-else-if="activeTab === 'changes'" class="deployment-tab-panel" role="tabpanel">
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
              <div class="deployment-changes-heading">
                <div class="deployment-info-heading">
                  <h3>Desired Changes</h3>
                  <div class="deployment-info" @pointerenter="showInfo('changes', $event)" @pointerleave="hideInfo('changes', $event)">
                    <button type="button" class="deployment-info-button" aria-label="About desired changes" :aria-expanded="infoOpen === 'changes'" :aria-describedby="infoOpen === 'changes' ? 'deployment-changes-tooltip' : undefined" @click.stop="toggleInfo('changes')">
                      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><circle cx="12" cy="12" r="9" /><path d="M12 11v5M12 8h.01" stroke-linecap="round" /></svg>
                    </button>
                    <div v-if="infoOpen === 'changes'" id="deployment-changes-tooltip" class="deployment-info-tooltip" role="tooltip">{{ comparison }} These entries do not confirm runtime convergence.</div>
                  </div>
                </div>
              </div>
              <p class="meta">{{ detail.summary.added }} added · {{ detail.summary.changed }} changed · {{ detail.summary.removed }} removed<template v-if="detail.summary.redacted"> · {{ detail.summary.redacted }} masked</template></p>
              <template v-for="group in groups" :key="`${group.type}/${group.name}`">
                <div v-if="group.operation === 'removed'" class="deployment-resource deployment-resource--removed">
                  <div class="deployment-resource-heading">
                    <span><DeploymentIcon :name="resourceIcon(group.type)" /><span><span class="deployment-resource-type">{{ group.type }}</span><strong>{{ group.name }}</strong></span></span>
                    <span class="deployment-resource-operation deployment-resource-operation--removed">Removed</span>
                  </div>
                </div>
                <details v-else class="deployment-resource" open>
                  <summary class="deployment-resource-heading"><span><svg class="deployment-chevron" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="m7 4 6 6-6 6" /></svg><DeploymentIcon :name="resourceIcon(group.type)" /><span><span class="deployment-resource-type">{{ group.type }}</span><strong>{{ group.name }}</strong></span></span><span class="deployment-resource-operation" :class="`deployment-resource-operation--${group.operation}`">{{ group.operation }}</span></summary>
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
            </template>
          </section>

          <section v-else class="deployment-tab-panel" role="tabpanel">
            <div class="deployment-configuration-heading">
              <div class="deployment-info-heading">
                <h3>Configuration Snapshot</h3>
                <div class="deployment-info" @pointerenter="showInfo('configuration', $event)" @pointerleave="hideInfo('configuration', $event)">
                  <button type="button" class="deployment-info-button" aria-label="About configuration snapshot" :aria-expanded="infoOpen === 'configuration'" :aria-describedby="infoOpen === 'configuration' ? 'deployment-configuration-tooltip' : undefined" @click.stop="toggleInfo('configuration')">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><circle cx="12" cy="12" r="9" /><path d="M12 11v5M12 8h.01" stroke-linecap="round" /></svg>
                  </button>
                  <div v-if="infoOpen === 'configuration'" id="deployment-configuration-tooltip" class="deployment-info-tooltip" role="tooltip">Sensitive values are masked before storage.</div>
                </div>
              </div>
              <button v-if="configuration" type="button" class="button-ghost" @click="copyConfiguration">{{ copyLabel }}</button>
            </div>
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
.deployment-status--superseded { color: var(--muted); }
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
.deployment-date-mobile { display: none; }
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
.deployment-tab-count {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 16px;
  margin-left: 4px;
  border: 0;
  border-radius: 999px;
  padding: 1px 5px;
  background: var(--surface-muted-strong);
  color: var(--muted);
  font-size: .68rem;
  font-weight: 600;
  line-height: 1.25;
}
.deployment-tabs button.active .deployment-tab-count { color: var(--text); }
.deployment-tab-panel { min-width: 0; padding: 14px 0 4px; }
.deployment-tab-panel > h3 { margin: 0 0 4px; font-size: 1.35rem; }
.deployment-tab-panel > .deployment-note { margin: 0; }
.deployment-attempts-panel { padding-top: 8px; }
.deployment-attempt-columns,
.deployment-attempt-summary {
  display: grid;
  grid-template-columns: 16px minmax(220px, 1.4fr) minmax(100px, .7fr) minmax(100px, .5fr) 18px;
  gap: 12px;
  align-items: center;
}
.deployment-attempt-columns { padding: 4px 8px 7px; color: var(--muted); font-size: .72rem; font-weight: 600; }
.deployment-attempt-list { border-top: 1px solid var(--line); }
.deployment-attempt-row { min-width: 0; border-bottom: 1px solid var(--line); }
.deployment-attempt-summary {
  min-width: 0;
  min-height: 44px;
  padding: 8px;
  cursor: pointer;
  list-style: none;
  font-size: .84rem;
}
.deployment-attempt-row summary.deployment-attempt-summary::-webkit-details-marker { display: none; }
.deployment-attempt-summary:hover { background: var(--surface-hover-soft); }
.deployment-attempt-row--static .deployment-attempt-summary { cursor: default; }
.deployment-attempt-summary > * { min-width: 0; }
.deployment-attempt-row time { overflow-wrap: anywhere; font-variant-numeric: tabular-nums; }
.deployment-attempt-stage { color: var(--muted); }
.deployment-attempt-duration { color: var(--muted); font-variant-numeric: tabular-nums; }
.deployment-attempt-status { width: 8px; height: 8px; border-radius: 50%; background: var(--muted); }
.deployment-attempt-status--succeeded { background: var(--success); }
.deployment-attempt-status--failed { background: var(--error); }
.deployment-attempt-status--running { background: var(--info); }
.deployment-attempt-status--interrupted,
.deployment-attempt-status--pending { background: var(--muted); }
.deployment-attempt-row .deployment-chevron { justify-self: end; color: var(--muted); }
.deployment-attempt-details { min-width: 0; padding: 2px 38px 14px 36px; }
.deployment-attempt-details-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.deployment-attempt-details-heading strong { font-size: .78rem; }
.deployment-attempt-details-actions { display: flex; align-items: center; gap: 4px; }
.deployment-attempt-details-heading .button-ghost { flex: 0 0 auto; padding: 4px 7px; font-size: .74rem; }
.deployment-attempt-details pre {
  box-sizing: border-box;
  width: 100%;
  max-width: 100%;
  min-width: 0;
  margin: 5px 0 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  word-break: break-word;
  color: var(--text);
  font: inherit;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: .78rem;
  line-height: 1.5;
  user-select: text;
}
.deployment-attempt-details code { white-space: inherit; overflow-wrap: inherit; word-break: inherit; }
.deployment-init-jobs { margin: 4px 0 14px; }
.deployment-init-jobs h4 { margin: 0 0 6px; font-size: .78rem; }
.deployment-init-job { border-top: 1px solid var(--line); }
.deployment-init-job-summary { display: grid; grid-template-columns: minmax(0, 1fr) auto auto auto; gap: 10px; align-items: center; padding: 8px 0; }
.deployment-init-job-identity { display: flex; min-width: 0; flex-direction: column; }
.deployment-init-job-title { display: flex; align-items: center; gap: 8px; min-width: 0; }
.deployment-init-job-title strong { min-width: 0; overflow-wrap: anywhere; }
.deployment-init-job-title .deployment-attempt-status { flex: 0 0 auto; }
.deployment-init-job-summary small { color: var(--muted); overflow-wrap: anywhere; }
.deployment-init-job-badge { border-radius: 999px; padding: 2px 7px; background: var(--surface-muted-strong); color: var(--muted); font-size: .68rem; }
.deployment-init-job-unavailable { color: var(--muted); font-size: .74rem; }
.deployment-init-job-logs-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  padding: 0;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--accent-strong);
  cursor: pointer;
}
.deployment-init-job-logs-button:hover,
.deployment-init-job-logs-button[aria-expanded="true"] { background: transparent; color: var(--text); filter: none; transform: none; }
.deployment-init-job-logs-button:focus-visible { outline: 2px solid var(--control-primary); outline-offset: 2px; }
.deployment-init-job-logs-button svg { width: 18px; height: 18px; }
.deployment-init-job-logs { margin: 0 0 8px; border-left: 2px solid var(--line); padding: 4px 10px; }
.deployment-init-job-logs pre { max-height: 320px; overflow: auto; white-space: pre; }
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
.deployment-info-heading { position: relative; display: inline-flex; align-items: center; gap: 8px; min-width: 0; }
.deployment-info { flex: 0 0 auto; }
.deployment-info-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 25px;
  height: 25px;
  padding: 2px;
  border: 0;
  border-radius: 50%;
  background: transparent;
  color: var(--muted);
  cursor: help;
}
.deployment-info-button svg { width: 20px; height: 20px; }
.deployment-info-button:hover, .deployment-info-button[aria-expanded="true"] { color: var(--text); transform: none; filter: none; }
.deployment-info-button:focus-visible { outline: 2px solid var(--control-primary); outline-offset: 2px; }
.deployment-info-tooltip {
  position: absolute;
  z-index: 10;
  top: -8px;
  left: calc(100% + 12px);
  box-sizing: border-box;
  width: min(325px, calc(100vw - 52px));
  padding: 11px 13px;
  border: 1px solid var(--border-default);
  border-radius: 8px;
  background: var(--surface-elevated);
  box-shadow: var(--shadow-popover);
  color: var(--text);
  font-size: .82rem;
  font-weight: 400;
  line-height: 1.5;
  overflow-wrap: anywhere;
}
.deployment-info-tooltip::before {
  content: "";
  position: absolute;
  left: -6px;
  top: 15px;
  width: 9px;
  height: 9px;
  border-bottom: 1px solid var(--border-default);
  border-left: 1px solid var(--border-default);
  background: var(--surface-elevated);
  transform: rotate(45deg);
}
.deployment-resource { border-bottom: 1px solid var(--line); margin: 10px 0; overflow: hidden; }
.deployment-resource-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; list-style: none; padding: 8px 4px; }
summary.deployment-resource-heading { cursor: pointer; }
.deployment-resource-heading > span:first-child { display: flex; align-items: center; gap: 10px; min-width: 0; overflow-wrap: anywhere; }
.deployment-resource-heading > span:first-child > :deep(.deployment-icon) { width: 22px; height: 22px; color: var(--muted); }
.deployment-resource-heading > span:first-child > span { display: flex; align-items: baseline; gap: 8px; min-width: 0; }
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

  /* Show icon-and-value metadata without cards or redundant stack/stage fields on mobile. */
  .deployment-metadata { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px 10px; margin-top: 14px; }
  .deployment-metadata-item,
  .deployment-metadata-item:first-child,
  .deployment-metadata-item:nth-child(4) {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
    min-height: 24px;
    padding: 0;
    border: 0;
    border-radius: 0;
    background: transparent;
  }
  .deployment-metadata-item:first-child,
  .deployment-metadata-item:last-child { display: none; }
  .deployment-metadata dt { display: flex; flex: 0 0 19px; align-items: center; font-size: 0; }
  .deployment-metadata dt > :deep(.deployment-icon) {
    position: static;
    display: block;
    width: 19px;
    height: 19px;
    color: var(--muted);
    transform: none;
  }
  .deployment-metadata dd { min-width: 0; margin: 0; font-size: .82rem; line-height: 1.25; }
  .deployment-date-desktop { display: none; }
  .deployment-date-mobile { display: inline; font-size: .74rem; font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }

  .deployment-outcome { grid-template-columns: 34px minmax(0, 1fr); gap: 12px; padding: 14px; }
  .deployment-outcome-icon { width: 32px; height: 32px; }
  .deployment-tabs { margin-top: 12px; }
  .deployment-tab-panel { padding-top: 12px; }
  .deployment-attempt-columns { display: none; }
  .deployment-attempt-list { border-top: 0; }
  .deployment-attempt-summary {
    grid-template-columns: 12px minmax(0, 1fr) auto 16px;
    gap: 8px;
  }
  .deployment-attempt-row time { grid-column: 2; }
  .deployment-attempt-stage { grid-column: 2; grid-row: 2; }
  .deployment-attempt-duration { grid-column: 3; grid-row: 1 / span 2; }
  .deployment-attempt-row .deployment-chevron { grid-column: 4; grid-row: 1 / span 2; }
  .deployment-attempt-details { padding: 2px 8px 14px 28px; }
  .deployment-counts > span { flex: 1 1 calc(50% - 10px); min-width: 0; gap: 8px; padding: 9px 10px; }
  .deployment-service-table-wrap, .deployment-resource-list, .deployment-resource-empty, .deployment-change-section { margin-left: 0; }
  .deployment-change { grid-template-columns: minmax(0, 1fr); gap: 5px; }
  .deployment-changes-heading, .deployment-configuration-heading { align-items: flex-start; flex-wrap: wrap; }
  .deployment-info-tooltip { top: calc(100% + 8px); left: 0; width: min(315px, calc(100vw - 44px)); }
  .deployment-info-tooltip::before { display: none; }
}
</style>
