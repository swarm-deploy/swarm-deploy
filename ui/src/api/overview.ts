import { apiRequest } from "./client";
import type {
  EventHistoryResponse,
  QueueResponse,
  ServiceDeploymentsResponse,
  ServiceStatusResponse,
  StacksResponse,
} from "./types";

export function fetchStacks(): Promise<StacksResponse> {
  return apiRequest<StacksResponse>("/api/v1/stacks");
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
