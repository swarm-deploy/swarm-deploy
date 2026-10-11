<script setup lang="ts">
import EventsView from "./EventsView.vue";
import AlertsView from "./AlertsView.vue";
</script>

<template>
  <h1>Events &amp; Alerts</h1>
  <div id="alerts"><AlertsView /></div>
  <EventsView />
</template>
