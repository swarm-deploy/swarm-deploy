import { defineStore } from "pinia";

import { fetchCurrentUser } from "../api/users";

interface CurrentUserState {
  displayName: string;
  loading: boolean;
  loaded: boolean;
}

export const useCurrentUserStore = defineStore("currentUser", {
  state: (): CurrentUserState => ({
    displayName: "",
    loading: false,
    loaded: false,
  }),
  actions: {
    async loadCurrentUser() {
      if (this.loading || this.loaded) {
        return;
      }

      this.loading = true;
      try {
        const response = await fetchCurrentUser();
        this.displayName = response.name.trim();
      } catch {
        this.displayName = "";
      } finally {
        this.loading = false;
        this.loaded = true;
      }
    },
  },
});
