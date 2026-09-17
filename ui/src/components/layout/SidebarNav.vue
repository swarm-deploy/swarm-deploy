<script setup lang="ts">
import { computed } from "vue";
import { RouterLink, useRoute } from "vue-router";

const route = useRoute();

const appVersion = computed(() => formatVersion(__SWARM_DEPLOY_VERSION__));
const buildTimeTitle = computed(() => formatBuildTime(__SWARM_DEPLOY_BUILD_TIME__));

const links = [
  { to: "/overview", label: "Overview" },
  { to: "/services", label: "Services" },
  { to: "/graph", label: "Graph" },
  { to: "/events", label: "Events" },
  { to: "/cluster", label: "Cluster" },
  { to: "/networks", label: "Networks" },
  { to: "/secrets", label: "Secrets" },
  { to: "/recommendations", label: "Recommendations" },
];

function isActive(path: string): boolean {
  if (path === "/services") {
    return route.path === "/services" || route.path.startsWith("/services/");
  }

  return route.path === path;
}

function formatVersion(value: string): string {
  const version = value.trim() || "dev";

  return version.startsWith("v") ? version : `v${version}`;
}

function formatBuildTime(value: string): string {
  const buildTime = value.trim();
  if (!buildTime) {
    return "Build time unavailable";
  }

  const date = new Date(buildTime);
  if (Number.isNaN(date.getTime())) {
    return buildTime;
  }

  return date.toLocaleString();
}
</script>

<template>
  <aside class="sidebar">
    <RouterLink to="/overview" class="sidebar-brand" aria-label="Swarm Deploy overview">
      <span class="sidebar-brand-mark">SD</span>
      <span>
        <span class="sidebar-brand-name">Swarm Deploy</span>
        <span class="sidebar-brand-meta" :title="buildTimeTitle">{{ appVersion }}</span>
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
    </nav>
  </aside>
</template>
