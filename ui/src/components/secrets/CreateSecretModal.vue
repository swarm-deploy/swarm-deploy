<script setup lang="ts">
import json from "@speed-highlight/core/languages/json.js";
import { tokenizeWith } from "@speed-highlight/core/tokenize";
import { computed, onMounted, onUnmounted, ref, watch } from "vue";

import { createExternalSecret, createSecret, syncSecretManager } from "../../api/secrets";
import type { SecretManagerInfo } from "../../api/types";
import AppTabs from "../common/AppTabs.vue";
import PathInput from "./PathInput.vue";

type Destination = "docker" | "cloud";
type ValueMode = "text" | "json";
type ProviderKind = "vault" | "cloudru" | "unknown";

const props = defineProps<{
  open: boolean;
  managers: SecretManagerInfo[];
}>();

const emit = defineEmits<{
  close: [];
  created: [message: string];
}>();

const vaultProviderName = "HashiCorp Vault";
const cloudRuProviderName = "Cloud.ru Secret Manager";

const destination = ref<Destination>("docker");
const valueMode = ref<ValueMode>("text");
const revealValue = ref(false);
const name = ref("");
const path = ref("");
const key = ref("");
const value = ref("");
const description = ref("");
const selectedManagerKey = ref("");
const pending = ref(false);
const errorMessage = ref("");
const partialSuccess = ref(false);
const normalizedPath = ref("");
const jsonHighlight = ref<HTMLElement>();

function providerKind(manager?: SecretManagerInfo): ProviderKind {
  if (manager?.provider?.name === vaultProviderName) return "vault";
  if (manager?.provider?.name === cloudRuProviderName) return "cloudru";
  return "unknown";
}

const availableManagers = computed(() => props.managers.filter((manager) => (
  manager.kind === "cloud-secrets"
  && manager.controllable
  && manager.available
  && providerKind(manager) !== "unknown"
)));

function managerIdentity(manager: SecretManagerInfo): string {
  return `${manager.stack}/${manager.service}`;
}

const selectedManager = computed(() => availableManagers.value.find((manager) => (
  managerIdentity(manager) === selectedManagerKey.value
)));
const selectedProviderKind = computed(() => providerKind(selectedManager.value));
const destinationTabs = computed(() => [
  { value: "docker", label: "Docker Swarm", disabled: partialSuccess.value },
  { value: "cloud", label: "Cloud Secrets", disabled: partialSuccess.value },
]);

const jsonError = computed(() => {
  if (valueMode.value !== "json" || value.value === "") return "";
  try {
    JSON.parse(value.value);
    return "";
  } catch (error) {
    return error instanceof SyntaxError ? error.message : "Invalid JSON";
  }
});

const highlightedJSON = computed(() => {
  let highlighted = "";
  tokenizeWith(value.value, json, (text, token) => {
    const escaped = text.replaceAll("&", "&#38;").replaceAll("<", "&lt;").replaceAll(">", "&gt;");
    highlighted += token ? `<span class="shj-syn-${token}">${escaped}</span>` : escaped;
  });
  return highlighted || " ";
});

const formValid = computed(() => {
  if (partialSuccess.value) return Boolean(selectedManager.value);
  if (value.value === "" || jsonError.value) return false;
  if (destination.value === "docker") return name.value.trim() !== "";
  if (!selectedManager.value || path.value.trim() === "") return false;
  return selectedProviderKind.value !== "vault" || key.value.trim() !== "";
});

const submitLabel = computed(() => {
  if (pending.value) return partialSuccess.value ? "Retrying Sync…" : "Creating…";
  if (partialSuccess.value) return "Retry Sync";
  return destination.value === "docker" ? "Create Secret" : "Create & Sync";
});

function resetForm(): void {
  destination.value = "cloud";
  valueMode.value = "text";
  revealValue.value = false;
  name.value = "";
  path.value = "";
  key.value = "";
  value.value = "";
  description.value = "";
  selectedManagerKey.value = availableManagers.value[0] ? managerIdentity(availableManagers.value[0]) : "";
  pending.value = false;
  errorMessage.value = "";
  partialSuccess.value = false;
  normalizedPath.value = "";
}

watch(() => props.open, (open) => {
  if (open) resetForm();
}, { immediate: true });

watch(availableManagers, (managers) => {
  if (!managers.some((manager) => managerIdentity(manager) === selectedManagerKey.value)) {
    selectedManagerKey.value = managers[0] ? managerIdentity(managers[0]) : "";
  }
});

watch(selectedManagerKey, () => {
  if (!partialSuccess.value) {
    key.value = "";
    description.value = "";
    errorMessage.value = "";
  }
});

function setDestination(next: string): void {
  if (next !== "docker" && next !== "cloud") return;
  if (pending.value || partialSuccess.value) return;
  destination.value = next;
  errorMessage.value = "";
}

function setValueMode(next: ValueMode): void {
  valueMode.value = next;
}

function formatJSON(): void {
  if (jsonError.value || value.value === "") return;
  value.value = JSON.stringify(JSON.parse(value.value), null, 2);
}

function syncJSONScroll(event: Event): void {
  const textarea = event.currentTarget as HTMLTextAreaElement;
  if (jsonHighlight.value) {
    jsonHighlight.value.scrollTop = textarea.scrollTop;
    jsonHighlight.value.scrollLeft = textarea.scrollLeft;
  }
}

function close(): void {
  if (!pending.value) emit("close");
}

async function synchronizeSelectedManager(): Promise<void> {
  const manager = selectedManager.value;
  if (!manager) return;

  try {
    await syncSecretManager(manager.stack, manager.service);
    emit("created", "Secret created in the external provider and synchronization completed.");
  } catch (error) {
    partialSuccess.value = true;
    errorMessage.value = error instanceof Error ? error.message : "Synchronization failed";
  }
}

async function submit(): Promise<void> {
  if (!formValid.value || pending.value) return;
  pending.value = true;
  errorMessage.value = "";

  try {
    if (partialSuccess.value) {
      await synchronizeSelectedManager();
      return;
    }
    if (destination.value === "docker") {
      await createSecret({
        name: name.value.trim(),
        value: value.value,
        ...(description.value.trim() ? { description: description.value.trim() } : {}),
      });
      emit("created", `Docker secret ${name.value.trim()} created.`);
      return;
    }

    const manager = selectedManager.value;
    if (!manager) return;
    const response = await createExternalSecret(manager.stack, manager.service, {
      path: path.value,
      value: value.value,
      ...(selectedProviderKind.value === "vault" ? { key: key.value } : {}),
      ...(selectedProviderKind.value === "cloudru" && description.value.trim()
        ? { description: description.value.trim() }
        : {}),
    });
    normalizedPath.value = response.path;
    partialSuccess.value = true;
    value.value = "";
    await synchronizeSelectedManager();
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : "Secret creation failed";
  } finally {
    pending.value = false;
  }
}

function handleEscape(event: KeyboardEvent): void {
  if (event.key === "Escape" && props.open) close();
}

onMounted(() => document.addEventListener("keydown", handleEscape));
onUnmounted(() => document.removeEventListener("keydown", handleEscape));
</script>

<template>
  <div v-if="open" class="modal create-secret-modal">
    <div class="modal-overlay" @click="close" />
    <div class="modal-card create-secret-dialog" role="dialog" aria-modal="true" aria-labelledby="create-secret-title">
      <div class="modal-header">
        <h2 id="create-secret-title">Create Secret</h2>
        <button class="modal-close" type="button" aria-label="Close modal" :disabled="pending" @click="close">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" aria-hidden="true"><path d="m7 7 10 10M17 7 7 17" /></svg>
        </button>
      </div>

      <form class="modal-body create-secret-form" @submit.prevent="submit">
        <AppTabs
          class="create-secret-tabs"
          :model-value="destination"
          :tabs="destinationTabs"
          label="Secret destination"
          density="compact"
          @update:model-value="setDestination"
        />

        <label v-if="destination === 'docker'" class="create-secret-control">
          <span class="create-secret-control-icon" title="Name" aria-hidden="true">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><path d="M4 5.5h9.2L20 12l-6.8 6.5H4z" /><circle cx="8" cy="9.5" r="1" /></svg>
          </span>
          <input v-model="name" type="text" autocomplete="off" aria-label="Name" placeholder="Secret name" :disabled="pending || partialSuccess" required />
        </label>

        <template v-else>
          <label class="create-secret-control create-secret-manager-control">
            <span class="create-secret-control-icon" title="Secret Manager" aria-hidden="true">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><path d="m12 3 8 5-8 5-8-5z" /><path d="m4 12 8 5 8-5M4 16l8 5 8-5" /></svg>
            </span>
            <select v-model="selectedManagerKey" aria-label="Secret Manager" :disabled="pending || partialSuccess || availableManagers.length === 0" required>
              <option v-for="manager in availableManagers" :key="managerIdentity(manager)" :value="managerIdentity(manager)">
                {{ manager.stack }}/{{ manager.service }} — {{ manager.provider?.name }}
              </option>
            </select>
            <span class="create-secret-manager-copy" aria-hidden="true">
              <strong>{{ selectedManager ? managerIdentity(selectedManager) : "Select Secret Manager" }}</strong>
              <small v-if="selectedManager?.provider?.name">{{ selectedManager.provider.name }}</small>
            </span>
            <span class="create-secret-chevron" aria-hidden="true">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="m7 9 5 5 5-5" /></svg>
            </span>
          </label>

          <p v-if="availableManagers.length === 0" class="create-secret-notice create-secret-notice--warning">
            No available, controllable Vault or Cloud.ru Secret Manager was discovered. Docker Swarm creation remains available.
          </p>

          <PathInput v-model="path" :disabled="pending || partialSuccess || !selectedManager" required />

          <label v-if="selectedProviderKind === 'vault'" class="create-secret-control">
            <span class="create-secret-control-icon" title="Key" aria-hidden="true">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><circle cx="8.5" cy="15.5" r="4" /><path d="m11.5 12.5 8-8m-2 2 2 2m-5 1 2 2" /></svg>
            </span>
            <input v-model="key" type="text" autocomplete="off" aria-label="Key" placeholder="Secret key" :disabled="pending || partialSuccess" required />
          </label>
        </template>

        <label v-if="destination === 'docker' || selectedProviderKind === 'cloudru'" class="create-secret-control">
          <span class="create-secret-control-icon" title="Description" aria-hidden="true">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><path d="M5 4h14v16H5zM8 8h8m-8 4h8m-8 4h5" /></svg>
          </span>
          <input v-model="description" type="text" autocomplete="off" aria-label="Description" placeholder="Description (optional)" :disabled="pending || partialSuccess" />
        </label>

        <div class="create-secret-editor" :class="{ 'create-secret-editor--error': Boolean(jsonError) }">
          <div class="create-secret-editor-toolbar">
            <span class="create-secret-editor-icon" title="Value" aria-hidden="true">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><path d="m8 7-5 5 5 5m8-10 5 5-5 5m-2-13-4 16" /></svg>
            </span>
            <div class="create-secret-value-tools">
              <div class="create-secret-mode-toggle" aria-label="Value format">
                <button type="button" :class="{ active: valueMode === 'text' }" :aria-pressed="valueMode === 'text'" @click="setValueMode('text')">Text</button>
                <button type="button" :class="{ active: valueMode === 'json' }" :aria-pressed="valueMode === 'json'" @click="setValueMode('json')">JSON</button>
              </div>
              <span class="create-secret-tool-divider" aria-hidden="true" />
              <button v-if="valueMode === 'text'" type="button" class="create-secret-small-action" @click="revealValue = !revealValue">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true"><path d="M2.5 12s3.5-6 9.5-6 9.5 6 9.5 6-3.5 6-9.5 6-9.5-6-9.5-6Z" /><circle cx="12" cy="12" r="2.5" /></svg>
                {{ revealValue ? "Hide" : "Reveal" }}
              </button>
              <button v-else type="button" class="create-secret-small-action" :disabled="Boolean(jsonError) || value === ''" @click="formatJSON">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true"><path d="M4 4h6v2H6v4H4zm16 0h-6v2h4v4h2zM4 20h6v-2H6v-4H4zm16 0h-6v-2h4v-4h2z" /></svg>
                Format
              </button>
            </div>
          </div>
          <textarea
            v-if="valueMode === 'text'"
            v-model="value"
            class="create-secret-value"
            :class="{ 'create-secret-value--masked': !revealValue }"
            rows="5"
            spellcheck="false"
            autocomplete="new-password"
            :disabled="pending || partialSuccess"
            aria-label="Value"
            required
          />
          <div v-else class="create-secret-json-editor">
            <pre ref="jsonHighlight" class="create-secret-json-highlight" aria-hidden="true" v-html="highlightedJSON" />
            <textarea
              v-model="value"
              class="create-secret-value create-secret-value--json"
              rows="5"
              spellcheck="false"
              autocomplete="off"
              :disabled="pending || partialSuccess"
              aria-label="Value"
              :aria-invalid="Boolean(jsonError)"
              :aria-describedby="jsonError ? 'create-secret-json-error' : undefined"
              required
              @scroll="syncJSONScroll"
            />
          </div>
        </div>
        <p v-if="jsonError" id="create-secret-json-error" class="create-secret-inline-error">Invalid JSON: {{ jsonError }}</p>

        <p v-if="destination === 'cloud'" class="create-secret-helper">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true"><circle cx="12" cy="12" r="9" /><path d="M12 11v6m0-10h.01" /></svg>
          <span>The secret is created in the external provider first, then synchronized into Docker Swarm.</span>
        </p>

        <div v-if="partialSuccess && errorMessage" class="create-secret-notice create-secret-notice--partial" role="alert">
          <strong>Secret created in the external provider, but synchronization with Docker Swarm failed.</strong>
          <span v-if="normalizedPath">Provider path: <code>{{ normalizedPath }}</code></span>
          <span>{{ errorMessage }}</span>
        </div>
        <p v-else-if="errorMessage" class="create-secret-inline-error" role="alert">{{ errorMessage }}</p>

        <div class="create-secret-actions">
          <button type="button" class="button-ghost" :disabled="pending" @click="close">Cancel</button>
          <button type="submit" class="create-secret-submit" :disabled="!formValid || pending">{{ submitLabel }}</button>
        </div>
      </form>
    </div>
  </div>
</template>

<style scoped>
.create-secret-dialog { width: min(560px, 94vw); display: flex; flex-direction: column; overflow: hidden; }
.create-secret-form { gap: 11px; overflow-y: auto; padding: 0 20px 18px; }
.create-secret-tabs { flex: 0 0 auto; gap: 0; margin: 0; }
.create-secret-tabs :deep(.app-tabs-tab) { flex: 1; justify-content: center; border-bottom-width: 2px; border-radius: 0; padding: 12px 8px 10px; font-size: .82rem; }
.create-secret-tabs :deep(.app-tabs-tab--active) { color: var(--control-primary); }
.create-secret-control { position: relative; display: flex; align-items: center; min-width: 0; height: 40px; border: 1px solid var(--border-default); border-radius: 6px; background: var(--surface-input); color: var(--text); }
.create-secret-control:focus-within { border-color: var(--accent-border); outline: 2px solid var(--accent-focus); outline-offset: 1px; }
.create-secret-control:has(:user-invalid) { border-color: var(--error); outline-color: var(--error-border); }
.create-secret-control-icon { flex: 0 0 auto; width: 42px; display: grid; place-items: center; color: var(--muted); }
.create-secret-control-icon svg, .create-secret-chevron svg, .create-secret-editor-icon svg { width: 19px; height: 19px; }
.create-secret-control input { min-width: 0; width: 100%; height: 100%; border: 0; outline: 0; background: transparent; color: var(--text); padding: 0 11px 0 0; font: inherit; font-size: .84rem; }
.create-secret-control input::placeholder { color: var(--muted); opacity: .88; }
.create-secret-manager-control { height: 46px; }
.create-secret-manager-control select { position: absolute; z-index: 2; inset: 0; width: 100%; height: 100%; border: 0; opacity: 0; cursor: pointer; }
.create-secret-manager-control select:disabled { cursor: not-allowed; }
.create-secret-manager-copy { min-width: 0; display: grid; flex: 1; gap: 1px; padding-right: 8px; line-height: 1.15; }
.create-secret-manager-copy strong, .create-secret-manager-copy small { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.create-secret-manager-copy strong { font-size: .82rem; font-weight: 500; }
.create-secret-manager-copy small { color: var(--muted); font-size: .68rem; }
.create-secret-chevron { width: 36px; display: grid; place-items: center; color: var(--muted); }
.create-secret-chevron svg { width: 17px; height: 17px; }
.create-secret-manager-control:has(select:disabled), .create-secret-control:has(input:disabled) { opacity: .58; }
.create-secret-editor { overflow: hidden; border: 1px solid var(--border-default); border-radius: 6px; background: var(--surface-input); }
.create-secret-editor:focus-within { border-color: var(--accent-border); outline: 2px solid var(--accent-focus); outline-offset: 1px; }
.create-secret-editor--error { border-color: var(--error); }
.create-secret-editor--error:focus-within { border-color: var(--error); outline-color: var(--error-border); }
.create-secret-editor-toolbar { min-height: 38px; display: flex; align-items: center; justify-content: space-between; gap: 10px; border-bottom: 1px solid var(--border-default); padding: 0 7px 0 12px; }
.create-secret-editor-icon { display: grid; place-items: center; color: var(--muted); }
.create-secret-value-tools { display: flex; align-items: stretch; align-self: stretch; margin-left: auto; }
.create-secret-mode-toggle { display: flex; align-items: stretch; }
.create-secret-mode-toggle button, .create-secret-small-action { border: 0; border-bottom: 2px solid transparent; border-radius: 0; background: transparent; color: var(--muted); padding: 0 9px; font: inherit; font-size: .74rem; transform: none; filter: none; }
.create-secret-mode-toggle button:hover:not(:disabled), .create-secret-small-action:hover:not(:disabled) { color: var(--text); background: var(--surface-hover); transform: none; filter: none; }
.create-secret-mode-toggle button:focus-visible, .create-secret-small-action:focus-visible { outline: 2px solid var(--accent-focus); outline-offset: -2px; }
.create-secret-mode-toggle button.active { border-bottom-color: var(--control-primary); color: var(--control-primary); }
.create-secret-tool-divider { width: 1px; align-self: center; height: 17px; margin: 0 2px; background: var(--border-default); }
.create-secret-small-action { display: inline-flex; align-items: center; gap: 5px; }
.create-secret-small-action svg { width: 15px; height: 15px; }
.create-secret-value { display: block; width: 100%; min-height: 132px; resize: vertical; border: 0; border-radius: 0; outline: 0; background: transparent; color: var(--text); padding: 11px 13px; font-size: .82rem; line-height: 1.45; font-family: "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; }
.create-secret-value--masked { -webkit-text-security: disc; }
.create-secret-json-editor { position: relative; min-height: 132px; }
.create-secret-json-highlight { position: absolute; z-index: 0; inset: 0; margin: 0; overflow: hidden; padding: 11px 13px; color: var(--text); font: inherit; font-family: "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; font-size: .82rem; line-height: 1.45; tab-size: 2; white-space: pre-wrap; overflow-wrap: anywhere; pointer-events: none; }
.create-secret-value--json { position: relative; z-index: 1; min-height: 132px; background: transparent; color: transparent; caret-color: var(--text); tab-size: 2; overflow-wrap: anywhere; }
.create-secret-value--json::selection { background: var(--surface-selected-strong); color: transparent; }
.create-secret-json-highlight :deep(.shj-syn-str) { color: var(--syntax-string); }
.create-secret-json-highlight :deep(.shj-syn-num), .create-secret-json-highlight :deep(.shj-syn-bool), .create-secret-json-highlight :deep(.shj-syn-type) { color: var(--syntax-number); }
.create-secret-json-highlight :deep(.shj-syn-var) { color: var(--syntax-variable); }
.create-secret-helper { display: flex; align-items: flex-start; gap: 8px; margin: 1px 2px 0; color: var(--muted); font-size: .76rem; line-height: 1.4; }
.create-secret-helper svg { flex: 0 0 auto; width: 17px; height: 17px; color: var(--muted); }
.create-secret-inline-error { margin: -2px 0 0; color: var(--error); font-size: .78rem; overflow-wrap: anywhere; }
.create-secret-notice { display: grid; gap: 3px; margin: -2px 2px 0; padding: 0; font-size: .76rem; line-height: 1.4; }
.create-secret-notice--warning { color: var(--warning); }
.create-secret-notice--partial { border-left: 2px solid var(--warning-border); color: var(--text); padding-left: 8px; }
.create-secret-notice--partial span { color: var(--muted); }
.create-secret-notice code { overflow-wrap: anywhere; }
.create-secret-actions { display: flex; justify-content: flex-end; gap: 9px; padding-top: 1px; }
.create-secret-actions button { min-width: 104px; min-height: 38px; padding: 8px 14px; }
.create-secret-submit { border-color: var(--accent-border); border-radius: 6px; background: var(--control-primary); font-weight: 600; box-shadow: none; }
.create-secret-submit:hover:not(:disabled) { background: var(--control-primary-hover); filter: none; }
.create-secret-submit:focus-visible { outline: 2px solid var(--accent-focus); outline-offset: 2px; box-shadow: none; }
button:disabled, input:disabled, select:disabled, textarea:disabled { cursor: not-allowed; opacity: .58; }
@media (max-width: 520px) {
  .create-secret-form { padding-inline: 12px; }
  .create-secret-editor-toolbar { padding-left: 9px; }
  .create-secret-mode-toggle button, .create-secret-small-action { padding-inline: 7px; }
  .create-secret-actions button { min-width: 0; flex: 1; }
}
</style>
