<script setup lang="ts">
import { formatNumber, plural, t } from "../../i18n";
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
  pause: [stackName: string];
}>();

const status = computed(() => stackSyncStatus(props.stack.status));
const statusKind = computed(() => status.value.kind);
const statusLabel = computed(() => status.value.label);
const statusVariant = computed(() => ({
  synced: "success",
  "out-of-sync": "warning",
  unknown: "neutral",
} as const)[statusKind.value]);

const serviceLabel = computed(() => plural("common.serviceCount", props.serviceCount));
const pausedLabel = computed(() => t("stack.pausedCount", { count: formatNumber(props.stack.effectively_paused_services ?? 0) }));
const stackPauseActive = computed(() => props.stack.stack_pause_active ?? false);
const syncActionLabel = computed(() => stackPauseActive.value ? t("syncPause.resumeStack") : t("common.pauseSync"));
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
        <span v-if="stack.effectively_paused_services" class="overview-stack-services-badge">{{ pausedLabel }}</span>
      </div>
    </header>

    <dl class="overview-stack-card-metadata">
      <div>
        <dt>{{ t('stack.lastDeploy') }}</dt>
        <dd>{{ formatDate(stack.last_deploy_at) }}</dd>
      </div>
      <div>
        <dt>{{ t('common.commit') }}</dt>
        <dd>
          <button
            v-if="stack.last_commit"
            type="button"
            class="overview-commit-badge overview-stack-commit-badge"
            :aria-label="t('common.openCommit', { commit: shortCommitHash(stack.last_commit) })"
            :title="stack.last_commit"
            @click.stop="emit('commit', stack.last_commit)"
          >
            {{ shortCommitHash(stack.last_commit) }}
          </button>
          <span v-else class="overview-stack-metadata-empty">{{ t('common.notApplicable') }}</span>
        </dd>
      </div>
    </dl>

    <footer class="overview-stack-card-actions">
      <button
        type="button"
        class="overview-stack-card-action app-tooltip"
        :aria-label="t('stack.compareState')"
        :data-tooltip="t('stack.compareState')"
        @click.stop="emit('compare', stack.name)"
      >
        <svg class="overview-stack-card-action-icon" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M14 2H6a2 2 0 0 0-2 2v16h7" />
          <path d="M14 2v4h4M14 2l4 4v5" />
          <path d="M8 9h6M8 12h3" />
          <circle cx="15" cy="15" r="4.5" />
          <path d="m18.25 18.25 3.25 3.25" />
        </svg>
      </button>
      <button
        type="button"
        class="overview-stack-card-action app-tooltip"
        :aria-label="syncActionLabel"
        :data-tooltip="syncActionLabel"
        @click.stop="emit('pause', stack.name)"
      >
        <svg class="overview-stack-card-action-icon" viewBox="0 0 24 24" aria-hidden="true">
          <circle cx="12" cy="12" r="9" />
          <path v-if="stackPauseActive" d="m10 8 6 4-6 4Z" />
          <path v-else d="M9.5 9v6M14.5 9v6" />
        </svg>
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
.overview-stack-card-action + .overview-stack-card-action { border-left: 1px solid var(--surface-hover-strong); }
.overview-stack-card-action-icon {
  width: 22px;
  height: 22px;
  flex: none;
  fill: none;
  stroke: currentColor;
  stroke-width: 1.5;
  stroke-linecap: round;
  stroke-linejoin: round;
}
.overview-stack-card-action.app-tooltip::after {
  top: auto;
  bottom: calc(100% + 6px);
}
.overview-stack-card-action.app-tooltip:first-child::after {
  left: 12px;
  transform: translateY(4px);
}
.overview-stack-card-action.app-tooltip:first-child:is(:hover, :focus-visible)::after {
  transform: translateY(0);
}
.overview-stack-card-action.app-tooltip:last-child::after {
  right: 12px;
  left: auto;
  transform: translateY(4px);
}
.overview-stack-card-action.app-tooltip:last-child:is(:hover, :focus-visible)::after {
  transform: translateY(0);
}
</style>
