import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fetchDeployment } from "../api/overview";
import { useOverviewStore } from "./overview";

vi.mock("../api/overview", () => ({ fetchDeployment: vi.fn() }));

describe("deployment details loading", () => {
  beforeEach(() => { setActivePinia(createPinia()); vi.clearAllMocks(); });
  it("loads details only on open and ignores stale responses after switching or closing", async () => {
    const store = useOverviewStore();
    expect(fetchDeployment).not.toHaveBeenCalled();
    let resolveFirst!: (value: unknown) => void;
    vi.mocked(fetchDeployment).mockImplementationOnce(() => new Promise(resolve => { resolveFirst = resolve; }) as ReturnType<typeof fetchDeployment>);
    vi.mocked(fetchDeployment).mockResolvedValueOnce({ id: "second", changes: [] } as never);
    const first = store.openDeploymentDetailsModal("first");
    expect(store.deploymentDetailsLoading).toBe(true);
    const second = store.openDeploymentDetailsModal("second");
    await second;
    resolveFirst({ id: "first", changes: [] });
    await first;
    expect(store.deploymentDetailsData?.id).toBe("second");
    store.closeDeploymentDetailsModal();
    expect(store.deploymentDetailsData).toBeNull();
    expect(fetchDeployment).toHaveBeenCalledTimes(2);
  });
  it("keeps an error available for retry", async () => {
    vi.mocked(fetchDeployment).mockRejectedValueOnce(new Error("unavailable"));
    const store = useOverviewStore();
    await store.openDeploymentDetailsModal("id");
    expect(store.deploymentDetailsError).toBe("unavailable");
    expect(store.deploymentDetailsLoading).toBe(false);
  });
});
