<script setup lang="ts">
import { computed } from "vue";
import { RouterLink, useRoute } from "vue-router";

import SidebarIcon from "./SidebarIcon.vue";

defineProps<{
  currentUserLabel: string;
  collapsed: boolean;
}>();

const emit = defineEmits<{
  toggle: [];
}>();

const route = useRoute();

const appVersion = computed(() => formatVersion(__SWARM_DEPLOY_VERSION__));
const buildTimeTitle = computed(() => formatBuildTime(__SWARM_DEPLOY_BUILD_TIME__));

const links = [
  { to: "/overview", label: "Overview", icon: "overview" },
  { to: "/services", label: "Services", icon: "services" },
  { to: "/graph", label: "Graph", icon: "graph" },
  { to: "/cluster", label: "Cluster", icon: "cluster" },
  { to: "/networks", label: "Networks", icon: "networks" },
  { to: "/secrets", label: "Secrets", icon: "secrets" },
  { to: "/recommendations", label: "Recommendations", icon: "recommendations" },
];

const secondaryLinks = [{ to: "/events", label: "Events", icon: "events" }];

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
  <aside class="sidebar" :class="{ collapsed }">
    <div class="sidebar-brand-block">
      <RouterLink to="/overview" class="sidebar-brand" aria-label="swarm-deploy overview">
        <span class="sidebar-brand-mark" aria-hidden="true">SD</span>
        <span class="sidebar-label sidebar-brand-name">Swarm Deploy</span>
      </RouterLink>
      <div class="sidebar-brand-meta sidebar-label">
        <span :title="buildTimeTitle">{{ appVersion }}</span>
        <a
          class="sidebar-github-link"
          href="https://github.com/swarm-deploy/swarm-deploy"
          target="_blank"
          rel="noopener noreferrer"
          aria-label="swarm-deploy on GitHub"
          title="View swarm-deploy on GitHub"
        >
          <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
            <path d="M12 2a10 10 0 0 0-3.16 19.49c.5.09.68-.22.68-.48v-1.88c-2.78.6-3.37-1.18-3.37-1.18-.45-1.16-1.11-1.47-1.11-1.47-.91-.62.07-.61.07-.61 1 .07 1.53 1.03 1.53 1.03.9 1.53 2.35 1.09 2.92.83.09-.65.35-1.09.64-1.34-2.22-.25-4.55-1.11-4.55-4.94 0-1.09.39-1.98 1.03-2.68-.1-.25-.45-1.27.1-2.64 0 0 .84-.27 2.75 1.02A9.58 9.58 0 0 1 12 6.52c.85 0 1.69.11 2.48.33 1.91-1.29 2.75-1.02 2.75-1.02.55 1.37.2 2.39.1 2.64.64.7 1.03 1.59 1.03 2.68 0 3.84-2.34 4.68-4.57 4.93.36.31.68.92.68 1.85v3.08c0 .27.18.58.69.48A10 10 0 0 0 12 2Z" />
          </svg>
        </a>
      </div>
    </div>

    <div class="sidebar-navigation">
      <nav class="sidebar-nav sidebar-nav-primary" aria-label="Primary navigation">
        <RouterLink
          v-for="link in links"
          :key="link.to"
          :to="link.to"
          class="sidebar-link"
          :class="{ active: isActive(link.to) }"
          :aria-label="collapsed ? link.label : undefined"
          :data-tooltip="link.label"
        >
          <SidebarIcon :name="link.icon" />
          <span class="sidebar-label">{{ link.label }}</span>
        </RouterLink>
      </nav>

      <nav class="sidebar-nav sidebar-nav-secondary" aria-label="Global navigation">
        <RouterLink
          v-for="link in secondaryLinks"
          :key="link.to"
          :to="link.to"
          class="sidebar-link"
          :class="{ active: isActive(link.to) }"
          :aria-label="collapsed ? link.label : undefined"
          :data-tooltip="link.label"
        >
          <SidebarIcon :name="link.icon" />
          <span class="sidebar-label">{{ link.label }}</span>
        </RouterLink>
        <div class="sidebar-link sidebar-user" :data-tooltip="currentUserLabel">
          <SidebarIcon name="user" />
          <span class="sidebar-label">{{ currentUserLabel }}</span>
        </div>
      </nav>
    </div>

    <div class="sidebar-footer">
      <button
        type="button"
        class="sidebar-link sidebar-toggle"
        :aria-label="collapsed ? 'Expand sidebar' : 'Collapse sidebar'"
        :aria-expanded="!collapsed"
        :data-tooltip="collapsed ? 'Expand sidebar' : undefined"
        @click="emit('toggle')"
      >
        <SidebarIcon :name="collapsed ? 'panel-open' : 'panel-close'" />
        <span class="sidebar-label">Collapse sidebar</span>
      </button>
    </div>
  </aside>
</template>
