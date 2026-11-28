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

function artifactTypeLabel(artifact: AssistantArtifact): string {
  if (artifact.type === "pull_request") return t("assistant.artifactPullRequest");
  if (artifact.type === "git_branch") return t("assistant.artifactBranch");
  return t("assistant.artifact");
}
</script>

<template>
  <div class="assistant-artifacts" :class="{ compact }">
    <span v-if="!compact" class="assistant-artifacts-title">{{ t('common.created') }}</span>
    <div
      v-for="(artifact, index) in artifacts"
      :key="`${artifact.type}-${artifact.label}-${index}`"
      class="assistant-artifact"
      :class="`assistant-artifact-${artifactClass(artifact)}`"
    >
      <span class="assistant-artifact-icon" aria-hidden="true">
        <svg v-if="artifact.type === 'pull_request'" viewBox="0 0 24 24">
          <circle cx="6" cy="5" r="2" />
          <circle cx="18" cy="19" r="2" />
          <path d="M6 7v12M18 17V9a4 4 0 0 0-4-4h-2" />
          <path d="m14 2-3 3 3 3" />
        </svg>
        <svg v-else-if="artifact.type === 'git_branch'" viewBox="0 0 24 24">
          <circle cx="6" cy="5" r="2" />
          <circle cx="18" cy="7" r="2" />
          <circle cx="6" cy="19" r="2" />
          <path d="M6 7v10M8 13h3a7 7 0 0 0 7-4" />
        </svg>
        <svg v-else viewBox="0 0 24 24">
          <path d="M8 12h8M12 8v8" />
          <circle cx="12" cy="12" r="8" />
        </svg>
      </span>

      <a
        v-if="artifact.url"
        class="assistant-artifact-label"
        :href="artifact.url"
        target="_blank"
        rel="noreferrer"
        :title="artifact.label"
        :aria-label="t('assistant.openArtifact', { label: artifact.label })"
      >
        <span v-if="!compact" class="assistant-artifact-type">{{ artifactTypeLabel(artifact) }}</span>
        <span class="assistant-artifact-value" :class="{ code: artifact.type === 'git_branch' && !compact }">
          <span class="assistant-artifact-value-text">{{ artifact.label }}</span>
          <svg v-if="!compact" class="assistant-artifact-external" viewBox="0 0 24 24" aria-hidden="true">
            <path d="M14 5h5v5" />
            <path d="m19 5-9 9" />
            <path d="M19 13v5a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1h5" />
          </svg>
        </span>
      </a>
      <span
        v-else
        class="assistant-artifact-label"
        :title="artifact.label"
      >
        <span v-if="!compact" class="assistant-artifact-type">{{ artifactTypeLabel(artifact) }}</span>
        <span class="assistant-artifact-value" :class="{ code: artifact.type === 'git_branch' && !compact }">
          <span class="assistant-artifact-value-text">{{ artifact.label }}</span>
        </span>
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
  margin-top: 9px;
}

.assistant-artifacts-title {
  margin-bottom: 3px;
  color: var(--muted);
  font-size: 0.76rem;
  font-weight: 500;
}

.assistant-artifact {
  min-width: 0;
  min-height: 52px;
  display: grid;
  grid-template-columns: 34px minmax(0, 1fr) auto;
  align-items: center;
  gap: 9px;
  padding: 8px 2px;
  color: var(--muted);
}

.assistant-artifact + .assistant-artifact {
  border-top: 1px solid var(--border-default);
}

.assistant-artifact-icon {
  width: 32px;
  height: 32px;
  display: grid;
  place-items: center;
  border-radius: 8px;
  background: var(--accent-muted);
  color: var(--accent-strong);
}

.assistant-artifact-icon > svg,
.assistant-artifact-external {
  fill: none;
  stroke: currentColor;
  stroke-width: 1.7;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.assistant-artifact-icon > svg {
  width: 17px;
  height: 17px;
}

.assistant-artifact-label {
  min-width: 0;
  min-height: 36px;
  display: grid;
  align-content: center;
  gap: 2px;
  overflow: hidden;
  color: var(--text);
}

a.assistant-artifact-label {
  color: var(--accent-strong);
  text-decoration: none;
}

.assistant-artifact-type {
  color: var(--muted);
  font-size: 0.72rem;
  line-height: 1.15;
}

.assistant-artifact-value {
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.84rem;
  font-weight: 550;
  line-height: 1.2;
  white-space: nowrap;
}

.assistant-artifact-value-text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.assistant-artifact-value.code {
  font-family: "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 0.76rem;
  font-weight: 500;
}

.assistant-artifact-pull-request .assistant-artifact-value {
  font-size: 0.88rem;
  font-weight: 650;
}

.assistant-artifact-external {
  width: 14px;
  height: 14px;
  flex: 0 0 auto;
}

a.assistant-artifact-label:hover .assistant-artifact-value {
  text-decoration: underline;
}

a.assistant-artifact-label:focus-visible {
  border-radius: 4px;
  outline: 2px solid var(--accent-focus);
  outline-offset: 2px;
}

.assistant-artifact :deep(.app-copy-button) {
  width: 40px;
  height: 40px;
}

.assistant-artifacts.compact {
  max-width: min(42%, 130px);
  flex-shrink: 0;
  margin: 0;
}

.compact .assistant-artifact {
  min-height: 28px;
  display: grid;
  grid-template-columns: 14px minmax(0, 1fr);
  gap: 5px;
  padding: 2px 6px;
  border-color: transparent;
  background: transparent;
}

.compact .assistant-artifact-icon {
  width: 14px;
  height: 14px;
  border-radius: 0;
  background: transparent;
  color: var(--muted);
}

.compact .assistant-artifact-icon > svg {
  width: 14px;
  height: 14px;
}

.compact .assistant-artifact-label {
  min-height: 0;
  display: block;
}

.compact .assistant-artifact-value {
  display: block;
  overflow: hidden;
  font-size: 0.74rem;
  font-weight: 550;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (max-width: 640px) {
  .assistant-artifacts.compact {
    max-width: 38%;
  }
}
</style>
