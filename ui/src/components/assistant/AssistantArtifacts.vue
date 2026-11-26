<script setup lang="ts">
import type { AssistantArtifact } from "../../api/types";
import { t } from "../../i18n";
import AppCopyButton from "../common/AppCopyButton.vue";

defineProps<{
  artifacts: AssistantArtifact[];
  compact?: boolean;
}>();

function artifactClass(artifact: AssistantArtifact): string {
  if (artifact.type === "pull_request") return "pull-request";
  if (artifact.type === "git_branch") return "git-branch";
  return "unknown";
}
</script>

<template>
  <div class="assistant-artifacts" :class="{ compact }">
    <div
      v-for="(artifact, index) in artifacts"
      :key="`${artifact.type}-${artifact.label}-${index}`"
      class="assistant-artifact"
      :class="`assistant-artifact-${artifactClass(artifact)}`"
    >
      <svg v-if="artifact.type === 'pull_request'" viewBox="0 0 24 24" aria-hidden="true">
        <circle cx="6" cy="5" r="2" />
        <circle cx="18" cy="19" r="2" />
        <path d="M6 7v12M18 17V9a4 4 0 0 0-4-4h-2" />
        <path d="m14 2-3 3 3 3" />
      </svg>
      <svg v-else-if="artifact.type === 'git_branch'" viewBox="0 0 24 24" aria-hidden="true">
        <circle cx="6" cy="5" r="2" />
        <circle cx="18" cy="7" r="2" />
        <circle cx="6" cy="19" r="2" />
        <path d="M6 7v10M8 13h3a7 7 0 0 0 7-4" />
      </svg>
      <svg v-else viewBox="0 0 24 24" aria-hidden="true">
        <path d="M8 12h8M12 8v8" />
        <circle cx="12" cy="12" r="8" />
      </svg>

      <a
        v-if="artifact.url"
        class="assistant-artifact-label"
        :class="{ code: artifact.type === 'git_branch' && !compact }"
        :href="artifact.url"
        target="_blank"
        rel="noreferrer"
        :title="artifact.label"
        :aria-label="t('assistant.openArtifact', { label: artifact.label })"
      >
        {{ artifact.label }}
      </a>
      <span
        v-else
        class="assistant-artifact-label"
        :class="{ code: artifact.type === 'git_branch' && !compact }"
        :title="artifact.label"
      >
        {{ artifact.label }}
      </span>

      <AppCopyButton
        v-if="artifact.type === 'git_branch' && !compact"
        :value="artifact.label"
        :label="t('assistant.copyBranch')"
        :copied-label="t('assistant.branchCopied')"
        color="var(--muted)"
      />
    </div>
  </div>
</template>

<style scoped>
.assistant-artifacts {
  min-width: 0;
  display: grid;
  gap: 4px;
  margin-top: 9px;
}

.assistant-artifact {
  min-width: 0;
  min-height: 30px;
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 2px 6px;
  border: 1px solid var(--border-default);
  border-radius: 6px;
  background: var(--surface-muted);
  color: var(--muted);
}

.assistant-artifact > svg {
  width: 16px;
  height: 16px;
  flex: 0 0 auto;
  fill: none;
  stroke: currentColor;
  stroke-width: 1.7;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.assistant-artifact-label {
  min-width: 0;
  flex: 1 1 auto;
  overflow: hidden;
  color: var(--text);
  font-size: 0.8rem;
  font-weight: 550;
  text-overflow: ellipsis;
  white-space: nowrap;
}

a.assistant-artifact-label {
  color: var(--accent-strong);
  text-decoration: none;
}

a.assistant-artifact-label:hover {
  text-decoration: underline;
}

.assistant-artifact-label.code {
  font-family: var(--font-mono);
  font-size: 0.76rem;
}

.assistant-artifacts.compact {
  max-width: min(42%, 130px);
  flex: 0 1 auto;
  margin: 0;
}

.compact .assistant-artifact {
  min-height: 28px;
  gap: 5px;
  padding: 2px 6px;
  border-color: transparent;
  background: transparent;
}

.compact .assistant-artifact > svg {
  width: 14px;
  height: 14px;
}

.compact .assistant-artifact-label {
  font-size: 0.74rem;
}

@media (max-width: 640px) {
  .assistant-artifacts.compact {
    max-width: 38%;
  }
}
</style>
