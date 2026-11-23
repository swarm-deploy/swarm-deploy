import type { SwarmService } from "../api/types";

export type RuntimeState = "running" | "degraded" | "scaled-down" | "unknown";

export function serviceRuntimeState(service: SwarmService): RuntimeState {
  const running = Number(service.replicas?.running);
  const desired = Number(service.replicas?.desired);

  if (!Number.isFinite(running) || !Number.isFinite(desired) || running < 0 || desired < 0) {
    return "unknown";
  }
  if (running === 0 && desired === 0) {
    return "scaled-down";
  }
  if (desired > 0 && running === desired) {
    return "running";
  }
  if (desired > 0 && running < desired) {
    return "degraded";
  }
  return "unknown";
}

export function decodeBase64Utf8(value: string): string | null {
  try {
    const bytes = Uint8Array.from(atob(value), (character) => character.charCodeAt(0));
    return new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch {
    return null;
  }
}
