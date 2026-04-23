import { defineStore } from "pinia";

export const useUIStore = defineStore("ui", {
  state: () => ({
    assistantDrawerOpen: false,
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
  },
});
