import { defineStore } from "pinia";

import { fetchSecretByName } from "../api/secrets";
import type { SecretDetailsResponse } from "../api/types";

interface SecretDetailsState {
  modalOpen: boolean;
  loading: boolean;
  error: string;
  secret: SecretDetailsResponse | null;
  selectedName: string;
}

export const useSecretDetailsStore = defineStore("secretDetails", {
  state: (): SecretDetailsState => ({
    modalOpen: false,
    loading: false,
    error: "",
    secret: null,
    selectedName: "",
  }),
  actions: {
    async openSecretDetails(name: string) {
      this.modalOpen = true;
      this.loading = true;
      this.error = "";
      this.selectedName = name;

      try {
        this.secret = await fetchSecretByName(name);
      } catch (error) {
        this.error = error instanceof Error ? error.message : "Failed to load secret details";
        this.secret = null;
      } finally {
        this.loading = false;
      }
    },
    closeSecretDetails() {
      this.modalOpen = false;
      this.loading = false;
      this.error = "";
      this.secret = null;
      this.selectedName = "";
    },
  },
});
