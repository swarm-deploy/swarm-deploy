<script setup lang="ts">
import { t } from "../i18n";
import { computed } from "vue";
import { useRoute, useRouter } from "vue-router";

import AppButton from "../components/common/AppButton.vue";
import AppTabs from "../components/common/AppTabs.vue";
import { useNtfyPush } from "../composables/useNtfyPush";
import EventsView from "./EventsView.vue";
import AlertsView from "./AlertsView.vue";

type EventsAlertsTab = "alerts" | "events";

const push = useNtfyPush();
const route = useRoute();
const router = useRouter();
const tabs = computed(() => [
  { value: "alerts", label: t("alerts.tab"), id: "alerts-tab", panelId: "alerts-panel" },
  { value: "events", label: t("events.tab"), id: "events-tab", panelId: "events-panel" },
]);
const activeTab = computed<EventsAlertsTab>(() => route.query.tab === "events" ? "events" : "alerts");

function selectTab(tab: string): void {
  if (tab !== "alerts" && tab !== "events") return;
  if (activeTab.value === tab && route.query.tab === tab) {
    return;
  }

  const query = { ...route.query };
  query.tab = tab;
  if (tab === "events") delete query.alert_id;
  void router.replace({ query });
}
</script>

<template>
  <div class="events-alerts-page">
    <header class="events-alerts-header">
      <h2 class="mobile-title-duplicate">{{ t('navigation.eventsAlerts') }}</h2>
      <AppButton v-if="push.available" compact variant="secondary" :disabled="push.busy.value || push.denied.value" @click="push.toggle">
        {{ t(push.denied.value ? 'webPush.blocked' : push.enabled.value ? 'webPush.disable' : 'webPush.enable') }}
      </AppButton>
    </header>
    <p v-if="push.error.value" class="push-error" role="alert">{{ t('webPush.error') }}</p>
    <AppTabs
      class="events-alerts-tabs"
      :model-value="activeTab"
      :tabs="tabs"
      :label="t('events.tabs')"
      @update:model-value="selectTab"
    />

    <section
      v-show="activeTab === 'alerts'"
      id="alerts-panel"
      role="tabpanel"
      aria-labelledby="alerts-tab"
    >
      <AlertsView />
    </section>
    <section
      v-show="activeTab === 'events'"
      id="events-panel"
      role="tabpanel"
      aria-labelledby="events-tab"
    >
      <EventsView />
    </section>
  </div>
</template>

<style scoped>
.events-alerts-page {
  display: grid;
  gap: 16px;
  min-width: 0;
}

.events-alerts-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
}

.push-error {
  margin: 0;
  color: var(--text);
}

.events-alerts-header h2 {
  margin: 0;
}

</style>
