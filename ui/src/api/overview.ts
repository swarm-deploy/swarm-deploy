import { apiRequest } from "./client";
import type {
  EventHistoryResponse,
  GitCommitDetailsResponse,
  QueueResponse,
  ServiceDeploymentsResponse,
  ServiceRealtimeResponse,
  ServiceStatusResponse,
  StackManifestosResponse,
  StacksResponse,
} from "./types";

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

export function fetchEvents(): Promise<EventHistoryResponse> {
  return apiRequest<EventHistoryResponse>("/api/v1/events");
}

export function fetchServiceStatus(stackName: string, serviceName: string): Promise<ServiceStatusResponse> {
  const encodedStack = encodeURIComponent(stackName);
  const encodedService = encodeURIComponent(serviceName);
  return apiRequest<ServiceStatusResponse>(`/api/v1/stacks/${encodedStack}/services/${encodedService}/status`);
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

export function fetchStackManifestos(stackName: string): Promise<StackManifestosResponse> {
  const encodedStack = encodeURIComponent(stackName);
  return apiRequest<StackManifestosResponse>(`/api/v1/stacks/${encodedStack}/manifestos`);
}
