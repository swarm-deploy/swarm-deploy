import { afterEach, describe, expect, it, vi } from "vitest";

import { useShare } from "./useShare";

const data: ShareData = { title: "Chat title", text: "**Reply**", url: "https://example.com/overview?assistant=chat&reply=1" };

afterEach(() => vi.unstubAllGlobals());

describe("useShare", () => {
  it("reports unavailable Web Share API", async () => {
    vi.stubGlobal("navigator", {});
    const { isSupported, share } = useShare();

    expect(isSupported).toBe(false);
    await expect(share(data)).rejects.toThrow("Web Share API is unavailable");
  });

  it("shares immediately when canShare is absent", async () => {
    const nativeShare = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { share: nativeShare });
    const { isSupported, share } = useShare();

    expect(isSupported).toBe(true);
    const result = share(data);
    expect(nativeShare).toHaveBeenCalledExactlyOnceWith(data);
    await expect(result).resolves.toBeUndefined();
  });

  it.each([true, false])("validates data before sharing when canShare returns %s", async (allowed) => {
    const nativeShare = vi.fn().mockResolvedValue(undefined);
    const canShare = vi.fn().mockReturnValue(allowed);
    vi.stubGlobal("navigator", { share: nativeShare, canShare });
    const { share } = useShare();

    if (allowed) {
      await expect(share(data)).resolves.toBeUndefined();
      expect(nativeShare).toHaveBeenCalledExactlyOnceWith(data);
      expect(canShare).toHaveBeenCalledBefore(nativeShare);
    } else {
      await expect(share(data)).rejects.toThrow(TypeError);
      expect(nativeShare).not.toHaveBeenCalled();
    }
    expect(canShare).toHaveBeenCalledExactlyOnceWith(data);
  });

  it("treats dismissal as a normal outcome", async () => {
    vi.stubGlobal("navigator", { share: vi.fn().mockRejectedValue(new DOMException("Dismissed", "AbortError")) });
    await expect(useShare().share(data)).resolves.toBeUndefined();
  });

  it("propagates other Web Share errors to the caller", async () => {
    const error = new DOMException("Blocked", "NotAllowedError");
    vi.stubGlobal("navigator", { share: vi.fn().mockRejectedValue(error) });
    await expect(useShare().share(data)).rejects.toBe(error);
  });
});
