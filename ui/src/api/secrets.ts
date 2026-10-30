import { apiRequest } from "./client";
import type {
  CreateExternalSecretRequest,
  CreateExternalSecretResponse,
  CreateSecretRequest,
  CreateSecretResponse,
  SecretDetailsResponse,
  SecretManagerSyncResponse,
  SecretManagersResponse,
  SecretsResponse,
} from "./types";

export function fetchSecrets(): Promise<SecretsResponse> {
  return apiRequest<SecretsResponse>("/api/v1/secrets");
}

export function createSecret(request: CreateSecretRequest): Promise<CreateSecretResponse> {
  return apiRequest<CreateSecretResponse>("/api/v1/secrets", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(request),
  });
}

export function fetchSecretByName(name: string): Promise<SecretDetailsResponse> {
  const encodedName = encodeURIComponent(name);
  return apiRequest<SecretDetailsResponse>(`/api/v1/secrets/${encodedName}`);
}

export function fetchSecretManagers(): Promise<SecretManagersResponse> {
  return apiRequest<SecretManagersResponse>("/api/v1/secret-managers");
}

export function syncSecretManager(
  stack: string,
  service: string,
  created?: { path: string; key?: string },
): Promise<SecretManagerSyncResponse> {
  const encodedStack = encodeURIComponent(stack);
  const encodedService = encodeURIComponent(service);
  const query = new URLSearchParams();
  if (created?.path) query.set("created_path", created.path);
  if (created?.key) query.set("created_key", created.key);
  const suffix = query.size > 0 ? `?${query}` : "";
  return apiRequest<SecretManagerSyncResponse>(
    `/api/v1/secret-managers/${encodedStack}/${encodedService}/sync${suffix}`,
    { method: "POST" },
  );
}

export function createExternalSecret(
  stack: string,
  service: string,
  request: CreateExternalSecretRequest,
): Promise<CreateExternalSecretResponse> {
  const encodedStack = encodeURIComponent(stack);
  const encodedService = encodeURIComponent(service);
  return apiRequest<CreateExternalSecretResponse>(
    `/api/v1/secret-managers/${encodedStack}/${encodedService}/secrets`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(request),
    },
  );
}
