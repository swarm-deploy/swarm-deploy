import { apiRequest } from "./client";
import type { ServicesResponse } from "./types";

export function fetchServices(): Promise<ServicesResponse> {
  return apiRequest<ServicesResponse>("/api/v1/services");
}
