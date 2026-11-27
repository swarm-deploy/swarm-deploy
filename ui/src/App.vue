<script setup lang="ts">
import { onMounted } from "vue";
import { refreshPushSubscription } from "./composables/useNtfyPush";
import AppShell from "./components/layout/AppShell.vue";

onMounted(refreshPushSubscription);
</script>

<template>
  <AppShell />
</template>
