<script setup lang="ts">
import { computed, ref, watch } from "vue";

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
  logs: [taskID: string];
}>();

const environmentSearch = ref("");
const revealed = ref(new Set<string>());
const copiedKey = ref("");

const filteredEnvironment = computed(() => {
  const query = environmentSearch.value.trim().toLocaleLowerCase();
  const environment = props.task?.environment ?? [];
  if (!query) return environment;
  return environment.filter((variable) => variable.name.toLocaleLowerCase().includes(query));
});

watch(
  () => props.task?.id,
  () => {
    revealed.value = new Set();
    copiedKey.value = "";
  },
);

function stateVariant(state: string): "success" | "error" | "neutral" {
  const value = state.trim().toLowerCase();
  if (["running", "complete", "completed"].includes(value)) return "success";
  if (["failed", "rejected", "shutdown"].includes(value)) return "error";
  return "neutral";
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

async function copyVariableName(variable: EnvironmentVariable): Promise<void> {
  await copyText(variable.name);
  copiedKey.value = `name:${variable.name}`;
}

async function copyVariableValue(variable: EnvironmentVariable): Promise<void> {
  const value = decodedValue(variable);
  if (value === null) return;
  await copyText(value);
  copiedKey.value = `value:${variable.name}`;
}

async function copyIdentifier(value: string, key: string): Promise<void> {
  if (!value) return;
  await copyText(value);
  copiedKey.value = key;
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

        <section class="realtime-task-section" aria-labelledby="task-overview-title">
          <div class="realtime-task-section-title">
            <h4 id="task-overview-title">Overview</h4>
            <button type="button" class="button-ghost realtime-logs-button" @click="emit('logs', task.id)">Logs</button>
          </div>
          <dl class="realtime-task-overview">
            <div>
              <dt>State</dt>
              <dd><AppStatusBadge :variant="stateVariant(task.current_state)" filled dot>{{ task.current_state || "Unknown" }}</AppStatusBadge></dd>
            </div>
            <div><dt>Desired</dt><dd>{{ task.desired_state || "n/a" }}</dd></div>
            <div><dt>Slot</dt><dd>{{ task.slot }}</dd></div>
            <div><dt>Exit code</dt><dd>{{ task.exit_code ?? "n/a" }}</dd></div>
            <div class="realtime-task-overview-wide">
              <dt>Task ID</dt>
              <dd><code>{{ task.id }}</code><button type="button" class="realtime-copy-button" aria-label="Copy task ID" @click="copyIdentifier(task.id, 'task-id')">{{ copiedKey === "task-id" ? "Copied" : "Copy" }}</button></dd>
            </div>
            <div class="realtime-task-overview-wide">
              <dt>Service ID</dt>
              <dd><code>{{ task.service_id || "n/a" }}</code><button v-if="task.service_id" type="button" class="realtime-copy-button" aria-label="Copy service ID" @click="copyIdentifier(task.service_id, 'service-id')">{{ copiedKey === "service-id" ? "Copied" : "Copy" }}</button></dd>
            </div>
            <div class="realtime-task-overview-wide">
              <dt>Node ID</dt>
              <dd><code>{{ task.node_id || "n/a" }}</code><button v-if="task.node_id" type="button" class="realtime-copy-button" aria-label="Copy node ID" @click="copyIdentifier(task.node_id, 'node-id')">{{ copiedKey === "node-id" ? "Copied" : "Copy" }}</button></dd>
            </div>
            <div class="realtime-task-overview-wide">
              <dt>Container ID</dt>
              <dd><code>{{ task.container_id || "n/a" }}</code><button v-if="task.container_id" type="button" class="realtime-copy-button" aria-label="Copy container ID" @click="copyIdentifier(task.container_id, 'container-id')">{{ copiedKey === "container-id" ? "Copied" : "Copy" }}</button></dd>
            </div>
            <div class="realtime-task-overview-wide"><dt>Image</dt><dd class="realtime-break-value">{{ task.image || "n/a" }}</dd></div>
            <div><dt>Created</dt><dd>{{ formatDate(task.created_at) }}</dd></div>
            <div><dt>Updated</dt><dd>{{ formatDate(task.updated_at) }}</dd></div>
            <div v-if="task.error" class="realtime-task-overview-wide realtime-task-error"><dt>Runtime error</dt><dd>{{ task.error }}</dd></div>
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
            <div>
              <h4 id="task-environment-title">Environment</h4>
              <span>{{ task.environment.length }} variables</span>
            </div>
            <label class="realtime-env-search">
              <span class="sr-only">Search environment variables</span>
              <input v-model="environmentSearch" type="search" placeholder="Search variables" />
            </label>
          </div>

          <AppTableEmpty v-if="task.environment.length === 0" message="No environment variables." />
          <AppTableEmpty v-else-if="filteredEnvironment.length === 0" message="No variables match the search." />
          <ul v-else class="realtime-environment-list">
            <li v-for="variable in filteredEnvironment" :key="variable.name" class="realtime-environment-row">
              <div class="realtime-environment-name">
                <code>{{ variable.name }}</code>
                <button type="button" class="realtime-copy-button" :aria-label="`Copy variable name ${variable.name}`" @click="copyVariableName(variable)">{{ copiedKey === `name:${variable.name}` ? "Copied" : "Copy name" }}</button>
              </div>
              <pre class="realtime-environment-value" :class="{ 'realtime-environment-value--masked': variable.encoded && !revealed.has(variable.name), 'realtime-environment-value--error': variable.encoded && revealed.has(variable.name) && decodedValue(variable) === null }">{{ displayedValue(variable) }}</pre>
              <div class="realtime-environment-actions">
                <button v-if="variable.encoded" type="button" class="button-ghost" :aria-label="`${revealed.has(variable.name) ? 'Hide' : 'Show'} value for ${variable.name}`" @click="toggleReveal(variable)">{{ revealed.has(variable.name) ? "Hide" : "Show" }}</button>
                <button type="button" class="button-ghost" :disabled="variable.encoded && decodedValue(variable) === null" :aria-label="`Copy value for ${variable.name}`" @click="copyVariableValue(variable)">{{ copiedKey === `value:${variable.name}` ? "Copied" : "Copy" }}</button>
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
.realtime-icon-button { display: inline-grid; place-items: center; width: 30px; height: 30px; border: 0; background: transparent; color: var(--muted); padding: 0; transform: none; filter: none; }
.realtime-icon-button:hover { color: var(--text); }
.realtime-icon-button svg { width: 18px; height: 18px; fill: none; stroke: currentColor; stroke-width: 1.7; stroke-linecap: round; }
.realtime-task-panel-scroll { min-height: 0; flex: 1 1 auto; overflow: auto; }
.realtime-task-section { min-width: 0; display: grid; gap: 10px; padding: 14px; border-bottom: 1px solid var(--border-subtle); }
.realtime-task-section:last-child { border-bottom: 0; }
.realtime-task-section h4 { margin: 0; font-size: .84rem; }
.realtime-task-section-title { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
.realtime-logs-button { min-height: 30px; padding: 5px 10px; border-radius: 6px; font-size: .74rem; transform: none; filter: none; }
.realtime-task-overview { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 10px 14px; margin: 0; }
.realtime-task-overview > div, .realtime-task-resources > div { min-width: 0; display: grid; gap: 4px; }
.realtime-task-overview dt, .realtime-task-resources dt { color: var(--muted); font-size: .68rem; }
.realtime-task-overview dd, .realtime-task-resources dd { min-width: 0; margin: 0; font-size: .78rem; overflow-wrap: anywhere; }
.realtime-task-overview dd { display: flex; align-items: center; gap: 7px; }
.realtime-task-overview code { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--text); font-size: .72rem; }
.realtime-task-overview-wide { grid-column: 1 / -1; }
.realtime-break-value { display: block !important; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; word-break: break-word; }
.realtime-task-error dd { color: var(--error); line-height: 1.4; }
.realtime-copy-button { flex: 0 0 auto; border: 0; background: transparent; color: var(--control-primary); padding: 2px; font: inherit; font-size: .68rem; font-weight: 600; transform: none; filter: none; }
.realtime-copy-button:hover { text-decoration: underline; }
.realtime-task-resources { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; margin: 0; }
.realtime-task-resources dd { display: flex; flex-wrap: wrap; gap: 6px 12px; font-variant-numeric: tabular-nums; }
.realtime-environment-title > div { display: flex; align-items: baseline; gap: 7px; }
.realtime-environment-title > div span { color: var(--muted); font-size: .68rem; }
.realtime-env-search { width: min(170px, 55%); }
.realtime-env-search input { width: 100%; height: 32px; border: 1px solid var(--border-default); border-radius: 6px; background: var(--surface-input); color: var(--text); padding: 5px 8px; font: inherit; font-size: .74rem; }
.realtime-env-search input:focus-visible, .realtime-task-panel button:focus-visible { outline: 2px solid var(--accent-focus); outline-offset: 2px; }
.realtime-environment-list { display: grid; gap: 8px; margin: 0; padding: 0; list-style: none; }
.realtime-environment-row { min-width: 0; display: grid; gap: 6px; padding: 9px; border: 1px solid var(--border-subtle); border-radius: 7px; background: var(--surface-muted); }
.realtime-environment-name { min-width: 0; display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.realtime-environment-name code { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--text); font-size: .74rem; font-weight: 600; }
.realtime-environment-value { min-width: 0; max-height: 180px; overflow: auto; margin: 0; border: 1px solid var(--border-subtle); border-radius: 5px; background: var(--surface-code); color: var(--text); padding: 7px 8px; font: .7rem/1.45 ui-monospace, SFMono-Regular, Menlo, monospace; white-space: pre-wrap; overflow-wrap: anywhere; word-break: break-word; }
.realtime-environment-value--masked { color: var(--muted); letter-spacing: .08em; user-select: none; }
.realtime-environment-value--error { color: var(--error); }
.realtime-environment-actions { display: flex; justify-content: flex-end; gap: 6px; }
.realtime-environment-actions button { min-height: 28px; padding: 4px 8px; border-radius: 5px; font-size: .7rem; transform: none; filter: none; }
.realtime-inline-error { margin: 10px 14px 0; border: 1px solid var(--warning-border); border-radius: 7px; background: var(--warning-muted); color: var(--warning); padding: 8px 10px; font-size: .76rem; }

@media (max-width: 900px) {
  .realtime-task-panel { border-radius: 10px; background: var(--surface-modal); box-shadow: var(--shadow-popover); }
  .realtime-task-panel-scroll { max-height: calc(100dvh - 76px); }
}

@media (max-width: 460px) {
  .realtime-task-overview, .realtime-task-resources { grid-template-columns: minmax(0, 1fr); }
  .realtime-task-overview-wide { grid-column: auto; }
  .realtime-environment-title { align-items: stretch; flex-direction: column; }
  .realtime-env-search { width: 100%; }
  .realtime-env-search input { font-size: 16px; }
}
</style>
