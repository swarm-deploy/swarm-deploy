<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";

import { fetchEvents } from "../api/overview";
import type { EventHistoryItem, StackStatus } from "../api/types";
import { useOverviewStore } from "../stores/overview";
import { formatDate, shortCommitHash } from "../utils/format";

const overviewStore = useOverviewStore();
const deploymentEvents = ref<EventHistoryItem[]>([]);
const alertEvents = ref<EventHistoryItem[]>([]);
const overviewEventsError = ref("");

let refreshTimer: ReturnType<typeof setInterval> | undefined;

const syncInfo = computed(() => overviewStore.syncInfo);
const syncRevision = computed(() => String(syncInfo.value?.git_revision ?? "").trim());

function normalizeStackStatus(status?: StackStatus | null): StackStatus {
  return {
    synced: Number(status?.synced ?? 0),
    out_of_synced: Number(status?.out_of_synced ?? 0),
  };
}

function stackStatusClass(status?: StackStatus | null): string {
  const normalizedStatus = normalizeStackStatus(status);
  if (normalizedStatus.out_of_synced > 0) {
    return "failed";
  }
  if (normalizedStatus.synced > 0) {
    return "success";
  }

  return "unknown";
}

function stackStatusLabel(status?: StackStatus | null): string {
  const normalizedStatus = normalizeStackStatus(status);

  return `synced ${normalizedStatus.synced} | out of sync ${normalizedStatus.out_of_synced}`;
}

function normalizedSeverity(item: EventHistoryItem): "info" | "warn" | "error" | "alert" {
  switch (item.severity) {
    case "warn":
    case "error":
    case "alert":
      return item.severity;
    case "info":
    default:
      return "info";
  }
}

function deploymentResult(item: EventHistoryItem): string {
  if (item.type === "deploySuccess") {
    return "success";
  }
  if (item.type === "deployFailed") {
    return "failed";
  }

  return item.severity;
}

function deploymentResultClass(item: EventHistoryItem): string {
  return item.type === "deploySuccess" ? "success" : "failed";
}

function detailValue(item: EventHistoryItem, keys: string[]): string {
  const details = item.details ?? {};
  for (const key of keys) {
    const value = String(details[key] ?? "").trim();
    if (value) {
      return value;
    }
  }

  return "";
}

function sortedEventDetails(item: EventHistoryItem): [string, string][] {
  return Object.entries(item.details ?? {}).sort(([leftKey], [rightKey]) => leftKey.localeCompare(rightKey));
}

async function openCommitDetails(commitHash: string | undefined) {
  const hash = String(commitHash || "").trim();
  if (!hash) {
    return;
  }

  await overviewStore.openCommitDetailsModal(hash);
}

async function openStackManifest(stackName: string) {
  const stack = String(stackName || "").trim();
  if (!stack) {
    return;
  }

  await overviewStore.openStackManifestModal(stack);
}

async function refreshOverview() {
  overviewEventsError.value = "";

  const [overviewResult, deploymentsResult, alertsResult] = await Promise.allSettled([
    overviewStore.loadOverview(),
    fetchEvents({ types: ["deploySuccess", "deployFailed"] }),
    fetchEvents({ severities: ["warn", "error", "alert"] }),
  ]);

  if (overviewResult.status === "rejected") {
    overviewStore.loadingError = overviewResult.reason instanceof Error ? overviewResult.reason.message : "Failed to load state";
  }
  if (deploymentsResult.status === "fulfilled") {
    deploymentEvents.value = Array.isArray(deploymentsResult.value.events) ? deploymentsResult.value.events.slice(-5).reverse() : [];
  } else {
    deploymentEvents.value = [];
    overviewEventsError.value =
      deploymentsResult.reason instanceof Error ? deploymentsResult.reason.message : "Failed to load latest deployments";
  }
  if (alertsResult.status === "fulfilled") {
    alertEvents.value = Array.isArray(alertsResult.value.events) ? alertsResult.value.events.slice(-5).reverse() : [];
  } else {
    alertEvents.value = [];
    overviewEventsError.value =
      overviewEventsError.value ||
      (alertsResult.reason instanceof Error ? alertsResult.reason.message : "Failed to load alerts");
  }
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

  <section class="overview-summary-grid" aria-label="Overview highlights">
    <article class="stack-card overview-latest-deployments">
      <h2 class="overview-panel-title">Latest Deployments</h2>
      <p v-if="overviewEventsError && deploymentEvents.length === 0" class="meta">
        Failed to load latest deployments: {{ overviewEventsError }}
      </p>
      <p v-else-if="deploymentEvents.length === 0" class="meta">No deployments recorded yet.</p>
      <div v-else class="overview-deployment-list">
        <article
          v-for="event in deploymentEvents"
          :key="`${event.type}-${event.created_at}-${event.message}`"
          class="overview-deployment-item"
        >
          <div class="overview-deployment-main">
            <strong>{{ detailValue(event, ["stack", "stack_name"]) || "unknown stack" }}</strong>
            <span class="status" :class="deploymentResultClass(event)">{{ deploymentResult(event) }}</span>
          </div>
          <div class="overview-deployment-meta">
            <span>{{ formatDate(event.created_at) }}</span>
            <button
              v-if="detailValue(event, ['commit', 'revision'])"
              type="button"
              class="stack-commit-badge status unknown"
              @click="openCommitDetails(detailValue(event, ['commit', 'revision']))"
            >
              {{ shortCommitHash(detailValue(event, ["commit", "revision"])) }}
            </button>
            <span v-if="detailValue(event, ['trigger', 'triggered_by'])">
              trigger: {{ detailValue(event, ["trigger", "triggered_by"]) }}
            </span>
          </div>
        </article>
      </div>
    </article>

    <article class="stack-card overview-alerts">
      <h2 class="overview-panel-title">Alerts</h2>
      <p v-if="overviewEventsError && alertEvents.length === 0" class="meta">Failed to load alerts: {{ overviewEventsError }}</p>
      <p v-else-if="alertEvents.length === 0" class="meta">No important events recorded.</p>
      <div v-else class="overview-alert-list">
        <article v-for="event in alertEvents" :key="`${event.type}-${event.created_at}-${event.message}`" class="overview-alert-item">
          <p class="event-item-header">
            <span class="event-severity" :class="`event-severity-${normalizedSeverity(event)}`">
              {{ normalizedSeverity(event) }}
            </span>
            <strong>{{ event.type || "unknown" }}</strong>
          </p>
          <p class="meta">{{ event.message || "No message" }}</p>
          <ul v-if="sortedEventDetails(event).length > 0" class="event-details overview-alert-details">
            <li v-for="[key, value] in sortedEventDetails(event).slice(0, 3)" :key="key" class="event-detail">
              <span class="event-detail-key">{{ key }}</span>
              <code class="event-detail-value">{{ value }}</code>
            </li>
          </ul>
        </article>
      </div>
    </article>
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
        <span class="status stack-card-status" :class="stackStatusClass(stack.status)">
          {{ stackStatusLabel(stack.status) }}
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
        <div class="stack-card-actions">
          <button
            type="button"
            class="service-copy-task-id-button stack-manifest-open-button"
            aria-label="Show stack manifest"
            title="Show stack manifest"
            @click="openStackManifest(stack.name)"
          >
            <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">
              <g transform="translate(0 -1028.4)">
                <path
                  d="m5 1030.4c-1.1046 0-2 0.9-2 2v8 4 6c0 1.1 0.8954 2 2 2h14c1.105 0 2-0.9 2-2v-6-4-4l-6-6h-10z"
                  fill="#95a5a6"
                />
                <path
                  d="m5 1029.4c-1.1046 0-2 0.9-2 2v8 4 6c0 1.1 0.8954 2 2 2h14c1.105 0 2-0.9 2-2v-6-4-4l-6-6h-10z"
                  fill="#bdc3c7"
                />
                <path d="m21 1035.4-6-6v4c0 1.1 0.895 2 2 2h4z" fill="#95a5a6" />
                <path
                  d="m6 8v1h12v-1h-12zm0 3v1h12v-1h-12zm0 3v1h12v-1h-12zm0 3v1h12v-1h-12z"
                  transform="translate(0 1028.4)"
                  fill="#95a5a6"
                />
              </g>
            </svg>
          </button>
        </div>
      </article>
    </div>
  </section>
</template>
