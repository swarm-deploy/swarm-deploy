<script setup lang="ts">
import { computed } from "vue";

import type { StackStatus, StackView } from "../../api/types";
import { formatDate, shortCommitHash } from "../../utils/format";

const props = defineProps<{
  stack: StackView;
  serviceCount: number;
}>();

const emit = defineEmits<{
  compare: [stackName: string];
  commit: [commitHash: string];
}>();

const normalizedStatus = computed<StackStatus>(() => ({
  synced: Number(props.stack.status?.synced ?? 0),
  out_of_synced: Number(props.stack.status?.out_of_synced ?? 0),
}));

const statusKind = computed<"synced" | "out-of-sync" | "unknown">(() => {
  if (normalizedStatus.value.out_of_synced > 0) {
    return "out-of-sync";
  }
  if (normalizedStatus.value.synced > 0) {
    return "synced";
  }

  return "unknown";
});

const statusLabel = computed(() => {
  if (statusKind.value === "out-of-sync") {
    return `Out of sync ${normalizedStatus.value.out_of_synced}`;
  }
  if (statusKind.value === "synced") {
    return "Synced";
  }

  return "Unknown";
});

const serviceLabel = computed(() => `${props.serviceCount} ${props.serviceCount === 1 ? "service" : "services"}`);
</script>

<template>
  <article class="stack-card overview-stack-card" :class="`overview-stack-card--${statusKind}`">
    <header class="overview-stack-card-header">
      <h3 class="overview-stack-card-title" :title="stack.name">{{ stack.name }}</h3>
      <div class="overview-stack-card-badges">
        <span class="overview-stack-status-badge" :class="`overview-stack-status-badge--${statusKind}`">
          <span class="overview-stack-status-icon" aria-hidden="true">
            {{ statusKind === "synced" ? "✓" : statusKind === "out-of-sync" ? "!" : "?" }}
          </span>
          {{ statusLabel }}
        </span>
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
            @click="emit('commit', stack.last_commit)"
          >
            {{ shortCommitHash(stack.last_commit) }}
          </button>
          <span v-else class="overview-stack-metadata-empty">n/a</span>
        </dd>
      </div>
    </dl>

    <footer class="overview-stack-card-actions">
      <button type="button" class="overview-stack-card-action" @click="emit('compare', stack.name)">
        Compare state
      </button>
    </footer>
  </article>
</template>
