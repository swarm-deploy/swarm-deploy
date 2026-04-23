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
import EventHistoryModal from "../overview/EventHistoryModal.vue";
import ServiceStatusModal from "../overview/ServiceStatusModal.vue";
import SecretDetailsModal from "../secrets/SecretDetailsModal.vue";

const route = useRoute();
const overviewStore = useOverviewStore();
const assistantStore = useAssistantStore();
const currentUserStore = useCurrentUserStore();
const uiStore = useUIStore();

const isOverviewRoute = computed(() => route.path === "/overview");

const syncDisabled = computed(() => !isOverviewRoute.value);
const notificationsDisabled = computed(() => false);

async function handleSyncNow() {
  if (!isOverviewRoute.value) {
    return;
  }

  await overviewStore.triggerManualSync();
}

async function handleNotifications() {
  await overviewStore.openEventsModal();
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
    <div class="bg-shape shape-1" />
    <div class="bg-shape shape-2" />
    <div class="layout-shell">
      <TopBar
        :sync-disabled="syncDisabled"
        :sync-pending="overviewStore.syncPending"
        :assistant-enabled="assistantStore.enabled"
        :assistant-open="uiStore.assistantDrawerOpen"
        :notifications-disabled="notificationsDisabled"
        @sync-now="handleSyncNow"
        @open-notifications="handleNotifications"
        @toggle-assistant="handleAssistantToggle"
      />

      <div class="shell-content">
        <SidebarNav />
        <main class="shell-main">
          <RouterView />
        </main>
      </div>
    </div>
    <EventHistoryModal />
    <ServiceStatusModal />
    <SecretDetailsModal />
    <AssistantDrawer />
  </div>
</template>
