import { apiRequest } from "./client";
import type {
  ServiceRealtimeResponse,
  ServicesResponse,
  SwarmServicesResponse,
  SwarmTaskDetails,
} from "./types";

export function fetchServices(): Promise<ServicesResponse> {
  return apiRequest<ServicesResponse>("/api/v1/services");
}

export function fetchSwarmServices(): Promise<SwarmServicesResponse> {
  return apiRequest<SwarmServicesResponse>("/api/v1/swarm/services");
}

export function fetchSwarmServiceTasks(serviceIDOrName: string): Promise<ServiceRealtimeResponse> {
  return apiRequest<ServiceRealtimeResponse>(
    `/api/v1/swarm/services/${encodeURIComponent(serviceIDOrName)}/tasks`,
  );
}

export function fetchSwarmTask(taskID: string): Promise<SwarmTaskDetails> {
  return apiRequest<SwarmTaskDetails>(`/api/v1/swarm/tasks/${encodeURIComponent(taskID)}`);
}

export interface GitOpsScaleTarget {
  stack: string;
  service: string;
}

export function scaleSwarmService(
  serviceID: string,
  replicas: number,
  gitopsTarget?: GitOpsScaleTarget,
): Promise<void> {
  return apiRequest<void>(`/api/v1/swarm/services/${encodeURIComponent(serviceID)}/replicas`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      replicas,
      ...(gitopsTarget ? {
        gitops_stack: gitopsTarget.stack,
        gitops_service: gitopsTarget.service,
      } : {}),
    }),
  });
}
