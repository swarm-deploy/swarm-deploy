import { apiRequest } from "./client";
import type { NetworkInfo, NetworksResponse, NodeLabelCreateRequest, NodeLabelUpdateRequest, NodesResponse } from "./types";

export function fetchNodes(): Promise<NodesResponse> {
  return apiRequest<NodesResponse>("/api/v1/nodes");
}

export function addNodeLabel(nodeID: string, request: NodeLabelCreateRequest): Promise<void> {
  return apiRequest<void>(`/api/v1/nodes/${encodeURIComponent(nodeID)}/labels`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
  });
}

export function updateNodeLabel(nodeID: string, key: string, request: NodeLabelUpdateRequest): Promise<void> {
  return apiRequest<void>(`/api/v1/nodes/${encodeURIComponent(nodeID)}/labels/${encodeURIComponent(key)}`, {
    method: "PUT",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
  });
}

export function deleteNodeLabel(nodeID: string, key: string): Promise<void> {
  return apiRequest<void>(`/api/v1/nodes/${encodeURIComponent(nodeID)}/labels/${encodeURIComponent(key)}`, {
    method: "DELETE",
  });
}

export interface FetchNetworksOptions {
  stacks?: string[];
}

export function fetchNetworks(options: FetchNetworksOptions = {}): Promise<NetworksResponse> {
  const params = new URLSearchParams();
  for (const stack of options.stacks ?? []) {
    params.append("stack", stack);
  }

  const query = params.toString();
  return apiRequest<NetworksResponse>(`/api/v1/networks${query ? `?${query}` : ""}`);
}

export function fetchNetwork(networkID: string): Promise<NetworkInfo> {
  return apiRequest<NetworkInfo>(`/api/v1/networks/${encodeURIComponent(networkID)}`);
}
