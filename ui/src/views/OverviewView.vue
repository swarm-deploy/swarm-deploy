<script setup lang="ts">
import { computed, onMounted, onUnmounted } from "vue";

import { useOverviewStore } from "../stores/overview";
import { formatDate, shortCommitHash } from "../utils/format";

const overviewStore = useOverviewStore();

let refreshTimer: ReturnType<typeof setInterval> | undefined;

const syncInfo = computed(() => overviewStore.syncInfo);
const syncRevision = computed(() => String(syncInfo.value?.git_revision ?? "").trim());

function isStatusClass(status: string, expected: string): boolean {
  return status.toLowerCase() === expected;
}

async function openCommitDetails(commitHash: string | undefined) {
  const hash = String(commitHash || "").trim();
  if (!hash) {
    return;
  }

  await overviewStore.openCommitDetailsModal(hash);
}

async function refreshOverview() {
  await overviewStore.loadOverview();
}

onMounted(async () => {
  await refreshOverview();
  refreshTimer = setInterval(() => {
    void refreshOverview();
  }, 10000);
});

onUnmounted(() => {
  if (refreshTimer) {
    clearInterval(refreshTimer);
    refreshTimer = undefined;
  }
});
</script>

<template>
  <section class="status-panel">
    <p v-if="overviewStore.loadingError" class="meta">Failed to load state: {{ overviewStore.loadingError }}</p>
    <p v-else-if="!syncInfo" class="meta">Sync status is unavailable.</p>
    <p v-else class="meta">
      Last sync: {{ formatDate(syncInfo.last_sync_at) }} | reason: {{ syncInfo.last_sync_reason || "n/a" }} | result:
      {{ syncInfo.last_sync_result || "n/a" }} | revision:
      <button
        v-if="syncRevision"
        type="button"
        class="stack-commit-badge status unknown"
        @click="openCommitDetails(syncRevision)"
      >
        {{ shortCommitHash(syncRevision) }}
      </button>
      <span v-else> n/a</span>
      <template v-if="syncInfo.last_sync_error"> | error: {{ syncInfo.last_sync_error }}</template>
    </p>
  </section>

  <section>
    <div v-if="overviewStore.loading && overviewStore.stacks.length === 0" class="stack-grid">
      <article class="stack-card">
        <p class="meta">Loading...</p>
      </article>
    </div>

    <div v-else-if="overviewStore.stacks.length === 0" class="stack-grid">
      <article class="stack-card">
        <p class="meta">No stacks configured.</p>
      </article>
    </div>

    <div v-else class="stack-grid">
      <article v-for="stack in overviewStore.stacks" :key="stack.name" class="stack-card">
        <h3 class="stack-title">{{ stack.name }}</h3>
        <span
          class="status stack-card-status"
          :class="{
            success: isStatusClass(stack.last_status || 'unknown', 'success'),
            failed: isStatusClass(stack.last_status || 'unknown', 'failed'),
            unknown: !isStatusClass(stack.last_status || 'unknown', 'success') && !isStatusClass(stack.last_status || 'unknown', 'failed'),
          }"
        >
          {{ (stack.last_status || "unknown").toLowerCase() }}
        </span>
        <p class="meta">compose: {{ stack.compose_file }}</p>
        <p class="meta">last deploy: {{ formatDate(stack.last_deploy_at) }}</p>
        <p class="meta">
          commit:
          <button
            v-if="stack.last_commit"
            type="button"
            class="stack-commit-badge status unknown"
            @click="openCommitDetails(stack.last_commit)"
          >
            {{ shortCommitHash(stack.last_commit) }}
          </button>
          <span v-else> n/a</span>
        </p>
        <p v-if="stack.last_error" class="meta">error: {{ stack.last_error }}</p>
      </article>
    </div>
  </section>
</template>
