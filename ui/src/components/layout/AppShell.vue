<script setup lang="ts">
import { computed, onMounted, watch } from "vue";
import { RouterView, useRoute, useRouter } from "vue-router";

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
import DeploymentDetailsModal from "../overview/DeploymentDetailsModal.vue";
import StackManifestModal from "../overview/StackManifestModal.vue";
import SecretDetailsModal from "../secrets/SecretDetailsModal.vue";

const route = useRoute();
const router = useRouter();
const overviewStore = useOverviewStore();
const assistantStore = useAssistantStore();
const currentUserStore = useCurrentUserStore();
const uiStore = useUIStore();

const isOverviewRoute = computed(() => route.path === "/overview");
const currentUserLabel = computed(() => currentUserStore.displayName.trim() || "User");

const syncDisabled = computed(() => !isOverviewRoute.value);
const assistantPinnedActive = computed(
  () => assistantStore.enabled && uiStore.assistantDrawerOpen && uiStore.assistantPinned,
);
const linkedDeploymentID = computed(() => {
  const value = route.query.deployment_id;
  if (typeof value === "string") return value.trim();
  if (Array.isArray(value)) return value.find((item) => typeof item === "string" && item.trim())?.trim() ?? "";
  return "";
});

watch(linkedDeploymentID, (deploymentID, previousDeploymentID) => {
  if (deploymentID) {
    if (!overviewStore.deploymentDetailsModalOpen || overviewStore.deploymentDetailsID !== deploymentID) {
      void overviewStore.openDeploymentDetailsModal(deploymentID);
    }
    return;
  }
  if (previousDeploymentID && overviewStore.deploymentDetailsModalOpen &&
      overviewStore.deploymentDetailsID === previousDeploymentID) {
    overviewStore.closeDeploymentDetailsModal();
  }
}, { immediate: true });

watch(() => overviewStore.deploymentDetailsModalOpen, (open, wasOpen) => {
  if (open || !wasOpen || !linkedDeploymentID.value) return;
  const query = { ...route.query };
  delete query.deployment_id;
  void router.replace({ query });
});

async function handleSyncNow() {
  if (!isOverviewRoute.value) {
    return;
  }

  await overviewStore.triggerManualSync();
}

async function handleAssistantToggle() {
  if (!assistantStore.enabled) {
    return;
  }

  const query = { ...route.query };
  if (uiStore.assistantDrawerOpen) {
    delete query.assistant;
    await router.push({ query });
    return;
  }

  query.assistant = "chats";
  await router.push({ query });

  if (!assistantStore.historyOpen && (assistantStore.conversationID || assistantStore.messages.length > 0)) {
    query.assistant = assistantStore.conversationID || "new";
    await router.push({ query });
  }
}

onMounted(() => {
  uiStore.initializeTheme();
  void currentUserStore.loadCurrentUser();
});
</script>

<template>
  <div class="app-root" :class="{ 'assistant-pinned': assistantPinnedActive }">
    <div class="layout-shell" :class="{ 'sidebar-collapsed': uiStore.sidebarCollapsed }">
      <SidebarNav
        :current-user-label="currentUserLabel"
        :collapsed="uiStore.sidebarCollapsed"
        @toggle="uiStore.toggleSidebar"
      />
      <main class="shell-main">
        <TopBar
          :sync-disabled="syncDisabled"
          :sync-pending="overviewStore.syncPending"
          :assistant-enabled="assistantStore.enabled"
          @sync-now="handleSyncNow"
          @toggle-assistant="handleAssistantToggle"
        />
        <div class="shell-view">
          <RouterView />
        </div>
      </main>
    </div>
    <ServiceStatusModal />
    <AlertDetailsModal />
    <DeploymentDetailsModal />
    <CommitDetailsModal />
    <StackManifestModal />
    <SecretDetailsModal />
    <AssistantDrawer />
  </div>
</template>
