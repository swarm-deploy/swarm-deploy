import { afterEach, describe, expect, it, vi } from "vitest";

import { apiRequest } from "./api/client";
import { openTaskLogsStream } from "./api/overview";
import { getWebBasePath, withWebBasePath } from "./runtimeConfig";
import { renderAssistantMarkdown } from "./utils/assistantMarkdown";

function setRuntimeBasePath(basePath: string): void {
  const element = document.createElement("script");
  element.id = "swarm-deploy-runtime-config";
  element.type = "application/json";
  element.textContent = JSON.stringify({ basePath });
  document.head.append(element);
}

afterEach(() => {
  document.getElementById("swarm-deploy-runtime-config")?.remove();
  vi.unstubAllGlobals();
});

describe("runtime web base path", () => {
  it("defaults to root", () => {
    expect(getWebBasePath()).toBe("/");
    expect(withWebBasePath("/api/v1/stacks")).toBe("/api/v1/stacks");
  });

  it("prefixes API requests for a nested deployment", async () => {
    setRuntimeBasePath("/infrastructure/swarm");
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: vi.fn().mockResolvedValue({ stacks: [] }),
    });
    vi.stubGlobal("fetch", fetchMock);

    await apiRequest("/api/v1/stacks");

    expect(fetchMock).toHaveBeenCalledWith("/infrastructure/swarm/api/v1/stacks", undefined);
  });

  it("prefixes SSE streams for a nested deployment", () => {
    setRuntimeBasePath("/infrastructure/swarm");
    const eventSourceMock = vi.fn();
    vi.stubGlobal("EventSource", eventSourceMock);

    openTaskLogsStream("task/1", { follow: false, tail: 50 });

    expect(eventSourceMock).toHaveBeenCalledWith(
      "/infrastructure/swarm/api/tasks/task%2F1/logs?follow=false&tail=50",
    );
  });

  it("configures Vue Router history for a nested deployment", async () => {
    setRuntimeBasePath("/infrastructure/swarm");

    const { router } = await import("./router");

    expect(router.resolve("/overview").href).toBe("/infrastructure/swarm/overview");
  });

  it("prefixes absolute and relative internal assistant links", () => {
    setRuntimeBasePath("/infrastructure/swarm");

    const html = renderAssistantMarkdown("[Absolute](/services) [Relative](services) [Dot](./overview)");

    expect(html).toContain(`${window.location.origin}/infrastructure/swarm/services`);
    expect(html).toContain(`${window.location.origin}/infrastructure/swarm/overview`);
    expect(html).not.toContain(`href="${window.location.origin}/services"`);
  });

  it("keeps external, mailto, and unsafe assistant links unchanged", () => {
    setRuntimeBasePath("/infrastructure/swarm");

    const html = renderAssistantMarkdown(
      "[External](https://example.org/docs) [Email](mailto:ops@example.org) [Unsafe](javascript:alert)",
    );

    expect(html).toContain('href="https://example.org/docs"');
    expect(html).toContain('href="mailto:ops@example.org"');
    expect(html).not.toContain('href="javascript:');
  });

  it("resolves relative assistant links at root for backward compatibility", () => {
    const html = renderAssistantMarkdown("[Services](services)");
    expect(html).toContain(`href="${window.location.origin}/services"`);
  });
});
