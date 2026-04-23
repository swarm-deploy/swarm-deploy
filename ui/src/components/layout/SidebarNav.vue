<script setup lang="ts">
import { RouterLink, useRoute } from "vue-router";

const route = useRoute();

const links = [
  { to: "/overview", label: "Overview" },
  { to: "/applications", label: "Applications" },
  { to: "/deployments", label: "Deployments" },
  { to: "/cluster", label: "Cluster" },
  { to: "/events", label: "Events & Audit" },
  { to: "/secrets", label: "Secrets & Config" },
];

function isActive(path: string): boolean {
  return route.path === path;
}
</script>

<template>
  <aside class="sidebar">
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
    </nav>
  </aside>
</template>
