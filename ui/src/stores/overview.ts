import { defineStore } from "pinia";

import { fetchEvents, fetchServiceStatus, fetchStacks, triggerSync } from "../api/overview";
import type { EventHistoryItem, ServiceStatusResponse, StackView, SyncInfo } from "../api/types";

interface OverviewState {
  stacks: StackView[];
  syncInfo: SyncInfo | null;
  loading: boolean;
  loadingError: string;
  syncPending: boolean;
  events: EventHistoryItem[];
  eventsLoading: boolean;
  eventsError: string;
  eventsModalOpen: boolean;
  serviceStatusData: ServiceStatusResponse | null;
  serviceStatusLoading: boolean;
  serviceStatusError: string;
  serviceStatusModalOpen: boolean;
}

export const useOverviewStore = defineStore("overview", {
  state: (): OverviewState => ({
    stacks: [],
    syncInfo: null,
    loading: false,
    loadingError: "",
    syncPending: false,
    events: [],
    eventsLoading: false,
    eventsError: "",
    eventsModalOpen: false,
    serviceStatusData: null,
    serviceStatusLoading: false,
    serviceStatusError: "",
    serviceStatusModalOpen: false,
  }),
  actions: {
    async loadOverview() {
      this.loading = true;
      this.loadingError = "";

      try {
        const response = await fetchStacks();
        this.stacks = Array.isArray(response.stacks) ? response.stacks : [];
        this.syncInfo = response.sync ?? null;
      } catch (error) {
        this.loadingError = error instanceof Error ? error.message : "Failed to load state";
      } finally {
        this.loading = false;
      }
    },
    async triggerManualSync() {
      this.syncPending = true;
      try {
        await triggerSync();
        await this.loadOverview();
      } catch (error) {
        const message = error instanceof Error ? error.message : "Failed to trigger sync";
        this.loadingError = `Failed to trigger sync: ${message}`;
      } finally {
        this.syncPending = false;
      }
    },
    async openEventsModal() {
      this.eventsModalOpen = true;
      this.eventsLoading = true;
      this.eventsError = "";

      try {
        const response = await fetchEvents();
        this.events = Array.isArray(response.events) ? response.events : [];
      } catch (error) {
        this.eventsError = error instanceof Error ? error.message : "Failed to load event history";
      } finally {
        this.eventsLoading = false;
      }
    },
    closeEventsModal() {
      this.eventsModalOpen = false;
    },
    async openServiceStatusModal(stackName: string, serviceName: string) {
      this.serviceStatusModalOpen = true;
      this.serviceStatusLoading = true;
      this.serviceStatusError = "";

      try {
        this.serviceStatusData = await fetchServiceStatus(stackName, serviceName);
      } catch (error) {
        this.serviceStatusError = error instanceof Error ? error.message : "Failed to load service status";
      } finally {
        this.serviceStatusLoading = false;
      }
    },
    closeServiceStatusModal() {
      this.serviceStatusModalOpen = false;
      this.serviceStatusData = null;
      this.serviceStatusError = "";
      this.serviceStatusLoading = false;
    },
  },
});
