<script setup lang="ts">
import { computed } from "vue";
import { useRoute, useRouter } from "vue-router";

import AppTabs from "../components/common/AppTabs.vue";
import EventsView from "./EventsView.vue";
import AlertsView from "./AlertsView.vue";

type EventsAlertsTab = "alerts" | "events";

const route = useRoute();
const router = useRouter();
const tabs = [
  { value: "alerts", label: "Alerts", id: "alerts-tab", panelId: "alerts-panel" },
  { value: "events", label: "Events", id: "events-tab", panelId: "events-panel" },
] as const;
const activeTab = computed<EventsAlertsTab>(() => route.query.tab === "events" ? "events" : "alerts");

function selectTab(tab: string): void {
  if (tab !== "alerts" && tab !== "events") return;
  if (activeTab.value === tab && route.query.tab === tab) {
    return;
  }

  void router.replace({ query: { ...route.query, tab } });
}
</script>

<template>
  <div class="events-alerts-page">
    <h1>Events &amp; Alerts</h1>
    <AppTabs
      class="events-alerts-tabs"
      :model-value="activeTab"
      :tabs="tabs"
      label="Events and alerts views"
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
}

.events-alerts-page h1 {
  margin-bottom: 0;
}

</style>
