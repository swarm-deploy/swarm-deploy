export function useShare() {
  const isSupported = typeof navigator !== "undefined" && typeof navigator.share === "function";

  async function share(data: ShareData): Promise<void> {
    if (!isSupported) throw new Error("Web Share API is unavailable");
    if (typeof navigator.canShare === "function" && !navigator.canShare(data)) {
      throw new TypeError("The browser cannot share this data");
    }

    try {
      await navigator.share(data);
    } catch (error) {
      if (!(error instanceof DOMException && error.name === "AbortError")) throw error;
    }
  }

  return { isSupported, share };
}
