<script setup lang="ts">
import { RouterLink, useRoute } from "vue-router";

defineProps<{
  assistantEnabled: boolean;
  assistantOpen: boolean;
}>();

const emit = defineEmits<{
  openEvents: [];
  toggleAssistant: [];
}>();

const route = useRoute();

const links = [
  { to: "/overview", label: "Overview" },
  { to: "/services", label: "Services" },
  { to: "/graph", label: "Graph" },
  { to: "/cluster", label: "Cluster" },
  { to: "/networks", label: "Networks" },
  { to: "/secrets", label: "Secrets" },
];

function isActive(path: string): boolean {
  if (path === "/services") {
    return route.path === "/services" || route.path.startsWith("/services/");
  }

  return route.path === path;
}
</script>

<template>
  <aside class="sidebar">
    <RouterLink to="/overview" class="sidebar-brand" aria-label="Swarm Deploy overview">
      <span class="sidebar-brand-mark">SD</span>
      <span>
        <span class="sidebar-brand-name">Swarm Deploy</span>
        <span class="sidebar-brand-meta">Docker Swarm CD</span>
      </span>
    </RouterLink>

    <nav class="sidebar-nav" aria-label="Primary navigation">
      <RouterLink
        v-for="link in links"
        :key="link.to"
        :to="link.to"
        class="sidebar-link"
        :class="{ active: isActive(link.to) }"
      >
        {{ link.label }}
      </RouterLink>
      <button type="button" class="sidebar-link sidebar-link-button" @click="emit('openEvents')">
        Events
      </button>
      <button
        type="button"
        class="sidebar-link sidebar-link-button"
        :class="{ active: assistantOpen }"
        :disabled="!assistantEnabled"
        @click="emit('toggleAssistant')"
      >
        Assistant
      </button>
    </nav>
  </aside>
</template>
