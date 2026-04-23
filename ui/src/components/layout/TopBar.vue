<script setup lang="ts">
import { RouterLink } from "vue-router";

defineProps<{
  syncDisabled: boolean;
  syncPending: boolean;
  assistantEnabled: boolean;
  assistantOpen: boolean;
  notificationsDisabled: boolean;
}>();

const emit = defineEmits<{
  syncNow: [];
  openNotifications: [];
  toggleAssistant: [];
}>();
</script>

<template>
  <header class="topbar-shell">
    <div class="topbar-brand">
      <p class="eyebrow">GitOps for Docker Swarm</p>
      <RouterLink to="/overview" class="brand-link">Swarm Deploy</RouterLink>
    </div>

    <div class="topbar-search">
      <input type="search" placeholder="Search/Command" aria-label="Search or command" />
    </div>

    <div class="topbar-actions">
      <button type="button" :disabled="syncDisabled || syncPending" @click="emit('syncNow')">
        {{ syncPending ? "Syncing..." : "Sync now" }}
      </button>
      <button type="button" :disabled="notificationsDisabled" @click="emit('openNotifications')">Events</button>
      <button type="button" :disabled="!assistantEnabled" @click="emit('toggleAssistant')">
        {{ assistantOpen ? "Assistant Open" : "Assistant" }}
      </button>
      <button type="button" class="button-ghost">User</button>
    </div>
  </header>
</template>
