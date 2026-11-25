<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { RouterLink, useRoute } from "vue-router";

import { t } from "../../i18n";
import { useUIStore, type ThemeMode } from "../../stores/ui";
import SidebarIcon from "./SidebarIcon.vue";

defineProps<{
  assistantEnabled: boolean;
}>();

const emit = defineEmits<{
  toggleAssistant: [];
}>();

const route = useRoute();
const uiStore = useUIStore();
const moreOpen = ref(false);
const appearanceOpen = ref(false);
const moreButtonRef = ref<HTMLButtonElement | null>(null);
const moreSheetRef = ref<HTMLElement | null>(null);

const themeOptions: Array<{ value: ThemeMode; label: string }> = [
  { value: "system", label: t("navigation.system") },
  { value: "light", label: t("navigation.light") },
  { value: "dark", label: t("navigation.dark") },
];

const moreLinks = [
  { to: "/graph", label: t("navigation.graph"), icon: "graph" },
  { to: "/cluster", label: t("navigation.cluster"), icon: "cluster" },
  { to: "/networks", label: t("common.networks"), icon: "networks" },
  { to: "/secrets", label: t("common.secrets"), icon: "secrets" },
];

const appVersion = computed(() => {
  const version = __SWARM_DEPLOY_VERSION__.trim() || "dev";
  return version.startsWith("v") ? version : `v${version}`;
});
const currentThemeLabel = computed(
  () => themeOptions.find((option) => option.value === uiStore.themeMode)?.label ?? t("navigation.system"),
);
const moreActive = computed(
  () => moreOpen.value || moreLinks.some((link) => route.path === link.to),
);

function isActive(path: string): boolean {
  return route.path === path;
}

function closeMore(returnFocus = true): void {
  if (!moreOpen.value) return;
  moreOpen.value = false;
  appearanceOpen.value = false;
  if (returnFocus) {
    void nextTick(() => moreButtonRef.value?.focus());
  }
}

async function toggleMore(): Promise<void> {
  if (moreOpen.value) {
    closeMore();
    return;
  }

  moreOpen.value = true;
  await nextTick();
  moreSheetRef.value?.focus();
}

function openAssistant(): void {
  closeMore(false);
  emit("toggleAssistant");
}

function selectTheme(theme: ThemeMode): void {
  uiStore.setTheme(theme);
  appearanceOpen.value = false;
}

function handleKeydown(event: KeyboardEvent): void {
  if (event.key === "Escape" && moreOpen.value) {
    event.preventDefault();
    closeMore();
  }
}

watch(
  () => route.fullPath,
  () => closeMore(false),
);

watch(moreOpen, (open) => {
  document.body.classList.toggle("mobile-more-open", open);
});

onMounted(() => {
  document.addEventListener("keydown", handleKeydown);
});

onUnmounted(() => {
  document.removeEventListener("keydown", handleKeydown);
  document.body.classList.remove("mobile-more-open");
});
</script>

<template>
  <div class="mobile-navigation-shell">
    <div v-if="moreOpen" class="mobile-more-layer">
      <button
        type="button"
        class="mobile-more-scrim"
        :aria-label="t('navigation.closeMore')"
        @click="closeMore()"
      />
      <section
        id="mobile-more-sheet"
        ref="moreSheetRef"
        class="mobile-more-sheet"
        role="dialog"
        aria-modal="true"
        aria-labelledby="mobile-more-title"
        tabindex="-1"
      >
        <div class="mobile-more-handle" aria-hidden="true" />
        <h2 id="mobile-more-title">{{ t('navigation.more') }}</h2>

        <nav class="mobile-more-links" :aria-label="t('navigation.moreNavigation')">
          <RouterLink
            v-for="link in moreLinks"
            :key="link.to"
            :to="link.to"
            class="mobile-more-link"
            @click="closeMore(false)"
          >
            <SidebarIcon :name="link.icon" />
            <span>{{ link.label }}</span>
            <SidebarIcon name="chevron" />
          </RouterLink>
        </nav>

        <div class="mobile-more-appearance">
          <button
            type="button"
            class="mobile-more-link"
            :aria-expanded="appearanceOpen"
            aria-controls="mobile-theme-options"
            @click="appearanceOpen = !appearanceOpen"
          >
            <SidebarIcon name="appearance" />
            <span>{{ t('navigation.appearance') }}</span>
            <span class="mobile-more-link-value">{{ currentThemeLabel }}</span>
            <SidebarIcon name="chevron" :class="{ 'mobile-more-chevron-open': appearanceOpen }" />
          </button>
          <div
            v-if="appearanceOpen"
            id="mobile-theme-options"
            class="mobile-theme-options"
            role="radiogroup"
            :aria-label="t('navigation.theme')"
          >
            <button
              v-for="option in themeOptions"
              :key="option.value"
              type="button"
              class="mobile-theme-option"
              :class="{ active: uiStore.themeMode === option.value }"
              role="radio"
              :aria-checked="uiStore.themeMode === option.value"
              @click="selectTheme(option.value)"
            >
              {{ option.label }}
            </button>
          </div>
        </div>

        <footer class="mobile-more-footer">
          <span>Swarm Deploy {{ appVersion }}</span>
          <a
            href="https://github.com/swarm-deploy/swarm-deploy"
            target="_blank"
            rel="noopener noreferrer"
          >GitHub</a>
        </footer>
      </section>
    </div>

    <nav
      class="mobile-navigation"
      :aria-label="t('navigation.mobileNavigation')"
      :inert="moreOpen || undefined"
    >
      <RouterLink
        to="/overview"
        class="mobile-navigation-item"
        :class="{ active: isActive('/overview') && !moreOpen }"
      >
        <SidebarIcon name="overview" />
        <span>{{ t('common.overview') }}</span>
      </RouterLink>
      <RouterLink
        to="/realtime"
        class="mobile-navigation-item"
        :class="{ active: isActive('/realtime') && !moreOpen }"
      >
        <SidebarIcon name="realtime" />
        <span>{{ t('navigation.realtimeShort') }}</span>
      </RouterLink>
      <RouterLink
        to="/events"
        class="mobile-navigation-item"
        :class="{ active: isActive('/events') && !moreOpen }"
      >
        <SidebarIcon name="alerts" />
        <span>{{ t('alerts.tab') }}</span>
      </RouterLink>
      <button
        type="button"
        class="mobile-navigation-item"
        :class="{ active: uiStore.assistantDrawerOpen && !moreOpen }"
        :disabled="!assistantEnabled"
        :aria-pressed="uiStore.assistantDrawerOpen"
        @click="openAssistant"
      >
        <SidebarIcon name="assistant" />
        <span>{{ t('assistant.name') }}</span>
      </button>
      <button
        ref="moreButtonRef"
        type="button"
        class="mobile-navigation-item"
        :class="{ active: moreActive }"
        aria-controls="mobile-more-sheet"
        :aria-expanded="moreOpen"
        @click="toggleMore"
      >
        <SidebarIcon name="more" />
        <span>{{ t('navigation.more') }}</span>
      </button>
    </nav>
  </div>
</template>
