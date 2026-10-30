import { afterEach, expect, it, vi } from "vitest";

import { fetchNetworks } from "./cluster";

afterEach(() => vi.unstubAllGlobals());

it("encodes repeated network stack filters", async () => {
  const fetch = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({ networks: [] }) });
  vi.stubGlobal("fetch", fetch);

  await fetchNetworks({ stacks: ["app", "infra"] });

  expect(fetch).toHaveBeenLastCalledWith("/api/v1/networks?stack=app&stack=infra", undefined);
});
