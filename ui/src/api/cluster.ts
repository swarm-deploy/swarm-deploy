import { apiRequest } from "./client";
import type { NetworksResponse, NodesResponse } from "./types";

export function fetchNodes(): Promise<NodesResponse> {
  return apiRequest<NodesResponse>("/api/v1/nodes");
}

export function fetchNetworks(): Promise<NetworksResponse> {
  return apiRequest<NetworksResponse>("/api/v1/networks");
}
