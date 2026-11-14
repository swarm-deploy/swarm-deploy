import { defineStore } from "pinia";

import { fetchLogLinkFields } from "../api/config";

export const useConfigStore = defineStore("config", {
  state: () => ({
    logLinkFields: {} as Record<string, string>,
  }),
  actions: {
    async loadLogLinkFields() {
      try {
        this.logLinkFields = await fetchLogLinkFields();
      } catch {
        // The log viewer can display fields as plain text without configuration.
      }
    },
  },
});
