import { describe, expect, it } from "vitest";

import type { SwarmService } from "../api/types";
import { decodeBase64Utf8, serviceRuntimeState } from "./realtime";

function service(running: number, desired: number): SwarmService {
  return {
    id: "service-id",
    name: "api",
    full_name: "infra_api",
    stack: "infra",
    image: "example/api:latest",
    mode: "replicated",
    replicas: { running, desired },
    updated_at: "2026-10-28T20:00:00Z",
  };
}

describe("serviceRuntimeState", () => {
  it.each([
    [2, 2, "running"],
    [1, 2, "degraded"],
    [0, 0, "scaled-down"],
    [2, 1, "unknown"],
  ] as const)("classifies %s/%s as %s", (running, desired, expected) => {
    expect(serviceRuntimeState(service(running, desired))).toBe(expected);
  });
});

describe("decodeBase64Utf8", () => {
  it("decodes UTF-8 and values containing equals signs", () => {
    expect(decodeBase64Utf8("0L/RgNC40LLQtdGCINC80LjRgD0x")).toBe("привет мир=1");
  });

  it("returns null for invalid UTF-8", () => {
    expect(decodeBase64Utf8("/w==")).toBeNull();
  });
});
