import { defineStore } from "pinia";

import {
  fetchEvents,
  fetchGitCommit,
  fetchServiceDeployments,
  fetchServiceStatus,
  fetchStacks,
  triggerSync,
} from "../api/overview";
import { fetchServices } from "../api/services";
import type {
  EventHistoryItem,
  GitCommitDetailsResponse,
  ServiceInfo,
  ServiceStatusResponse,
  StackView,
  SyncInfo,
} from "../api/types";

interface OverviewState {
  stacks: StackView[];
  services: ServiceInfo[];
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
  serviceStatusStack: string;
  serviceStatusService: string;
  serviceStatusLatestDeploymentAt: string;
  commitDetailsData: GitCommitDetailsResponse | null;
  commitDetailsLoading: boolean;
  commitDetailsError: string;
  commitDetailsModalOpen: boolean;
  commitDetailsHash: string;
}

export const useOverviewStore = defineStore("overview", {
  state: (): OverviewState => ({
    stacks: [],
    services: [],
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
    serviceStatusStack: "",
    serviceStatusService: "",
    serviceStatusLatestDeploymentAt: "",
    commitDetailsData: null,
    commitDetailsLoading: false,
    commitDetailsError: "",
    commitDetailsModalOpen: false,
    commitDetailsHash: "",
  }),
  actions: {
    async loadOverview() {
      this.loading = true;
      this.loadingError = "";

      try {
        const [stacksResult, servicesResult] = await Promise.allSettled([fetchStacks(), fetchServices()]);
        const errors: string[] = [];

        if (stacksResult.status === "fulfilled") {
          this.stacks = Array.isArray(stacksResult.value.stacks) ? stacksResult.value.stacks : [];
          this.syncInfo = stacksResult.value.sync ?? null;
        } else {
          this.stacks = [];
          this.syncInfo = null;
          errors.push(stacksResult.reason instanceof Error ? stacksResult.reason.message : "Failed to load stacks");
        }

        if (servicesResult.status === "fulfilled") {
          this.services = Array.isArray(servicesResult.value.services) ? servicesResult.value.services : [];
        } else {
          this.services = [];
          errors.push(servicesResult.reason instanceof Error ? servicesResult.reason.message : "Failed to load services");
        }

        if (errors.length > 0) {
          this.loadingError = errors.join("; ");
        }
      } catch (error) {
        this.stacks = [];
        this.services = [];
        this.syncInfo = null;
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
      this.serviceStatusData = null;
      this.serviceStatusStack = stackName;
      this.serviceStatusService = serviceName;
      this.serviceStatusLatestDeploymentAt = "";

      try {
        const [statusResult, deploymentsResult] = await Promise.allSettled([
          fetchServiceStatus(stackName, serviceName),
          fetchServiceDeployments(stackName, serviceName, 1),
        ]);

        if (statusResult.status === "fulfilled") {
          this.serviceStatusData = statusResult.value;
        } else {
          throw statusResult.reason;
        }

        if (deploymentsResult.status === "fulfilled") {
          this.serviceStatusLatestDeploymentAt = deploymentsResult.value.deployments.at(0)?.created_at ?? "";
        }
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
      this.serviceStatusStack = "";
      this.serviceStatusService = "";
      this.serviceStatusLatestDeploymentAt = "";
    },
    async openCommitDetailsModal(commitHash: string) {
      this.commitDetailsModalOpen = true;
      this.commitDetailsLoading = true;
      this.commitDetailsError = "";
      this.commitDetailsData = null;
      this.commitDetailsHash = commitHash;

      try {
        this.commitDetailsData = await fetchGitCommit(commitHash);
      } catch (error) {
        this.commitDetailsError = error instanceof Error ? error.message : "Failed to load commit details";
      } finally {
        this.commitDetailsLoading = false;
      }
    },
    closeCommitDetailsModal() {
      this.commitDetailsModalOpen = false;
      this.commitDetailsData = null;
      this.commitDetailsLoading = false;
      this.commitDetailsError = "";
      this.commitDetailsHash = "";
    },
  },
});
