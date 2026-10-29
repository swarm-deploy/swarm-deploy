<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";

import type { EnvironmentVariable, SwarmTaskDetails, SwarmTaskResources } from "../../api/types";
import { copyText } from "../../utils/clipboard";
import { formatBytes, formatDate, formatNanoCPU } from "../../utils/format";
import { decodeBase64Utf8 } from "../../utils/realtime";
import AppPanel from "../common/AppPanel.vue";
import AppStatusBadge from "../common/AppStatusBadge.vue";
import AppTableEmpty from "../common/AppTableEmpty.vue";

const props = defineProps<{
  task: SwarmTaskDetails | null;
  loading: boolean;
  error: string;
}>();

const emit = defineEmits<{
  close: [];
}>();

const environmentSearch = ref("");
const revealed = ref(new Set<string>());
const copiedKey = ref("");
let copiedResetTimer: ReturnType<typeof setTimeout> | undefined;

interface ImageReferenceParts {
  name: string;
  tag: string;
  digest: string;
}

const imageReference = computed(() => parseImageReference(props.task?.image ?? ""));
const identifiers = computed(() => [
  { key: "task-id", label: "Task ID", value: props.task?.id ?? "" },
  { key: "service-id", label: "Service ID", value: props.task?.service_id ?? "" },
  { key: "node-id", label: "Node ID", value: props.task?.node_id ?? "" },
  { key: "container-id", label: "Container ID", value: props.task?.container_id ?? "" },
]);

const filteredEnvironment = computed(() => {
  const query = environmentSearch.value.trim().toLocaleLowerCase();
  const environment = props.task?.environment ?? [];
  if (!query) return environment;
  return environment.filter((variable) => variable.name.toLocaleLowerCase().includes(query));
});

watch(
  () => props.task?.id,
  () => {
    environmentSearch.value = "";
    revealed.value = new Set();
    clearCopiedState();
  },
);

onBeforeUnmount(clearCopiedState);

function stateVariant(state: string): "success" | "error" | "neutral" {
  const value = state.trim().toLowerCase();
  if (["running", "complete", "completed"].includes(value)) return "success";
  if (["failed", "rejected", "shutdown"].includes(value)) return "error";
  return "neutral";
}

function parseImageReference(reference: string): ImageReferenceParts {
  const value = reference.trim();
  if (!value) return { name: "", tag: "", digest: "" };

  const digestSeparator = value.indexOf("@");
  const nameAndTag = digestSeparator === -1 ? value : value.slice(0, digestSeparator);
  const digest = digestSeparator === -1 ? "" : value.slice(digestSeparator + 1);
  const lastSlash = nameAndTag.lastIndexOf("/");
  const lastColon = nameAndTag.lastIndexOf(":");
  const hasTag = lastColon > lastSlash;

  return {
    name: hasTag ? nameAndTag.slice(0, lastColon) : nameAndTag,
    tag: hasTag ? nameAndTag.slice(lastColon + 1) : "",
    digest,
  };
}

function shortenedDigest(digest: string): string {
  const separator = digest.indexOf(":");
  if (separator === -1) return digest.length > 18 ? `${digest.slice(0, 8)}...${digest.slice(-7)}` : digest;

  const algorithm = digest.slice(0, separator);
  const value = digest.slice(separator + 1);
  return value.length > 18 ? `${algorithm}:${value.slice(0, 8)}...${value.slice(-7)}` : digest;
}

function displayedExitCode(task: SwarmTaskDetails): number | string {
  const state = task.current_state.trim().toLowerCase();
  const finished = ["complete", "completed", "failed", "rejected", "shutdown"].includes(state);
  return finished && task.exit_code !== undefined && task.exit_code !== null ? task.exit_code : "—";
}

function toggleReveal(variable: EnvironmentVariable): void {
  const next = new Set(revealed.value);
  if (next.has(variable.name)) next.delete(variable.name);
  else next.add(variable.name);
  revealed.value = next;
}

function decodedValue(variable: EnvironmentVariable): string | null {
  return variable.encoded ? decodeBase64Utf8(variable.value) : variable.value;
}

function displayedValue(variable: EnvironmentVariable): string {
  if (variable.encoded && !revealed.value.has(variable.name)) return "••••••••••••";
  const decoded = decodedValue(variable);
  if (decoded === null) return "Unable to decode value";
  return decoded === "" ? "(empty)" : decoded;
}

async function copyVariableValue(variable: EnvironmentVariable): Promise<void> {
  const value = decodedValue(variable);
  if (value === null) return;
  await copyText(value);
  showCopiedState(`value:${variable.name}`);
}

async function copyDetailValue(value: string, key: string): Promise<void> {
  if (!value) return;
  await copyText(value);
  showCopiedState(key);
}

function showCopiedState(key: string): void {
  if (copiedResetTimer !== undefined) clearTimeout(copiedResetTimer);
  copiedKey.value = key;
  copiedResetTimer = setTimeout(() => {
    copiedKey.value = "";
    copiedResetTimer = undefined;
  }, 1600);
}

function clearCopiedState(): void {
  if (copiedResetTimer !== undefined) clearTimeout(copiedResetTimer);
  copiedResetTimer = undefined;
  copiedKey.value = "";
}

function hasResources(resources: SwarmTaskResources | undefined): boolean {
  return Boolean(resources && (resources.nano_cpus > 0 || resources.memory_bytes > 0));
}
</script>

<template>
  <AppPanel class="realtime-task-panel" aria-label="Task details">
    <template #header>
      <div class="realtime-task-panel-header">
        <div>
          <span class="realtime-task-panel-eyebrow">Task details</span>
          <h3>{{ task ? `Slot ${task.slot}` : "Task" }}</h3>
        </div>
        <button type="button" class="realtime-icon-button" aria-label="Close task details" @click="emit('close')">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m7 7 10 10M17 7 7 17" /></svg>
        </button>
      </div>
    </template>

    <div class="realtime-task-panel-scroll">
      <AppTableEmpty v-if="loading && !task" message="Loading task details..." />
      <AppTableEmpty v-else-if="error && !task" role="alert">Failed to load task: {{ error }}</AppTableEmpty>

      <template v-if="task">
        <p v-if="error" class="realtime-inline-error" role="alert">Refresh failed: {{ error }}</p>

        <section class="realtime-task-section" aria-labelledby="task-runtime-title">
          <h4 id="task-runtime-title">Runtime</h4>
          <dl class="realtime-task-runtime">
            <div>
              <dt>State</dt>
              <dd><AppStatusBadge class="realtime-task-runtime-state" :variant="stateVariant(task.current_state)" dot>{{ task.current_state || "Unknown" }}</AppStatusBadge></dd>
            </div>
            <div><dt>Desired</dt><dd>{{ task.desired_state || "—" }}</dd></div>
            <div><dt>Exit code</dt><dd>{{ displayedExitCode(task) }}</dd></div>
          </dl>
          <dl v-if="task.error" class="realtime-task-runtime-error">
            <div><dt>Runtime error</dt><dd>{{ task.error }}</dd></div>
          </dl>
        </section>

        <section class="realtime-task-section" aria-labelledby="task-image-title">
          <h4 id="task-image-title">Image</h4>
          <div v-if="task.image" class="realtime-task-image">
            <div class="realtime-task-image-primary">
              <code :title="imageReference.name">{{ imageReference.name }}</code>
              <button
                type="button"
                class="realtime-detail-copy-button"
                :aria-label="copiedKey === 'image' ? 'Image reference copied' : 'Copy full image reference'"
                :title="copiedKey === 'image' ? 'Copied' : 'Copy full image reference'"
                @click="copyDetailValue(task.image, 'image')"
              >
                <svg v-if="copiedKey === 'image'" class="realtime-detail-copy-check" viewBox="0 0 24 24" aria-hidden="true"><path d="m5 12.5 4.2 4.2L19 7" /></svg>
                <svg v-else viewBox="0 0 24 24" aria-hidden="true"><rect x="8" y="8" width="11" height="11" rx="2" /><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2" /></svg>
              </button>
            </div>
            <div v-if="imageReference.tag || imageReference.digest" class="realtime-task-image-meta">
              <span v-if="imageReference.tag" class="realtime-task-image-tag" :title="imageReference.tag">{{ imageReference.tag }}</span>
              <code v-if="imageReference.digest" class="realtime-task-image-digest" :title="imageReference.digest">{{ shortenedDigest(imageReference.digest) }}</code>
            </div>
          </div>
          <span v-else class="realtime-task-missing">—</span>
        </section>

        <section class="realtime-task-section" aria-labelledby="task-identifiers-title">
          <h4 id="task-identifiers-title">Identifiers</h4>
          <dl class="realtime-task-identifiers">
            <div v-for="identifier in identifiers" :key="identifier.key">
              <dt>{{ identifier.label }}</dt>
              <dd>
                <code v-if="identifier.value" :title="identifier.value">{{ identifier.value }}</code>
                <span v-else class="realtime-task-missing">—</span>
              </dd>
              <button
                v-if="identifier.value"
                type="button"
                class="realtime-detail-copy-button"
                :aria-label="copiedKey === identifier.key ? `${identifier.label} copied` : `Copy ${identifier.label.toLowerCase()}`"
                :title="copiedKey === identifier.key ? 'Copied' : `Copy ${identifier.label}`"
                @click="copyDetailValue(identifier.value, identifier.key)"
              >
                <svg v-if="copiedKey === identifier.key" class="realtime-detail-copy-check" viewBox="0 0 24 24" aria-hidden="true"><path d="m5 12.5 4.2 4.2L19 7" /></svg>
                <svg v-else viewBox="0 0 24 24" aria-hidden="true"><rect x="8" y="8" width="11" height="11" rx="2" /><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2" /></svg>
              </button>
            </div>
          </dl>
        </section>

        <section class="realtime-task-section" aria-labelledby="task-timestamps-title">
          <h4 id="task-timestamps-title">Timestamps</h4>
          <dl class="realtime-task-timestamps">
            <div><dt>Created</dt><dd><time :datetime="task.created_at">{{ formatDate(task.created_at) }}</time></dd></div>
            <div><dt>Updated</dt><dd><time :datetime="task.updated_at">{{ formatDate(task.updated_at) }}</time></dd></div>
          </dl>
        </section>

        <section v-if="hasResources(task.reservations) || hasResources(task.limits)" class="realtime-task-section" aria-labelledby="task-resources-title">
          <h4 id="task-resources-title">Resources</h4>
          <dl class="realtime-task-resources">
            <div v-if="hasResources(task.reservations)">
              <dt>Reservations</dt>
              <dd><span v-if="task.reservations?.nano_cpus">{{ formatNanoCPU(task.reservations.nano_cpus) }}</span><span v-if="task.reservations?.memory_bytes">{{ formatBytes(task.reservations.memory_bytes) }}</span></dd>
            </div>
            <div v-if="hasResources(task.limits)">
              <dt>Limits</dt>
              <dd><span v-if="task.limits?.nano_cpus">{{ formatNanoCPU(task.limits.nano_cpus) }}</span><span v-if="task.limits?.memory_bytes">{{ formatBytes(task.limits.memory_bytes) }}</span></dd>
            </div>
          </dl>
        </section>

        <section class="realtime-task-section" aria-labelledby="task-environment-title">
          <div class="realtime-task-section-title realtime-environment-title">
            <div class="realtime-environment-heading">
              <h4 id="task-environment-title">Environment variables</h4>
              <span class="realtime-environment-count" :aria-label="`${task.environment.length} variables`">{{ task.environment.length }}</span>
            </div>
            <label class="realtime-env-search">
              <span class="sr-only">Search environment variables</span>
              <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="11" cy="11" r="6.5" /><path d="m16 16 4 4" /></svg>
              <input v-model="environmentSearch" type="search" placeholder="Search variables..." />
            </label>
          </div>

          <AppTableEmpty v-if="task.environment.length === 0" message="No environment variables." />
          <AppTableEmpty v-else-if="filteredEnvironment.length === 0" message="No variables match the search." />
          <ul v-else class="realtime-environment-list">
            <li v-for="variable in filteredEnvironment" :key="variable.name" class="realtime-environment-row">
              <div class="realtime-environment-content">
                <code class="realtime-environment-name">{{ variable.name }}</code>
                <span class="realtime-environment-value" :class="{ 'realtime-environment-value--masked': variable.encoded && !revealed.has(variable.name), 'realtime-environment-value--error': variable.encoded && revealed.has(variable.name) && decodedValue(variable) === null }">{{ displayedValue(variable) }}</span>
              </div>
              <div class="realtime-environment-actions">
                <button
                  v-if="variable.encoded"
                  type="button"
                  class="realtime-environment-action"
                  :aria-label="`${revealed.has(variable.name) ? 'Hide' : 'Show'} value for ${variable.name}`"
                  :title="revealed.has(variable.name) ? 'Hide value' : 'Show value'"
                  @click="toggleReveal(variable)"
                >
                  <svg v-if="revealed.has(variable.name)" viewBox="0 0 24 24" aria-hidden="true"><path d="M3 3 21 21M10.6 10.7a2 2 0 0 0 2.7 2.7M9.9 4.2A10.4 10.4 0 0 1 12 4c5.2 0 8.5 4.2 9.5 5.8a4 4 0 0 1 .5 1.2M6.6 6.6A14 14 0 0 0 2.5 11.8C3.5 13.4 6.8 17.6 12 17.6a10 10 0 0 0 4.1-.9" /></svg>
                  <svg v-else viewBox="0 0 24 24" aria-hidden="true"><path d="M2.5 12S6 6.5 12 6.5 21.5 12 21.5 12 18 17.5 12 17.5 2.5 12 2.5 12Z" /><circle cx="12" cy="12" r="2.5" /></svg>
                </button>
                <button
                  type="button"
                  class="realtime-environment-action"
                  :disabled="variable.encoded && decodedValue(variable) === null"
                  :aria-label="copiedKey === `value:${variable.name}` ? `Value copied for ${variable.name}` : `Copy value for ${variable.name}`"
                  :title="copiedKey === `value:${variable.name}` ? 'Value copied' : 'Copy value'"
                  @click="copyVariableValue(variable)"
                >
                  <svg v-if="copiedKey === `value:${variable.name}`" class="realtime-environment-copy-check" viewBox="0 0 24 24" aria-hidden="true"><path d="m5 12.5 4.2 4.2L19 7" /></svg>
                  <svg v-else viewBox="0 0 24 24" aria-hidden="true"><rect x="8" y="8" width="11" height="11" rx="2" /><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2" /></svg>
                </button>
              </div>
            </li>
          </ul>
        </section>
      </template>
    </div>
  </AppPanel>
</template>

<style scoped>
.realtime-task-panel { min-width: 0; }
.realtime-task-panel :deep(.app-panel-content) { min-height: 0; flex: 1 1 auto; display: flex; }
.realtime-task-panel-header { min-height: 58px; display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 11px 14px; border-bottom: 1px solid var(--border-subtle); background: var(--surface-card-soft); }
.realtime-task-panel-header > div { min-width: 0; }
.realtime-task-panel-header h3 { margin: 2px 0 0; font-size: 1rem; }
.realtime-task-panel-eyebrow { color: var(--muted); font-size: .68rem; text-transform: uppercase; letter-spacing: .05em; }
.realtime-icon-button { display: inline-grid; place-items: center; width: 30px; height: 30px; border: 0; border-radius: 8px; background: transparent; color: var(--muted); padding: 0; transform: none; filter: none; transition: background-color 150ms ease, color 150ms ease, opacity 150ms ease; }
.realtime-icon-button:hover { background: var(--surface-hover-strong); color: var(--text); transform: none; filter: none; }
.realtime-icon-button:focus-visible { outline: 2px solid var(--accent-focus); outline-offset: 2px; }
.realtime-icon-button svg { width: 18px; height: 18px; fill: none; stroke: currentColor; stroke-width: 1.7; stroke-linecap: round; }
.realtime-task-panel-scroll { min-height: 0; flex: 1 1 auto; overflow: auto; }
.realtime-task-section { min-width: 0; display: grid; gap: 10px; padding: 14px; border-bottom: 1px solid var(--border-subtle); }
.realtime-task-section:last-child { border-bottom: 0; }
.realtime-task-section h4 { margin: 0; font-size: .84rem; }
.realtime-task-section-title { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
.realtime-task-runtime, .realtime-task-timestamps { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; margin: 0; }
.realtime-task-runtime > div, .realtime-task-timestamps > div, .realtime-task-resources > div { min-width: 0; display: grid; align-content: start; gap: 4px; }
.realtime-task-runtime dt, .realtime-task-timestamps dt, .realtime-task-resources dt, .realtime-task-runtime-error dt { color: var(--muted); font-size: .68rem; }
.realtime-task-runtime dd, .realtime-task-timestamps dd, .realtime-task-resources dd, .realtime-task-runtime-error dd { min-width: 0; margin: 0; font-size: .78rem; overflow-wrap: anywhere; }
.realtime-task-runtime-state { border: 0; padding: 0; font-size: .78rem; }
.realtime-task-runtime-error { margin: 0; border-top: 1px solid var(--border-subtle); padding-top: 10px; }
.realtime-task-runtime-error > div { display: grid; gap: 4px; }
.realtime-task-runtime-error dd { color: var(--error); line-height: 1.4; }
.realtime-task-image { min-width: 0; display: grid; gap: 8px; }
.realtime-task-image-primary { min-width: 0; display: grid; grid-template-columns: minmax(0, 1fr) 28px; align-items: center; gap: 8px; }
.realtime-task-image-primary code { min-width: 0; overflow: hidden; color: var(--text); font-size: .75rem; font-weight: 400; text-overflow: ellipsis; white-space: nowrap; }
.realtime-task-image-meta { min-width: 0; display: flex; align-items: center; gap: 10px; }
.realtime-task-image-tag { flex: 0 1 auto; max-width: 42%; overflow: hidden; border: 0; border-radius: 999px; background: var(--surface-muted-strong); color: var(--text); padding: 3px 9px; font: 400 .68rem/1.2 ui-monospace, SFMono-Regular, Menlo, monospace; text-overflow: ellipsis; white-space: nowrap; }
.realtime-task-image-digest { min-width: 0; overflow: hidden; color: var(--muted); font-size: .68rem; text-overflow: ellipsis; white-space: nowrap; }
.realtime-detail-copy-button { display: inline-grid; place-items: center; width: 28px; height: 28px; border: 0; border-radius: 5px; background: transparent; color: var(--muted); padding: 0; transform: none; filter: none; }
.realtime-detail-copy-button:hover { background: var(--surface-hover); color: var(--text); transform: none; filter: none; }
.realtime-detail-copy-button svg { width: 16px; height: 16px; fill: none; stroke: currentColor; stroke-width: 1.7; stroke-linecap: round; stroke-linejoin: round; }
.realtime-detail-copy-button .realtime-detail-copy-check { color: var(--success); stroke-width: 2; }
.realtime-task-identifiers { margin: 0; }
.realtime-task-identifiers > div { min-width: 0; display: grid; grid-template-columns: 80px minmax(0, 1fr) 28px; align-items: center; gap: 8px; min-height: 36px; border-bottom: 1px solid var(--border-subtle); }
.realtime-task-identifiers > div:last-child { border-bottom: 0; }
.realtime-task-identifiers dt { color: var(--muted); font-size: .68rem; }
.realtime-task-identifiers dd { min-width: 0; margin: 0; }
.realtime-task-identifiers code { display: block; min-width: 0; overflow: hidden; color: var(--text); font-size: .7rem; text-overflow: ellipsis; white-space: nowrap; }
.realtime-task-missing { color: var(--muted); font-size: .78rem; }
.realtime-task-timestamps { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 14px; }
.realtime-task-timestamps time { color: var(--text); font-variant-numeric: tabular-nums; }
.realtime-task-resources { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; margin: 0; }
.realtime-task-resources dd { display: flex; flex-wrap: wrap; gap: 6px 12px; font-variant-numeric: tabular-nums; }
.realtime-environment-title { margin: -14px -14px 0; border-bottom: 1px solid var(--border-subtle); background: var(--surface-card-muted); padding: 12px 14px; }
.realtime-environment-heading { min-width: 0; display: flex; align-items: center; gap: 8px; }
.realtime-environment-count { flex: 0 0 auto; min-width: 30px; height: 24px; display: inline-grid; place-items: center; border: 0; border-radius: 999px; background: var(--surface-muted-strong); color: var(--muted); padding: 0 8px; font-size: .72rem; font-weight: 600; font-variant-numeric: tabular-nums; }
.realtime-env-search { position: relative; width: min(180px, 52%); }
.realtime-env-search svg { position: absolute; top: 50%; left: 9px; width: 14px; height: 14px; color: var(--muted); fill: none; stroke: currentColor; stroke-width: 1.7; stroke-linecap: round; transform: translateY(-50%); pointer-events: none; }
.realtime-env-search input { width: 100%; height: 32px; border: 1px solid var(--border-default); border-radius: 6px; background: var(--surface-input); color: var(--text); padding: 5px 9px 5px 29px; font: inherit; font-size: .72rem; }
.realtime-env-search input::placeholder { color: var(--muted); }
.realtime-env-search input:focus-visible, .realtime-task-panel button:focus-visible { outline: 2px solid var(--accent-focus); outline-offset: 2px; }
.realtime-environment-list { margin: 0; padding: 0; list-style: none; }
.realtime-environment-row { min-width: 0; display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: center; gap: 12px; padding: 10px 2px; border-bottom: 1px solid var(--border-subtle); }
.realtime-environment-row:first-child { padding-top: 4px; }
.realtime-environment-row:last-child { padding-bottom: 4px; border-bottom: 0; }
.realtime-environment-content { min-width: 0; display: grid; gap: 3px; }
.realtime-environment-name { min-width: 0; color: var(--text); font: 600 .73rem/1.35 ui-monospace, SFMono-Regular, Menlo, monospace; overflow-wrap: anywhere; }
.realtime-environment-value { min-width: 0; color: var(--muted); font: .69rem/1.45 ui-monospace, SFMono-Regular, Menlo, monospace; white-space: pre-wrap; overflow-wrap: anywhere; word-break: break-word; }
.realtime-environment-value--masked { color: var(--muted); letter-spacing: .08em; user-select: none; }
.realtime-environment-value--error { color: var(--error); }
.realtime-environment-actions { display: flex; align-items: center; justify-content: flex-end; gap: 2px; }
.realtime-environment-action { display: inline-grid; place-items: center; width: 28px; height: 28px; border: 0; border-radius: 5px; background: transparent; color: var(--muted); padding: 0; transform: none; filter: none; }
.realtime-environment-action:hover:not(:disabled) { background: var(--surface-hover); color: var(--text); transform: none; filter: none; }
.realtime-environment-action:disabled { cursor: not-allowed; opacity: .45; }
.realtime-environment-action svg { width: 16px; height: 16px; fill: none; stroke: currentColor; stroke-width: 1.7; stroke-linecap: round; stroke-linejoin: round; }
.realtime-environment-action .realtime-environment-copy-check { color: var(--success); stroke-width: 2; }
.realtime-inline-error { margin: 10px 14px 0; border: 1px solid var(--warning-border); border-radius: 7px; background: var(--warning-muted); color: var(--warning); padding: 8px 10px; font-size: .76rem; }

@media (max-width: 900px) {
  .realtime-task-panel { border-radius: 10px; background: var(--surface-modal); box-shadow: var(--shadow-popover); }
  .realtime-task-panel-scroll { max-height: calc(100dvh - 76px); }
}

@media (max-width: 460px) {
  .realtime-task-timestamps, .realtime-task-resources { grid-template-columns: minmax(0, 1fr); }
  .realtime-environment-title { align-items: stretch; flex-direction: column; }
  .realtime-env-search { width: 100%; }
  .realtime-env-search input { font-size: 16px; }
}
</style>
