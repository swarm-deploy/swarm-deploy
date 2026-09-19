<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";

import { fetchEvents, fetchRecommendations } from "../api/overview";
import type { EventHistoryItem, Recommendation, RecommendationSeverity, StackStatus } from "../api/types";
import OverviewCardAction from "../components/overview/OverviewCardAction.vue";
import { useOverviewStore } from "../stores/overview";
import { formatDate, shortCommitHash } from "../utils/format";

const overviewStore = useOverviewStore();
const deploymentEvents = ref<EventHistoryItem[]>([]);
const alertEvents = ref<EventHistoryItem[]>([]);
const recommendations = ref<Recommendation[]>([]);
const overviewEventsError = ref("");
const overviewRecommendationsError = ref("");
const deploymentEventsLimit = 4;
const alertEventsLimit = 4;
const recommendationsLimit = 4;
const recommendationSeverityRank: Record<RecommendationSeverity, number> = {
  high: 0,
  medium: 1,
  low: 2,
};

let refreshTimer: ReturnType<typeof setInterval> | undefined;

const syncInfo = computed(() => overviewStore.syncInfo);
const syncRevision = computed(() => String(syncInfo.value?.git_revision ?? "").trim());
const sortedRecommendations = computed(() =>
  recommendations.value
    .slice()
    .sort(
      (left, right) =>
        recommendationSeverityRank[left.severity] - recommendationSeverityRank[right.severity] ||
        left.subject.stack.localeCompare(right.subject.stack) ||
        left.subject.service.localeCompare(right.subject.service),
    ),
);
const overviewRecommendations = computed(() => sortedRecommendations.value.slice(0, recommendationsLimit));

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

function formatDateMinute(raw: string | undefined): string {
  if (!raw) {
    return "n/a";
  }

  const parsed = new Date(raw);
  if (Number.isNaN(parsed.valueOf())) {
    return raw;
  }

  const pad = (value: number) => String(value).padStart(2, "0");
  return `${pad(parsed.getDate())}.${pad(parsed.getMonth() + 1)}.${parsed.getFullYear()} ${pad(parsed.getHours())}:${pad(
    parsed.getMinutes(),
  )}`;
}

function recommendationSeverityClass(severity: RecommendationSeverity): string {
  return `overview-recommendation-severity-${severity}`;
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

function openAlertDetails(event: EventHistoryItem) {
  overviewStore.openAlertDetailsModal(event);
}

async function refreshOverview() {
  overviewEventsError.value = "";
  overviewRecommendationsError.value = "";

  const [overviewResult, deploymentsResult, alertsResult, recommendationsResult] = await Promise.allSettled([
    overviewStore.loadOverview(),
    fetchEvents({ types: ["deploySuccess", "deployFailed"], limit: deploymentEventsLimit }),
    fetchEvents({
      severities: ["alert"],
      since: new Date(Date.now() - 12 * 60 * 60 * 1000).toISOString(),
      limit: alertEventsLimit,
    }),
    fetchRecommendations({ limit: recommendationsLimit }),
  ]);

  if (overviewResult.status === "rejected") {
    overviewStore.loadingError = overviewResult.reason instanceof Error ? overviewResult.reason.message : "Failed to load state";
  }
  if (deploymentsResult.status === "fulfilled") {
    deploymentEvents.value = Array.isArray(deploymentsResult.value.events) ? deploymentsResult.value.events.reverse() : [];
  } else {
    deploymentEvents.value = [];
    overviewEventsError.value =
      deploymentsResult.reason instanceof Error ? deploymentsResult.reason.message : "Failed to load latest deployments";
  }
  if (alertsResult.status === "fulfilled") {
    alertEvents.value = Array.isArray(alertsResult.value.events) ? alertsResult.value.events.reverse() : [];
  } else {
    alertEvents.value = [];
    overviewEventsError.value =
      overviewEventsError.value ||
      (alertsResult.reason instanceof Error ? alertsResult.reason.message : "Failed to load alerts");
  }
  if (recommendationsResult.status === "fulfilled") {
    recommendations.value = Array.isArray(recommendationsResult.value.recommendations)
      ? recommendationsResult.value.recommendations
      : [];
  } else {
    recommendations.value = [];
    overviewRecommendationsError.value =
      recommendationsResult.reason instanceof Error
        ? recommendationsResult.reason.message
        : "Failed to load recommendations";
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
      <header class="overview-card-header">
        <h2 class="overview-panel-title">Latest Deployments</h2>
        <OverviewCardAction :to="{ path: '/events', query: { types: ['deploySuccess', 'deployFailed'] } }" />
      </header>
      <p v-if="overviewEventsError && deploymentEvents.length === 0" class="meta">
        Failed to load latest deployments: {{ overviewEventsError }}
      </p>
      <p v-else-if="deploymentEvents.length === 0" class="meta">No deployments recorded yet.</p>
      <div v-else class="overview-deployment-list">
        <article
          v-for="event in deploymentEvents.slice(0, deploymentEventsLimit)"
          :key="`${event.type}-${event.created_at}-${event.message}`"
          class="overview-deployment-item"
        >
          <p class="overview-deployment-row">
            <span class="overview-deployment-stack">{{ detailValue(event, ["stack", "stack_name"]) || "unknown stack" }}</span>
            <span class="overview-deployment-result" :class="deploymentResultClass(event)">
              {{ deploymentResult(event) }}
            </span>
            <span>{{ formatDateMinute(event.created_at) }}</span>
            <button
              v-if="detailValue(event, ['commit', 'revision'])"
              type="button"
              class="overview-deployment-revision"
              @click="openCommitDetails(detailValue(event, ['commit', 'revision']))"
            >
              {{ shortCommitHash(detailValue(event, ["commit", "revision"])) }}
            </button>
          </p>
        </article>
      </div>
    </article>

    <article class="stack-card overview-alerts" :class="{ 'overview-alerts-has-items': alertEvents.length > 0 }">
      <header class="overview-card-header">
        <h2 class="overview-panel-title">Alerts</h2>
        <OverviewCardAction to="/events?severity=alert" />
      </header>
      <p v-if="overviewEventsError && alertEvents.length === 0" class="meta">Failed to load alerts: {{ overviewEventsError }}</p>
      <p v-else-if="alertEvents.length === 0" class="meta">No alerts in the last 12 hours.</p>
      <div v-else class="overview-alert-list">
        <button
          v-for="event in alertEvents"
          :key="`${event.type}-${event.created_at}-${event.message}`"
          type="button"
          class="overview-alert-item"
          @click="openAlertDetails(event)"
        >
          <span class="overview-alert-row">
            <span class="overview-alert-message">{{ event.message || "No message" }}</span>
            <span class="overview-alert-time">{{ formatDateMinute(event.created_at) }}</span>
          </span>
        </button>
      </div>
    </article>

    <article
      class="stack-card overview-recommendations"
      :class="{ 'overview-recommendations-has-items': overviewRecommendations.length > 0 }"
    >
      <header class="overview-card-header">
        <h2 class="overview-panel-title">Recommendations</h2>
        <OverviewCardAction to="/recommendations" />
      </header>
      <p v-if="overviewRecommendationsError && overviewRecommendations.length === 0" class="meta">
        Failed to load recommendations: {{ overviewRecommendationsError }}
      </p>
      <p v-else-if="overviewRecommendations.length === 0" class="meta">No recommendations right now.</p>
      <div v-else class="overview-recommendation-list">
        <article
          v-for="recommendation in overviewRecommendations"
          :key="`${recommendation.severity}-${recommendation.subject.stack}-${recommendation.subject.service}-${recommendation.title}`"
          class="overview-recommendation-item"
        >
          <span
            class="overview-recommendation-severity-dot"
            :class="recommendationSeverityClass(recommendation.severity)"
            :title="recommendation.severity"
            aria-hidden="true"
          ></span>
          <span class="overview-recommendation-text">{{ recommendation.title }}</span>
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
