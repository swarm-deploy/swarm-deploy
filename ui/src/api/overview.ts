import { apiRequest } from "./client";
import type {
  EventHistoryResponse,
  GitCommitDetailsResponse,
  QueueResponse,
  RecommendationsResponse,
  ServiceDeploymentsResponse,
  ServiceRealtimeResponse,
  ServiceStatusResponse,
  StackManifestosResponse,
  StacksResponse,
} from "./types";

export interface FetchEventsOptions {
  severities?: string[];
  categories?: string[];
  types?: string[];
  since?: string;
  limit?: number;
}

export function fetchStacks(): Promise<StacksResponse> {
  return apiRequest<StacksResponse>("/api/v1/stacks");
}

export function fetchGitCommit(commitHash: string): Promise<GitCommitDetailsResponse> {
  const encodedCommitHash = encodeURIComponent(commitHash);
  return apiRequest<GitCommitDetailsResponse>(`/api/v1/git/commits/${encodedCommitHash}`);
}

export function triggerSync(): Promise<QueueResponse> {
  return apiRequest<QueueResponse>("/api/v1/sync", {
    method: "POST",
  });
}

export function fetchEvents(options: FetchEventsOptions = {}): Promise<EventHistoryResponse> {
  const params = new URLSearchParams();
  for (const severity of options.severities ?? []) {
    params.append("severities", severity);
  }
  for (const category of options.categories ?? []) {
    params.append("categories", category);
  }
  for (const type of options.types ?? []) {
    params.append("types", type);
  }
  if (options.since) {
    params.set("since", options.since);
  }
  if (typeof options.limit === "number") {
    params.set("limit", String(options.limit));
  }

  const query = params.toString();
  return apiRequest<EventHistoryResponse>(`/api/v1/events${query ? `?${query}` : ""}`);
}

export function fetchRecommendations(options: { stack?: string; limit?: number } = {}): Promise<RecommendationsResponse> {
  const params = new URLSearchParams();
  if (options.stack) {
    params.set("stack", options.stack);
  }
  if (typeof options.limit === "number") {
    params.set("limit", String(options.limit));
  }

  const query = params.toString();
  return apiRequest<RecommendationsResponse>(`/api/v1/recommendations${query ? `?${query}` : ""}`);
}

export function fetchServiceStatus(stackName: string, serviceName: string): Promise<ServiceStatusResponse> {
  const encodedStack = encodeURIComponent(stackName);
  const encodedService = encodeURIComponent(serviceName);
  return apiRequest<ServiceStatusResponse>(`/api/v1/stacks/${encodedStack}/services/${encodedService}`);
}

export function fetchServiceDeployments(
  stackName: string,
  serviceName: string,
  limit?: number,
): Promise<ServiceDeploymentsResponse> {
  const encodedStack = encodeURIComponent(stackName);
  const encodedService = encodeURIComponent(serviceName);
  const query = typeof limit === "number" ? `?limit=${encodeURIComponent(String(limit))}` : "";
  return apiRequest<ServiceDeploymentsResponse>(
    `/api/v1/stacks/${encodedStack}/services/${encodedService}/deployments${query}`,
  );
}

export function fetchServiceRealtime(stackName: string, serviceName: string): Promise<ServiceRealtimeResponse> {
  const encodedStack = encodeURIComponent(stackName);
  const encodedService = encodeURIComponent(serviceName);
  return apiRequest<ServiceRealtimeResponse>(`/api/v1/stacks/${encodedStack}/services/${encodedService}/realtime`);
}

export function openTaskLogsStream(taskID: string, options?: { follow?: boolean; tail?: number }): EventSource {
  const encodedTaskID = encodeURIComponent(taskID);
  const params = new URLSearchParams();
  params.set("follow", String(options?.follow ?? true));
  params.set("tail", String(options?.tail ?? 200));

  return new EventSource(`/api/tasks/${encodedTaskID}/logs?${params.toString()}`);
}

export function fetchStackManifestos(stackName: string): Promise<StackManifestosResponse> {
  const encodedStack = encodeURIComponent(stackName);
  return apiRequest<StackManifestosResponse>(`/api/v1/stacks/${encodedStack}/manifestos`);
}
