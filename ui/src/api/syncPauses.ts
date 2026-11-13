import { apiRequest } from "./client";
import type { ServiceSyncPauseResponse, StackSyncPausesResponse } from "./types";

export function getStackSyncPauses(stack: string): Promise<StackSyncPausesResponse> {
  return apiRequest(`/api/v1/stacks/${encodeURIComponent(stack)}/sync-pauses`);
}

export function setStackSyncPause(stack: string, expiresAt: string | null): Promise<StackSyncPausesResponse> {
  return apiRequest(`/api/v1/stacks/${encodeURIComponent(stack)}/sync-pause`, {
    method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ expires_at: expiresAt }),
  });
}

export function resumeStackSync(stack: string): Promise<void> {
  return apiRequest(`/api/v1/stacks/${encodeURIComponent(stack)}/sync-pause`, { method: "DELETE" });
}

export function getServiceSyncPause(stack: string, service: string): Promise<ServiceSyncPauseResponse> {
  return apiRequest(`/api/v1/stacks/${encodeURIComponent(stack)}/services/${encodeURIComponent(service)}/sync-pause`);
}

export function setServiceSyncPause(
  stack: string, service: string, expiresAt: string | null,
): Promise<ServiceSyncPauseResponse> {
  return apiRequest(`/api/v1/stacks/${encodeURIComponent(stack)}/services/${encodeURIComponent(service)}/sync-pause`, {
    method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ expires_at: expiresAt }),
  });
}

export function removeServiceSyncPause(stack: string, service: string): Promise<void> {
  return apiRequest(`/api/v1/stacks/${encodeURIComponent(stack)}/services/${encodeURIComponent(service)}/sync-pause`, {
    method: "DELETE",
  });
}

export const syncPauseDurations = [
  { value: "1", label: "1 hour", milliseconds: 60 * 60 * 1000 },
  { value: "2", label: "2 hours", milliseconds: 2 * 60 * 60 * 1000 },
  { value: "4", label: "4 hours", milliseconds: 4 * 60 * 60 * 1000 },
  { value: "24", label: "24 hours", milliseconds: 24 * 60 * 60 * 1000 },
  { value: "none", label: "No expiration", milliseconds: null },
] as const;

export function expirationForDuration(value: string, now = Date.now()): string | null {
  const duration = syncPauseDurations.find((item) => item.value === value);
  return duration?.milliseconds == null ? null : new Date(now + duration.milliseconds).toISOString();
}
