<script setup lang="ts">
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
const desiredManifestLines = computed(() => {
  const payload = String(desiredManifest.value || "");
  return payload.split(/\r?\n/);
});
const liveManifest = computed(() => overviewStore.stackManifestLive);
const liveManifestLines = computed(() => {
  const payload = String(liveManifest.value || "");
  return payload.split(/\r?\n/);
});

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

          <div v-if="activeTab === 'desired'" class="manifest-viewer">
            <p v-if="desiredManifest.trim().length === 0" class="meta">Desired manifest is empty.</p>
            <ol v-else class="manifest-lines" aria-label="Desired manifest yaml">
              <li
                v-for="(line, lineIndex) in desiredManifestLines"
                :key="`${lineIndex}-${line}`"
                class="manifest-line"
              >
                <code>{{ line || " " }}</code>
              </li>
            </ol>
          </div>

          <div v-else class="manifest-viewer">
            <p v-if="liveManifest.trim().length === 0" class="meta">Live manifest is empty.</p>
            <ol v-else class="manifest-lines" aria-label="Live manifest yaml">
              <li
                v-for="(line, lineIndex) in liveManifestLines"
                :key="`${lineIndex}-${line}`"
                class="manifest-line"
              >
                <code>{{ line || " " }}</code>
              </li>
            </ol>
          </div>
        </template>
      </div>
    </div>
  </div>
</template>
