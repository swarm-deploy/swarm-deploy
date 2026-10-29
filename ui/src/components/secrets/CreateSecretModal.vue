<script setup lang="ts">
import json from "@speed-highlight/core/languages/json.js";
import { tokenizeWith } from "@speed-highlight/core/tokenize";
import { computed, onMounted, onUnmounted, ref, watch } from "vue";

import { createExternalSecret, createSecret, syncSecretManager } from "../../api/secrets";
import type { SecretManagerInfo } from "../../api/types";

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

function setDestination(next: Destination): void {
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
        <div class="create-secret-segments" role="tablist" aria-label="Secret destination">
          <button
            type="button"
            role="tab"
            :aria-selected="destination === 'docker'"
            :class="{ active: destination === 'docker' }"
            :disabled="partialSuccess"
            @click="setDestination('docker')"
          >Docker Swarm</button>
          <button
            type="button"
            role="tab"
            :aria-selected="destination === 'cloud'"
            :class="{ active: destination === 'cloud' }"
            :disabled="partialSuccess"
            @click="setDestination('cloud')"
          >Cloud Secrets</button>
        </div>

        <label v-if="destination === 'docker'" class="create-secret-field">
          <span>Name <b aria-hidden="true">*</b></span>
          <input v-model="name" type="text" autocomplete="off" :disabled="pending || partialSuccess" required />
        </label>

        <template v-else>
          <label class="create-secret-field">
            <span>Secret Manager <b aria-hidden="true">*</b></span>
            <select v-model="selectedManagerKey" :disabled="pending || partialSuccess || availableManagers.length === 0" required>
              <option v-for="manager in availableManagers" :key="managerIdentity(manager)" :value="managerIdentity(manager)">
                {{ manager.stack }}/{{ manager.service }} — {{ manager.provider?.name }}
              </option>
            </select>
          </label>

          <p v-if="availableManagers.length === 0" class="create-secret-notice create-secret-notice--warning">
            No available, controllable Vault or Cloud.ru Secret Manager was discovered. Docker Swarm creation remains available.
          </p>

          <label class="create-secret-field">
            <span>Path <b aria-hidden="true">*</b></span>
            <input v-model="path" type="text" autocomplete="off" :disabled="pending || partialSuccess || !selectedManager" required />
          </label>

          <label v-if="selectedProviderKind === 'vault'" class="create-secret-field">
            <span>Key <b aria-hidden="true">*</b></span>
            <input v-model="key" type="text" autocomplete="off" :disabled="pending || partialSuccess" required />
          </label>
        </template>

        <div class="create-secret-value-heading">
          <span>Value <b aria-hidden="true">*</b></span>
          <div class="create-secret-value-tools">
            <div class="create-secret-mode-toggle" aria-label="Value format">
              <button type="button" :class="{ active: valueMode === 'text' }" @click="setValueMode('text')">Text</button>
              <button type="button" :class="{ active: valueMode === 'json' }" @click="setValueMode('json')">JSON</button>
            </div>
            <button v-if="valueMode === 'text'" type="button" class="button-ghost create-secret-small-action" @click="revealValue = !revealValue">
              {{ revealValue ? "Hide" : "Reveal" }}
            </button>
            <button v-else type="button" class="button-ghost create-secret-small-action" :disabled="Boolean(jsonError) || value === ''" @click="formatJSON">
              Format JSON
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
          aria-label="Secret value"
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
            aria-label="Secret value"
            required
            @scroll="syncJSONScroll"
          />
        </div>
        <p v-if="jsonError" class="create-secret-inline-error">Invalid JSON: {{ jsonError }}</p>

        <label v-if="destination === 'docker' || selectedProviderKind === 'cloudru'" class="create-secret-field">
          <span>Description <small>optional</small></span>
          <input v-model="description" type="text" autocomplete="off" :disabled="pending || partialSuccess" />
        </label>

        <p v-if="destination === 'cloud'" class="create-secret-helper">
          The secret is created in the external provider first, then synchronized into Docker Swarm.
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
.create-secret-dialog { width: min(560px, 94vw); }
.create-secret-form { gap: 9px; }
.create-secret-segments { display: grid; grid-template-columns: 1fr 1fr; padding: 3px; border: 1px solid var(--border-default); border-radius: 8px; background: var(--surface-muted); }
.create-secret-segments button, .create-secret-mode-toggle button { border: 0; border-radius: 6px; background: transparent; color: var(--muted); padding: 7px 10px; font-size: .8rem; transform: none; filter: none; }
.create-secret-segments button.active, .create-secret-mode-toggle button.active { background: var(--surface-selected-strong); color: var(--text); }
.create-secret-field { display: grid; gap: 5px; color: var(--muted); font-size: .82rem; }
.create-secret-field > span, .create-secret-value-heading > span { font-weight: 600; }
.create-secret-field b, .create-secret-value-heading b { color: var(--error); }
.create-secret-field small { color: var(--muted); font-weight: 400; }
.create-secret-field input, .create-secret-field select, .create-secret-value { width: 100%; border: 1px solid var(--border-default); border-radius: 8px; background: var(--surface-input); color: var(--text); padding: 9px 11px; font: inherit; }
.create-secret-field input:focus, .create-secret-field select:focus, .create-secret-value:focus { outline: 2px solid var(--accent-focus); outline-offset: 1px; }
.create-secret-value-heading { display: flex; align-items: center; justify-content: space-between; gap: 10px; color: var(--muted); font-size: .82rem; }
.create-secret-value-tools { display: flex; align-items: center; gap: 5px; }
.create-secret-mode-toggle { display: flex; padding: 2px; border: 1px solid var(--border-default); border-radius: 7px; background: var(--surface-muted); }
.create-secret-mode-toggle button { padding: 4px 8px; font-size: .74rem; }
.create-secret-small-action { padding: 5px 8px; border-radius: 7px; font-size: .74rem; }
.create-secret-value { min-height: 104px; resize: vertical; font-size: .82rem; line-height: 1.4; font-family: "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; }
.create-secret-value--masked { -webkit-text-security: disc; }
.create-secret-json-editor { position: relative; min-height: 104px; }
.create-secret-json-highlight { position: absolute; z-index: 0; inset: 0; margin: 0; overflow: hidden; border: 1px solid transparent; padding: 9px 11px; color: var(--text); font: inherit; font-family: "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace; font-size: .82rem; line-height: 1.4; tab-size: 2; white-space: pre-wrap; overflow-wrap: anywhere; pointer-events: none; }
.create-secret-value--json { position: relative; z-index: 1; min-height: 132px; background: transparent; color: transparent; caret-color: var(--text); tab-size: 2; overflow-wrap: anywhere; }
.create-secret-value--json::selection { background: var(--surface-selected-strong); color: transparent; }
.create-secret-json-highlight :deep(.shj-syn-str) { color: var(--syntax-string); }
.create-secret-json-highlight :deep(.shj-syn-num), .create-secret-json-highlight :deep(.shj-syn-bool), .create-secret-json-highlight :deep(.shj-syn-type) { color: var(--syntax-number); }
.create-secret-json-highlight :deep(.shj-syn-var) { color: var(--syntax-variable); }
.create-secret-helper { margin: 0; color: var(--muted); font-size: .78rem; line-height: 1.4; }
.create-secret-inline-error { margin: -2px 0 0; color: var(--error); font-size: .78rem; overflow-wrap: anywhere; }
.create-secret-notice { display: grid; gap: 4px; margin: 0; border: 1px solid var(--border-default); border-radius: 8px; padding: 9px 10px; font-size: .78rem; line-height: 1.4; }
.create-secret-notice--warning { border-color: var(--warning-border); background: var(--warning-muted); color: var(--warning); }
.create-secret-notice--partial { border-color: var(--warning-border); background: var(--warning-muted); color: var(--text); }
.create-secret-notice--partial span { color: var(--muted); }
.create-secret-notice code { overflow-wrap: anywhere; }
.create-secret-actions { display: flex; justify-content: flex-end; gap: 8px; padding-top: 3px; }
.create-secret-actions button { padding: 8px 13px; }
.create-secret-submit { border-color: var(--accent-border); border-radius: 6px; background: var(--control-primary); font-weight: 600; box-shadow: none; }
.create-secret-submit:hover:not(:disabled) { background: var(--control-primary-hover); filter: none; }
.create-secret-submit:focus-visible { outline: 2px solid var(--accent-focus); outline-offset: 2px; box-shadow: none; }
button:disabled, input:disabled, select:disabled, textarea:disabled { cursor: not-allowed; opacity: .58; }
@media (max-width: 520px) {
  .create-secret-value-heading { align-items: flex-start; flex-direction: column; gap: 5px; }
  .create-secret-value-tools { width: 100%; justify-content: space-between; }
}
</style>
