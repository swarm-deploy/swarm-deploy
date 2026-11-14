<script setup lang="ts">
import yaml from "@speed-highlight/core/languages/yaml.js";
import { tokenizeWith } from "@speed-highlight/core/tokenize";
import { computed, ref, watch } from "vue";

import AppModal from "../common/AppModal.vue";
import { useOverviewStore } from "../../stores/overview";
import AppTabs from "../common/AppTabs.vue";

type ManifestTab = "desired" | "live";

const overviewStore = useOverviewStore();
const activeTab = ref<ManifestTab>("desired");
const tabs = [
  { value: "desired", label: "Desired", id: "desired-manifest-tab", panelId: "stack-manifest-panel" },
  { value: "live", label: "Live", id: "live-manifest-tab", panelId: "stack-manifest-panel" },
] as const;

const modalTitle = computed(() => {
  const stackName = String(overviewStore.stackManifestStack || "").trim();
  if (!stackName) {
    return "Stack manifest";
  }

  return `${stackName} manifest`;
});
const desiredManifest = computed(() => overviewStore.stackManifestDesired);
const liveManifest = computed(() => overviewStore.stackManifestLive);
const desiredManifestHtml = computed(() => highlightManifest(desiredManifest.value));
const liveManifestHtml = computed(() => highlightManifest(liveManifest.value));
const activeManifest = computed(() => (activeTab.value === "desired" ? desiredManifest.value : liveManifest.value));
const activeManifestHtml = computed(() =>
  activeTab.value === "desired" ? desiredManifestHtml.value : liveManifestHtml.value,
);
const activeManifestLabel = computed(() =>
  activeTab.value === "desired" ? "Desired manifest yaml" : "Live manifest yaml",
);
const activeManifestEmptyMessage = computed(() =>
  activeTab.value === "desired" ? "Desired manifest is empty." : "Live manifest is empty.",
);

function escapeHtml(value: string) {
  return value.replaceAll("&", "&#38;").replaceAll("<", "&lt;").replaceAll(">", "&gt;");
}

function highlightManifest(manifest: string) {
  if (!manifest.trim()) {
    return "";
  }

  let highlighted = "";
  tokenizeWith(manifest, yaml, (text, token) => {
    const escapedText = escapeHtml(text);
    highlighted += token ? `<span class="shj-syn-${token}">${escapedText}</span>` : escapedText;
  });

  const lineCount = manifest.split(/\r\n|\r|\n/).length;
  const lineNumbers = "<div></div>".repeat(lineCount);
  return `<div><div class="shj-numbers" aria-hidden="true">${lineNumbers}</div><div>${highlighted}</div></div>`;
}

function setActiveTab(tab: string) {
  if (tab !== "desired" && tab !== "live") return;
  activeTab.value = tab;
}

function closeStackManifestModal() {
  overviewStore.closeStackManifestModal();
}

watch(
  () => overviewStore.stackManifestModalOpen,
  (modalOpen) => {
    if (modalOpen) {
      activeTab.value = "desired";
    }
  },
);
</script>

<template>
  <AppModal
    :open="overviewStore.stackManifestModalOpen"
    labelledby="stack-manifest-title"
    keep-mounted
    card-class="manifest-modal-card"
    @close="closeStackManifestModal"
  >
    <template #title>
      <h2 id="stack-manifest-title">{{ modalTitle }}</h2>
    </template>

    <div class="modal-body">
      <p v-if="overviewStore.stackManifestLoading" class="meta">Loading manifest...</p>
      <p v-else-if="overviewStore.stackManifestError" class="meta">
        Failed to load manifest: {{ overviewStore.stackManifestError }}
      </p>
      <template v-else>
        <AppTabs
          class="manifest-tabs"
          :model-value="activeTab"
          :tabs="tabs"
          label="Stack manifest tabs"
          @update:model-value="setActiveTab"
        />

        <div
          id="stack-manifest-panel"
          class="manifest-viewer"
          role="tabpanel"
          :aria-labelledby="`${activeTab}-manifest-tab`"
        >
          <p v-if="activeManifest.trim().length === 0" class="meta">{{ activeManifestEmptyMessage }}</p>
          <div
            v-else
            class="manifest-code shj-lang-yaml shj-block"
            :aria-label="activeManifestLabel"
            v-html="activeManifestHtml"
          />
        </div>
      </template>
    </div>
  </AppModal>
</template>
