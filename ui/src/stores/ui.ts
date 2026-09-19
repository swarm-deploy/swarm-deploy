import { defineStore } from "pinia";

const SIDEBAR_COLLAPSED_STORAGE_KEY = "swarm-deploy.sidebar-collapsed";

function loadSidebarCollapsed(): boolean {
  try {
    return localStorage.getItem(SIDEBAR_COLLAPSED_STORAGE_KEY) === "true";
  } catch {
    return false;
  }
}

export const useUIStore = defineStore("ui", {
  state: () => ({
    assistantDrawerOpen: false,
    sidebarCollapsed: loadSidebarCollapsed(),
  }),
  actions: {
    openAssistantDrawer() {
      this.assistantDrawerOpen = true;
    },
    closeAssistantDrawer() {
      this.assistantDrawerOpen = false;
    },
    toggleAssistantDrawer() {
      this.assistantDrawerOpen = !this.assistantDrawerOpen;
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
