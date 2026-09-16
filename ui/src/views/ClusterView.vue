<script setup lang="ts">
import { computed, onMounted, ref } from "vue";

import { addNodeLabel, deleteNodeLabel, fetchNodes, updateNodeLabel } from "../api/cluster";
import type { NodeInfo } from "../api/types";
import { formatBytes, formatNanoCPU } from "../utils/format";

interface LabelEntry {
  key: string;
  value: string;
  text: string;
}

interface LabelDialogState {
  mode: "add" | "edit";
  node: NodeInfo;
  originalKey: string;
  key: string;
  value: string;
  error: string;
  saving: boolean;
}

const nodes = ref<NodeInfo[]>([]);
const loading = ref(false);
const loadingError = ref("");
const labelDialog = ref<LabelDialogState | null>(null);

const nodeSummary = computed(() => {
  const total = nodes.value.length;
  const ready = nodes.value.filter((node) => normalize(node.status) === "ready").length;
  const managers = nodes.value.filter((node) => isManager(node)).length;
  const workers = total - managers;

  return `${total} ${plural(total, "node")} / ${ready} ready / ${managers} ${plural(managers, "manager")} / ${workers} ${plural(workers, "worker")}`;
});

function statusClass(status: string): string {
  return normalize(status) === "ready" ? "node-status-ready" : "node-status-warning";
}

function showNodeStatus(node: NodeInfo): boolean {
  return normalize(node.status) !== "ready";
}

function roleClass(node: NodeInfo): string {
  const managerStatus = normalize(node.manager_status);
  if (managerStatus === "leader") {
    return "node-role-leader";
  }
  if (isManager(node)) {
    return "node-role-manager";
  }

  return "node-role-worker";
}

function roleLabel(node: NodeInfo): string {
  const managerStatus = normalize(node.manager_status);
  if (managerStatus === "leader") {
    return "Leader";
  }
  if (isManager(node)) {
    return "Manager";
  }

  return "Worker";
}

function availabilityClass(availability: string): string {
  return normalize(availability) === "drain" ? "node-availability-drain" : "node-availability-pause";
}

function showAvailability(node: NodeInfo): boolean {
  const availability = normalize(node.availability);
  return availability === "drain" || availability === "pause";
}

function nodeCPU(node: NodeInfo): string {
  return node.cpu_nano > 0 ? formatNanoCPU(node.cpu_nano) : "n/a";
}

function nodeRAM(node: NodeInfo): string {
  return node.memory_bytes > 0 ? formatBytes(node.memory_bytes) : "n/a";
}

function nodeResources(node: NodeInfo): string {
  return `${nodeCPU(node)} CPU · ${nodeRAM(node)} RAM`;
}

function nodeEngine(node: NodeInfo): string {
  return node.engine_version ? `Docker ${node.engine_version}` : "Docker n/a";
}

function labelEntries(labels?: Record<string, string>): LabelEntry[] {
  return Object.entries(labels ?? {})
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([key, value]) => ({
      key,
      value,
      text: `${key}=${value}`,
    }));
}

async function loadNodes() {
  loading.value = true;
  loadingError.value = "";

  try {
    const response = await fetchNodes();
    const nextNodes = Array.isArray(response.nodes) ? response.nodes : [];
    nodes.value = [...nextNodes].sort((left, right) => left.hostname.localeCompare(right.hostname));
  } catch (error) {
    loadingError.value = error instanceof Error ? error.message : "Failed to load nodes";
    nodes.value = [];
  } finally {
    loading.value = false;
  }
}

function openAddLabel(node: NodeInfo) {
  labelDialog.value = {
    mode: "add",
    node,
    originalKey: "",
    key: "",
    value: "true",
    error: "",
    saving: false,
  };
}

function openEditLabel(node: NodeInfo, label: LabelEntry) {
  labelDialog.value = {
    mode: "edit",
    node,
    originalKey: label.key,
    key: label.key,
    value: label.value,
    error: "",
    saving: false,
  };
}

function closeLabelDialog() {
  if (labelDialog.value?.saving) {
    return;
  }

  labelDialog.value = null;
}

async function submitLabelDialog() {
  const dialog = labelDialog.value;
  if (!dialog) {
    return;
  }

  const key = dialog.key.trim();
  if (dialog.mode === "add" && !key) {
    dialog.error = "Label key is required.";
    return;
  }

  dialog.saving = true;
  dialog.error = "";

  try {
    if (dialog.mode === "add") {
      await addNodeLabel(dialog.node.id, { key, value: dialog.value });
    } else {
      await updateNodeLabel(dialog.node.id, dialog.originalKey, { value: dialog.value });
    }
    await loadNodes();
    labelDialog.value = null;
  } catch (error) {
    dialog.error = error instanceof Error ? error.message : "Failed to update node label";
  } finally {
    dialog.saving = false;
  }
}

async function removeCurrentLabel() {
  const dialog = labelDialog.value;
  if (!dialog || dialog.mode !== "edit") {
    return;
  }

  dialog.saving = true;
  dialog.error = "";

  try {
    await deleteNodeLabel(dialog.node.id, dialog.originalKey);
    await loadNodes();
    labelDialog.value = null;
  } catch (error) {
    dialog.error = error instanceof Error ? error.message : "Failed to remove node label";
  } finally {
    dialog.saving = false;
  }
}

function normalize(value: string): string {
  return value.trim().toLowerCase();
}

function isManager(node: NodeInfo): boolean {
  const managerStatus = normalize(node.manager_status);
  return managerStatus !== "" && managerStatus !== "worker";
}

function plural(count: number, single: string): string {
  return count === 1 ? single : `${single}s`;
}

onMounted(() => {
  void loadNodes();
});
</script>

<template>
  <section class="services-page nodes-page">
    <header class="services-header nodes-header">
      <div>
        <h2>Nodes</h2>
        <p class="meta nodes-summary">{{ nodeSummary }}</p>
      </div>
    </header>

    <div v-if="loading && nodes.length === 0" class="services-empty">
      <p class="meta">Loading...</p>
    </div>

    <div v-else-if="loadingError" class="services-empty">
      <p class="meta">Failed to load nodes: {{ loadingError }}</p>
    </div>

    <div v-else-if="nodes.length === 0" class="services-empty">
      <p class="meta">No nodes found.</p>
    </div>

    <div v-else class="node-list">
      <article v-for="node in nodes" :key="node.id" class="node-card">
        <div class="node-main-row">
          <div class="node-title-group">
            <span class="node-status-dot" :class="statusClass(node.status || '')" aria-hidden="true"></span>
            <h3 class="node-name" :title="node.hostname || 'unknown'">{{ node.hostname || "unknown" }}</h3>
          </div>

          <div class="node-state-group">
            <span v-if="showNodeStatus(node)" class="node-state-text">
              {{ node.status || "unknown" }}
            </span>
            <span class="node-role" :class="roleClass(node)">
              {{ roleLabel(node) }}
            </span>
            <span v-if="showAvailability(node)" class="node-availability" :class="availabilityClass(node.availability)">
              {{ node.availability }}
            </span>
          </div>
        </div>

        <div class="node-meta-list">
          <div class="node-meta-row">
            <span class="node-meta-key">Address</span>
            <span class="node-meta-value">{{ node.addr || "n/a" }}</span>
          </div>

          <div class="node-meta-row">
            <span class="node-meta-key">Resources</span>
            <span class="node-meta-value">{{ nodeResources(node) }}</span>
          </div>

          <div class="node-meta-row">
            <span class="node-meta-key">Engine</span>
            <span class="node-meta-value">{{ nodeEngine(node) }}</span>
          </div>

          <div class="node-meta-row">
            <span class="node-meta-key">Labels</span>
            <div class="node-meta-value node-label-list">
              <button
                v-for="label in labelEntries(node.labels)"
                :key="label.key"
                type="button"
                class="node-label-chip"
                :title="`Edit ${label.text}`"
                @click="openEditLabel(node, label)"
              >
                {{ label.text }}
              </button>
              <span v-if="labelEntries(node.labels).length === 0" class="meta node-label-empty">none</span>
              <button type="button" class="node-label-add" @click="openAddLabel(node)">+ Add label</button>
            </div>
          </div>
        </div>
      </article>
    </div>

    <div v-if="labelDialog" class="modal">
      <div class="modal-overlay" @click="closeLabelDialog"></div>
      <div class="modal-card node-label-dialog" role="dialog" aria-modal="true" aria-labelledby="node-label-title">
        <div class="modal-header">
          <h2 id="node-label-title">
            {{ labelDialog.mode === "add" ? "Add label" : "Edit label" }}
          </h2>
          <button class="modal-close" type="button" aria-label="Close modal" :disabled="labelDialog.saving" @click="closeLabelDialog">
            x
          </button>
        </div>

        <form class="node-label-form" @submit.prevent="submitLabelDialog">
          <label>
            <span>Key</span>
            <input v-model="labelDialog.key" type="text" :readonly="labelDialog.mode === 'edit'" :disabled="labelDialog.saving" />
          </label>

          <label>
            <span>Value</span>
            <input v-model="labelDialog.value" type="text" :disabled="labelDialog.saving" />
          </label>

          <p v-if="labelDialog.error" class="meta node-label-error">{{ labelDialog.error }}</p>

          <div class="node-label-dialog-actions">
            <button
              v-if="labelDialog.mode === 'edit'"
              type="button"
              class="button-danger"
              :disabled="labelDialog.saving"
              @click="removeCurrentLabel"
            >
              Remove
            </button>
            <span class="node-label-dialog-spacer"></span>
            <button type="button" class="button-ghost" :disabled="labelDialog.saving" @click="closeLabelDialog">Cancel</button>
            <button type="submit" :disabled="labelDialog.saving">
              {{ labelDialog.saving ? "Saving..." : labelDialog.mode === "add" ? "Add" : "Save" }}
            </button>
          </div>
        </form>
      </div>
    </div>
  </section>
</template>
