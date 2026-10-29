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
