import { apiRequest } from "./client";
import type { NodesResponse } from "./types";

export function fetchNodes(): Promise<NodesResponse> {
  return apiRequest<NodesResponse>("/api/v1/nodes");
}
