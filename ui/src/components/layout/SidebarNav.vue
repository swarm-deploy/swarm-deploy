<script setup lang="ts">
import { locale, t } from "../../i18n";
import { computed, onMounted, onUnmounted, ref } from "vue";
import { RouterLink, useRoute } from "vue-router";

import { useUIStore, type ThemeMode } from "../../stores/ui";
import SidebarIcon from "./SidebarIcon.vue";

defineProps<{
  currentUserLabel: string;
  collapsed: boolean;
}>();

const emit = defineEmits<{
  toggle: [];
}>();

const route = useRoute();
const uiStore = useUIStore();
const userMenuRef = ref<HTMLDetailsElement | null>(null);

const themeOptions: Array<{ value: ThemeMode; label: string }> = [
  { value: "system", label: t("navigation.system") },
  { value: "light", label: t("navigation.light") },
  { value: "dark", label: t("navigation.dark") },
];

const appVersion = computed(() => formatVersion(__SWARM_DEPLOY_VERSION__));
const buildTimeTitle = computed(() => formatBuildTime(__SWARM_DEPLOY_BUILD_TIME__));

const links = [
  { to: "/overview", label: t("common.overview"), icon: "overview" },
  { to: "/realtime", label: t("common.realtime"), icon: "realtime" },
  { to: "/graph", label: t("navigation.graph"), icon: "graph" },
  { to: "/cluster", label: t("navigation.cluster"), icon: "cluster" },
  { to: "/networks", label: t("common.networks"), icon: "networks" },
  { to: "/secrets", label: t("common.secrets"), icon: "secrets" },
];

const secondaryLinks = [{ to: "/events", label: t("navigation.eventsAlerts"), icon: "events" }];

function isActive(path: string): boolean {
  return route.path === path || route.path.startsWith(`${path}/`);
}

function formatVersion(value: string): string {
  const version = value.trim() || "dev";

  return version.startsWith("v") ? version : `v${version}`;
}

function formatBuildTime(value: string): string {
  const buildTime = value.trim();
  if (!buildTime) {
    return t("navigation.buildTimeUnavailable");
  }

  const date = new Date(buildTime);
  if (Number.isNaN(date.getTime())) {
    return buildTime;
  }

  return date.toLocaleString(locale());
}

function selectTheme(theme: ThemeMode) {
  uiStore.setTheme(theme);
  if (userMenuRef.value) {
    userMenuRef.value.open = false;
  }
}

function handleDocumentClick(event: MouseEvent) {
  const userMenu = userMenuRef.value;
  if (!userMenu || !userMenu.open) {
    return;
  }
  if (event.target instanceof Node && userMenu.contains(event.target)) {
    return;
  }

  userMenu.open = false;
}

onMounted(() => {
  document.addEventListener("mousedown", handleDocumentClick);
});

onUnmounted(() => {
  document.removeEventListener("mousedown", handleDocumentClick);
});
</script>

<template>
  <aside class="sidebar" :class="{ collapsed }">
    <div class="sidebar-brand-block">
      <RouterLink to="/overview" class="sidebar-brand" :aria-label="t('navigation.siteOverview')">
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
          :aria-label="t('navigation.github')"
          :title="t('navigation.viewGithub')"
        >
          <SidebarIcon name="github" />
        </a>
      </div>
    </div>

    <div class="sidebar-navigation">
      <div class="sidebar-navigation-scroll">
        <nav class="sidebar-nav sidebar-nav-primary" :aria-label="t('navigation.primaryNavigation')">
          <RouterLink
            v-for="link in links"
            :key="link.to"
            :to="link.to"
            class="sidebar-link"
            :class="{ active: isActive(link.to) }"
            :aria-label="link.label"
            :data-tooltip="link.label"
          >
            <SidebarIcon :name="link.icon" />
            <span class="sidebar-label">{{ link.label }}</span>
          </RouterLink>
        </nav>

        <nav class="sidebar-nav sidebar-nav-secondary" :aria-label="t('navigation.globalNavigation')">
          <RouterLink
            v-for="link in secondaryLinks"
            :key="link.to"
            :to="link.to"
            class="sidebar-link"
            :class="{ active: isActive(link.to) }"
            :aria-label="link.label"
            :data-tooltip="link.label"
          >
            <SidebarIcon :name="link.icon" />
            <span class="sidebar-label">{{ link.label }}</span>
          </RouterLink>
          <details ref="userMenuRef" class="sidebar-user-menu">
            <summary class="sidebar-link sidebar-user" :data-tooltip="currentUserLabel" :aria-label="currentUserLabel">
              <SidebarIcon name="user" />
              <span class="sidebar-label">{{ currentUserLabel }}</span>
            </summary>
            <div class="sidebar-settings-menu" :aria-label="t('navigation.userSettings')">
              <div class="sidebar-settings-header">
                <strong>{{ currentUserLabel }}</strong>
                <span>{{ t('navigation.appearance') }}</span>
              </div>
              <div class="theme-selector" role="radiogroup" :aria-label="t('navigation.theme')">
                <button
                  v-for="option in themeOptions"
                  :key="option.value"
                  type="button"
                  class="theme-option"
                  :class="{ active: uiStore.themeMode === option.value }"
                  role="radio"
                  :aria-label="option.label"
                  :aria-checked="uiStore.themeMode === option.value"
                  @click="selectTheme(option.value)"
                >
                  <span>{{ option.label }}</span>
                  <span v-if="uiStore.themeMode === option.value" aria-hidden="true">✓</span>
                </button>
              </div>
            </div>
          </details>
        </nav>
      </div>
    </div>

    <div class="sidebar-footer">
      <button
        type="button"
        class="sidebar-link sidebar-toggle"
        :aria-label="collapsed ? t('navigation.expandSidebar') : t('navigation.collapseSidebar')"
        :aria-expanded="!collapsed"
        :data-tooltip="collapsed ? t('navigation.expandSidebar') : undefined"
        @click="emit('toggle')"
      >
        <SidebarIcon :name="collapsed ? 'panel-open' : 'panel-close'" />
        <span class="sidebar-label">{{ t('navigation.collapseSidebar') }}</span>
      </button>
    </div>
  </aside>
</template>
