import { apiRequest } from "./client";
import type {
  SecretDetailsResponse,
  SecretManagerSyncResponse,
  SecretManagersResponse,
  SecretsResponse,
} from "./types";

export function fetchSecrets(): Promise<SecretsResponse> {
  return apiRequest<SecretsResponse>("/api/v1/secrets");
}

export function fetchSecretByName(name: string): Promise<SecretDetailsResponse> {
  const encodedName = encodeURIComponent(name);
  return apiRequest<SecretDetailsResponse>(`/api/v1/secrets/${encodedName}`);
}

export function fetchSecretManagers(): Promise<SecretManagersResponse> {
  return apiRequest<SecretManagersResponse>("/api/v1/secret-managers");
}

export function syncSecretManager(stack: string, service: string): Promise<SecretManagerSyncResponse> {
  const encodedStack = encodeURIComponent(stack);
  const encodedService = encodeURIComponent(service);
  return apiRequest<SecretManagerSyncResponse>(
    `/api/v1/secret-managers/${encodedStack}/${encodedService}/sync`,
    { method: "POST" },
  );
}
