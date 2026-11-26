import { defineStore } from "pinia";

const SIDEBAR_COLLAPSED_STORAGE_KEY = "swarm-deploy.sidebar-collapsed";
const ASSISTANT_PINNED_STORAGE_KEY = "swarm-deploy.assistant-pinned";
const THEME_STORAGE_KEY = "swarm-deploy.theme";

export type ThemeMode = "system" | "light" | "dark";

const colorSchemeQuery = window.matchMedia("(prefers-color-scheme: dark)");

function loadThemeMode(): ThemeMode {
  try {
    const storedTheme = localStorage.getItem(THEME_STORAGE_KEY);
    if (storedTheme === "light" || storedTheme === "dark" || storedTheme === "system") {
      return storedTheme;
    }
  } catch {
    // Fall back to the system preference when storage is unavailable.
  }

  return "system";
}

function applyTheme(mode: ThemeMode) {
  const resolvedTheme = mode === "system" ? (colorSchemeQuery.matches ? "dark" : "light") : mode;
  document.documentElement.dataset.theme = resolvedTheme;
  document.documentElement.dataset.themeMode = mode;
  document.documentElement.style.colorScheme = resolvedTheme;
}

const initialThemeMode = loadThemeMode();
applyTheme(initialThemeMode);

function loadSidebarCollapsed(): boolean {
  try {
    return localStorage.getItem(SIDEBAR_COLLAPSED_STORAGE_KEY) === "true";
  } catch {
    return false;
  }
}

function loadAssistantPinned(): boolean {
  try {
    return localStorage.getItem(ASSISTANT_PINNED_STORAGE_KEY) === "true";
  } catch {
    return false;
  }
}

export const useUIStore = defineStore("ui", {
  state: () => ({
    assistantDrawerOpen: false,
    assistantPinned: loadAssistantPinned(),
    sidebarCollapsed: loadSidebarCollapsed(),
    themeMode: initialThemeMode as ThemeMode,
  }),
  actions: {
    initializeTheme() {
      applyTheme(this.themeMode);
      colorSchemeQuery.addEventListener("change", this.handleSystemThemeChange);
    },
    handleSystemThemeChange() {
      if (this.themeMode === "system") {
        applyTheme(this.themeMode);
      }
    },
    setTheme(mode: ThemeMode) {
      this.themeMode = mode;
      applyTheme(mode);

      try {
        localStorage.setItem(THEME_STORAGE_KEY, mode);
      } catch {
        // Keep the in-memory preference when storage is unavailable.
      }
    },
    openAssistantDrawer() {
      this.assistantDrawerOpen = true;
    },
    closeAssistantDrawer() {
      this.assistantDrawerOpen = false;
    },
    toggleAssistantDrawer() {
      this.assistantDrawerOpen = !this.assistantDrawerOpen;
    },
    toggleAssistantPinned() {
      this.assistantPinned = !this.assistantPinned;

      try {
        localStorage.setItem(ASSISTANT_PINNED_STORAGE_KEY, String(this.assistantPinned));
      } catch {
        // Keep the in-memory preference when storage is unavailable.
      }
    },
    toggleSidebar() {
      this.sidebarCollapsed = !this.sidebarCollapsed;

      try {
        localStorage.setItem(SIDEBAR_COLLAPSED_STORAGE_KEY, String(this.sidebarCollapsed));
      } catch {
        // Keep the in-memory preference when storage is unavailable.
      }
    },
  },
});
