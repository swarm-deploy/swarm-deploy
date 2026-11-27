<script setup lang="ts">
import { t } from "../../i18n";
import { computed, onMounted, watch } from "vue";
import { RouterView, useRoute, useRouter } from "vue-router";

import { assistantQuery } from "../../router/assistant";
import { useAssistantStore } from "../../stores/assistant";
import { useCurrentUserStore } from "../../stores/currentUser";
import { useOverviewStore } from "../../stores/overview";
import { useSecretDetailsStore } from "../../stores/secretDetails";
import { useUIStore } from "../../stores/ui";
import AssistantDrawer from "../assistant/AssistantDrawer.vue";
import MobileNavigation from "./MobileNavigation.vue";
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
const secretDetailsStore = useSecretDetailsStore();
const assistantStore = useAssistantStore();
const currentUserStore = useCurrentUserStore();
const uiStore = useUIStore();

const currentUserLabel = computed(() => currentUserStore.displayName.trim() || t("common.user"));

const assistantPinnedActive = computed(
  () => assistantStore.enabled && uiStore.assistantDrawerOpen && uiStore.assistantPinned,
);

watch(() => route.path, (path, previousPath) => {
  if (!previousPath || path === previousPath) return;
  if (overviewStore.serviceStatusModalOpen) overviewStore.closeServiceStatusModal();
  if (overviewStore.alertDetailsModalOpen) overviewStore.closeAlertDetailsModal();
  if (overviewStore.deploymentDetailsModalOpen) overviewStore.closeDeploymentDetailsModal();
  if (overviewStore.commitDetailsModalOpen) overviewStore.closeCommitDetailsModal();
  if (overviewStore.stackManifestModalOpen) overviewStore.closeStackManifestModal();
  if (secretDetailsStore.modalOpen) secretDetailsStore.closeSecretDetails();
}, { flush: "sync" });

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

const linkedAlertID = computed(() => {
  const id = route.query.alert_id;
  return route.name === "events" && route.query.tab !== "events" && typeof id === "string" ? id.trim() : "";
});

watch(linkedAlertID, (id, previousID) => {
  if (id) {
    if (overviewStore.alertDetailsID !== id) void overviewStore.openAlertDetailsByID(id);
  } else if (previousID && overviewStore.alertDetailsID === previousID) {
    overviewStore.closeAlertDetailsModal();
  }
}, { immediate: true });

watch(() => overviewStore.alertDetailsID, (id, previousID) => {
  if (id || !previousID || previousID !== linkedAlertID.value) return;
  const query = { ...route.query };
  delete query.alert_id;
  void router.replace({ query, hash: route.hash });
});

async function handleAssistantToggle() {
  if (!assistantStore.enabled) {
    return;
  }

  const assistant = uiStore.assistantDrawerOpen ? undefined : assistantStore.conversationID || "new";
  await router.push({ query: assistantQuery(route.query, assistant), hash: route.hash });
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
          :assistant-enabled="assistantStore.enabled"
          @toggle-assistant="handleAssistantToggle"
        />
        <div class="shell-view">
          <RouterView />
        </div>
      </main>
    </div>
    <MobileNavigation
      :assistant-enabled="assistantStore.enabled"
      @toggle-assistant="handleAssistantToggle"
    />
    <ServiceStatusModal />
    <AlertDetailsModal :linked="Boolean(linkedAlertID)" />
    <DeploymentDetailsModal />
    <CommitDetailsModal />
    <StackManifestModal />
    <SecretDetailsModal />
    <AssistantDrawer />
  </div>
</template>
