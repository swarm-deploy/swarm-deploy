<script setup lang="ts">
import { computed } from "vue";

import type { StackView } from "../../api/types";
import { stackSyncStatus } from "../../utils/stackStatus";
import { formatDate, shortCommitHash } from "../../utils/format";
import AppCard from "../common/AppCard.vue";
import AppStatusBadge from "../common/AppStatusBadge.vue";

const props = defineProps<{
  stack: StackView;
  serviceCount: number;
}>();

const emit = defineEmits<{
  compare: [stackName: string];
  commit: [commitHash: string];
}>();

const status = computed(() => stackSyncStatus(props.stack.status));
const statusKind = computed(() => status.value.kind);
const statusLabel = computed(() => status.value.label);
const statusVariant = computed(() => ({
  synced: "success",
  "out-of-sync": "warning",
  unknown: "neutral",
} as const)[statusKind.value]);

const serviceLabel = computed(() => `${props.serviceCount} ${props.serviceCount === 1 ? "service" : "services"}`);
const stackAnchor = computed(() => `stack-${encodeURIComponent(props.stack.name)}`);
</script>

<template>
  <AppCard :id="stackAnchor" class="stack-card overview-stack-card" :class="`overview-stack-card--${statusKind}`">
    <header class="overview-stack-card-header">
      <h3 class="overview-stack-card-title" :title="stack.name"><RouterLink class="stack-card-link" :to="{ name: 'stack-details', params: { stack: stack.name } }">{{ stack.name }}</RouterLink></h3>
      <div class="overview-stack-card-badges">
        <AppStatusBadge
          class="overview-stack-status-badge"
          :class="`overview-stack-status-badge--${statusKind}`"
          :variant="statusVariant"
          filled
        >
          {{ statusLabel }}
        </AppStatusBadge>
        <span class="overview-stack-services-badge">{{ serviceLabel }}</span>
      </div>
    </header>

    <dl class="overview-stack-card-metadata">
      <div>
        <dt>Last deploy</dt>
        <dd>{{ formatDate(stack.last_deploy_at) }}</dd>
      </div>
      <div>
        <dt>Commit</dt>
        <dd>
          <button
            v-if="stack.last_commit"
            type="button"
            class="overview-commit-badge overview-stack-commit-badge"
            :aria-label="`Open commit ${shortCommitHash(stack.last_commit)}`"
            :title="stack.last_commit"
            @click.stop="emit('commit', stack.last_commit)"
          >
            {{ shortCommitHash(stack.last_commit) }}
          </button>
          <span v-else class="overview-stack-metadata-empty">n/a</span>
        </dd>
      </div>
    </dl>

    <footer class="overview-stack-card-actions">
      <button type="button" class="overview-stack-card-action" @click.stop="emit('compare', stack.name)">
        Compare state
      </button>
    </footer>
  </AppCard>
</template>

<style scoped>
.overview-stack-card { position: relative; }
.stack-card-link { color: inherit; text-decoration: none; }
.stack-card-link::after { content: ""; position: absolute; inset: 0; }
.stack-card-link:focus-visible { outline: none; }
.overview-stack-card:has(.stack-card-link:focus-visible) { outline: 2px solid var(--accent-focus); outline-offset: 2px; }
.overview-stack-card-actions, .overview-stack-commit-badge { position: relative; z-index: 1; }
.overview-stack-card-actions { pointer-events: none; }
.overview-stack-card-action { pointer-events: auto; }
</style>
