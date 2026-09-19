<script setup lang="ts">
import { computed, onMounted } from "vue";
import { RouterView, useRoute } from "vue-router";

import { useAssistantStore } from "../../stores/assistant";
import { useCurrentUserStore } from "../../stores/currentUser";
import { useOverviewStore } from "../../stores/overview";
import { useUIStore } from "../../stores/ui";
import AssistantDrawer from "../assistant/AssistantDrawer.vue";
import SidebarNav from "./SidebarNav.vue";
import TopBar from "./TopBar.vue";
import ServiceStatusModal from "../overview/ServiceStatusModal.vue";
import AlertDetailsModal from "../overview/AlertDetailsModal.vue";
import CommitDetailsModal from "../overview/CommitDetailsModal.vue";
import StackManifestModal from "../overview/StackManifestModal.vue";
import SecretDetailsModal from "../secrets/SecretDetailsModal.vue";

const route = useRoute();
const overviewStore = useOverviewStore();
const assistantStore = useAssistantStore();
const currentUserStore = useCurrentUserStore();
const uiStore = useUIStore();

const isOverviewRoute = computed(() => route.path === "/overview");

const syncDisabled = computed(() => !isOverviewRoute.value);

async function handleSyncNow() {
  if (!isOverviewRoute.value) {
    return;
  }

  await overviewStore.triggerManualSync();
}

function handleAssistantToggle() {
  if (!assistantStore.enabled) {
    return;
  }

  uiStore.toggleAssistantDrawer();
}

onMounted(() => {
  void currentUserStore.loadCurrentUser();
});
</script>

<template>
  <div class="app-root">
    <div class="layout-shell">
      <SidebarNav :assistant-enabled="assistantStore.enabled" @toggle-assistant="handleAssistantToggle" />
      <main class="shell-main">
        <TopBar
          :sync-disabled="syncDisabled"
          :sync-pending="overviewStore.syncPending"
          @sync-now="handleSyncNow"
        />
        <div class="shell-view">
          <RouterView />
        </div>
      </main>
    </div>
    <ServiceStatusModal />
    <AlertDetailsModal />
    <CommitDetailsModal />
    <StackManifestModal />
    <SecretDetailsModal />
    <AssistantDrawer />
  </div>
</template>
