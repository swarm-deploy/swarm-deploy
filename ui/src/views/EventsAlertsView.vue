<script setup lang="ts">
import { computed } from "vue";
import { useRoute, useRouter } from "vue-router";

import EventsView from "./EventsView.vue";
import AlertsView from "./AlertsView.vue";

type EventsAlertsTab = "alerts" | "events";

const route = useRoute();
const router = useRouter();
const activeTab = computed<EventsAlertsTab>(() => route.query.tab === "events" ? "events" : "alerts");

function selectTab(tab: EventsAlertsTab): void {
  if (activeTab.value === tab && route.query.tab === tab) {
    return;
  }

  void router.replace({ query: { ...route.query, tab } });
}
</script>

<template>
  <div class="events-alerts-page">
    <h1>Events &amp; Alerts</h1>
    <div class="events-alerts-tabs" role="tablist" aria-label="Events and alerts views">
      <button
        id="alerts-tab"
        type="button"
        role="tab"
        :aria-selected="activeTab === 'alerts'"
        aria-controls="alerts-panel"
        :class="{ active: activeTab === 'alerts' }"
        @click="selectTab('alerts')"
      >
        Alerts
      </button>
      <button
        id="events-tab"
        type="button"
        role="tab"
        :aria-selected="activeTab === 'events'"
        aria-controls="events-panel"
        :class="{ active: activeTab === 'events' }"
        @click="selectTab('events')"
      >
        Events
      </button>
    </div>

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

.events-alerts-tabs {
  display: flex;
  gap: 8px;
  border-bottom: 1px solid var(--line);
}

.events-alerts-tabs button {
  border: 0;
  border-bottom: 3px solid transparent;
  border-radius: 0;
  background: transparent;
  color: var(--muted);
  padding: 10px 10px 12px;
}

.events-alerts-tabs button:hover {
  color: var(--text);
  transform: none;
  filter: none;
}

.events-alerts-tabs button.active {
  border-bottom-color: var(--accent);
  color: var(--accent-strong);
}
</style>
