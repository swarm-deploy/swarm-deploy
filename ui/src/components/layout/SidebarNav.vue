<script setup lang="ts">
import { RouterLink, useRoute } from "vue-router";

const route = useRoute();

const links = [
  { to: "/overview", label: "Overview" },
  { to: "/services", label: "Services" },
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
