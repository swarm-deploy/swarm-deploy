import { apiRequest } from "./client";
import type { SecretDetailsResponse, SecretsResponse } from "./types";

export function fetchSecrets(): Promise<SecretsResponse> {
  return apiRequest<SecretsResponse>("/api/v1/secrets");
}

export function fetchSecretByName(name: string): Promise<SecretDetailsResponse> {
  const encodedName = encodeURIComponent(name);
  return apiRequest<SecretDetailsResponse>(`/api/v1/secrets/${encodedName}`);
}
