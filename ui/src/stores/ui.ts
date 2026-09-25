import { defineStore } from "pinia";

const SIDEBAR_COLLAPSED_STORAGE_KEY = "swarm-deploy.sidebar-collapsed";
const ASSISTANT_PINNED_STORAGE_KEY = "swarm-deploy.assistant-pinned";

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
