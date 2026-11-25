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
const dragOffset = ref(0);
const dragging = ref(false);
const moreButtonRef = ref<HTMLButtonElement | null>(null);
const moreSheetRef = ref<HTMLElement | null>(null);

const DRAG_CLOSE_DISTANCE = 80;
const DRAG_CLOSE_VELOCITY = 0.5;
const DRAG_FLICK_MIN_DISTANCE = 24;

let dragStartY = 0;
let dragStartedAt = 0;

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
const scrimOpacity = computed(() => Math.max(0.35, 1 - dragOffset.value / 320));

function isActive(path: string): boolean {
  return route.path === path;
}

function resetDrag(): void {
  dragging.value = false;
  dragOffset.value = 0;
}

function closeMore(returnFocus = true): void {
  if (!moreOpen.value) return;
  resetDrag();
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

function startDrag(event: PointerEvent): void {
  if (!event.isPrimary || event.button !== 0) return;
  event.preventDefault();
  dragStartY = event.clientY;
  dragStartedAt = Date.now();
  dragOffset.value = 0;
  dragging.value = true;
  (event.currentTarget as HTMLElement).setPointerCapture?.(event.pointerId);
}

function moveDrag(event: PointerEvent): void {
  if (!dragging.value || !event.isPrimary) return;
  dragOffset.value = Math.max(0, event.clientY - dragStartY);
}

function finishDrag(event: PointerEvent): void {
  if (!dragging.value || !event.isPrimary) return;

  const distance = Math.max(dragOffset.value, event.clientY - dragStartY, 0);
  const elapsed = Math.max(Date.now() - dragStartedAt, 1);
  const velocity = distance / elapsed;
  dragging.value = false;

  if (
    distance >= DRAG_CLOSE_DISTANCE ||
    (distance >= DRAG_FLICK_MIN_DISTANCE && velocity >= DRAG_CLOSE_VELOCITY)
  ) {
    closeMore();
    return;
  }

  dragOffset.value = 0;
}

function cancelDrag(): void {
  resetDrag();
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
        :style="{ opacity: scrimOpacity }"
        @click="closeMore()"
      />
      <section
        id="mobile-more-sheet"
        ref="moreSheetRef"
        class="mobile-more-sheet"
        :class="{ 'mobile-more-sheet--dragging': dragging }"
        :style="{ transform: `translate3d(0, ${dragOffset}px, 0)` }"
        role="dialog"
        aria-labelledby="mobile-more-title"
        tabindex="-1"
      >
        <div
          class="mobile-more-drag-zone"
          aria-hidden="true"
          @pointerdown="startDrag"
          @pointermove="moveDrag"
          @pointerup="finishDrag"
          @pointercancel="cancelDrag"
        >
          <span class="mobile-more-handle" />
        </div>
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
    >
      <RouterLink
        to="/overview"
        class="mobile-navigation-item"
        :class="{ active: isActive('/overview') && !moreOpen }"
        @click="closeMore(false)"
      >
        <SidebarIcon name="overview" />
        <span>{{ t('common.overview') }}</span>
      </RouterLink>
      <RouterLink
        to="/realtime"
        class="mobile-navigation-item"
        :class="{ active: isActive('/realtime') && !moreOpen }"
        @click="closeMore(false)"
      >
        <SidebarIcon name="realtime" />
        <span>{{ t('navigation.realtimeShort') }}</span>
      </RouterLink>
      <RouterLink
        to="/events"
        class="mobile-navigation-item"
        :class="{ active: isActive('/events') && !moreOpen }"
        @click="closeMore(false)"
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
