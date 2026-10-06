<script setup lang="ts">
import yaml from "@speed-highlight/core/languages/yaml.js";
import { tokenizeWith } from "@speed-highlight/core/tokenize";
import { computed, onMounted, onUnmounted, ref, watch } from "vue";

import { useOverviewStore } from "../../stores/overview";

type ManifestTab = "desired" | "live";

const overviewStore = useOverviewStore();
const activeTab = ref<ManifestTab>("desired");

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

function setActiveTab(tab: ManifestTab) {
  activeTab.value = tab;
}

function closeStackManifestModal() {
  overviewStore.closeStackManifestModal();
}

function handleEscape(event: KeyboardEvent) {
  if (event.key === "Escape" && overviewStore.stackManifestModalOpen) {
    closeStackManifestModal();
  }
}

watch(
  () => overviewStore.stackManifestModalOpen,
  (modalOpen) => {
    if (modalOpen) {
      activeTab.value = "desired";
    }
  },
);

onMounted(() => {
  document.addEventListener("keydown", handleEscape);
});

onUnmounted(() => {
  document.removeEventListener("keydown", handleEscape);
});
</script>

<template>
  <div class="modal" :class="{ hidden: !overviewStore.stackManifestModalOpen }" aria-hidden="true">
    <div class="modal-overlay" @click="closeStackManifestModal" />
    <div class="modal-card manifest-modal-card" role="dialog" aria-modal="true" aria-labelledby="stack-manifest-title">
      <div class="modal-header">
        <h2 id="stack-manifest-title">{{ modalTitle }}</h2>
        <button class="modal-close" type="button" aria-label="Close modal" @click="closeStackManifestModal">x</button>
      </div>

      <div class="modal-body">
        <p v-if="overviewStore.stackManifestLoading" class="meta">Loading manifest...</p>
        <p v-else-if="overviewStore.stackManifestError" class="meta">
          Failed to load manifest: {{ overviewStore.stackManifestError }}
        </p>
        <template v-else>
          <div class="manifest-tabs" role="tablist" aria-label="Stack manifest tabs">
            <button
              type="button"
              role="tab"
              class="manifest-tab-btn"
              :class="{ active: activeTab === 'desired' }"
              :aria-selected="activeTab === 'desired'"
              @click="setActiveTab('desired')"
            >
              Desired
            </button>
            <button
              type="button"
              role="tab"
              class="manifest-tab-btn"
              :class="{ active: activeTab === 'live' }"
              :aria-selected="activeTab === 'live'"
              @click="setActiveTab('live')"
            >
              Live
            </button>
          </div>

          <div class="manifest-viewer">
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
    </div>
  </div>
</template>
