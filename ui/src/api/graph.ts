import { apiRequest } from "./client";
import type { GraphResponse } from "./types";

export function fetchGraph(): Promise<GraphResponse> {
  return apiRequest<GraphResponse>("/api/v1/graph");
}
