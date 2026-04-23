<script setup lang="ts">
import { onMounted, ref } from "vue";

import { fetchNodes } from "../api/cluster";
import type { NodeInfo } from "../api/types";

const nodes = ref<NodeInfo[]>([]);
const loading = ref(false);
const loadingError = ref("");

function statusClass(status: string): "success" | "failed" | "unknown" {
  const normalizedStatus = status.trim().toLowerCase();
  if (normalizedStatus === "ready" || normalizedStatus === "active" || normalizedStatus === "up") {
    return "success";
  }
  if (
    normalizedStatus === "down" ||
    normalizedStatus === "failed" ||
    normalizedStatus === "error" ||
    normalizedStatus === "disconnected"
  ) {
    return "failed";
  }
  return "unknown";
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

onMounted(() => {
  void loadNodes();
});
</script>

<template>
  <section class="services-header">
    <h2>Nodes</h2>
  </section>

  <section>
    <div v-if="loading && nodes.length === 0" class="node-grid">
      <article class="node-card">
        <p class="meta">Loading...</p>
      </article>
    </div>

    <div v-else-if="loadingError" class="node-grid">
      <article class="node-card">
        <p class="meta">Failed to load nodes: {{ loadingError }}</p>
      </article>
    </div>

    <div v-else-if="nodes.length === 0" class="node-grid">
      <article class="node-card">
        <p class="meta">No nodes found.</p>
      </article>
    </div>

    <div v-else class="node-grid">
      <article v-for="node in nodes" :key="node.id" class="node-card">
        <div class="node-card-header">
          <h3 class="node-name">{{ node.hostname || "unknown" }}</h3>
          <span class="status" :class="statusClass(node.status || '')">
            {{ (node.status || "unknown").toLowerCase() }}
          </span>
        </div>
        <p class="meta">availability: {{ node.availability || "n/a" }}</p>
        <p class="meta">manager: {{ node.manager_status || "n/a" }}</p>
        <p class="meta">address: {{ node.addr || "n/a" }}</p>
        <p class="meta">engine: {{ node.engine_version || "n/a" }}</p>
        <p class="meta">id: {{ node.id || "n/a" }}</p>
      </article>
    </div>
  </section>
</template>
