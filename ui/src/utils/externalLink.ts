export function safeExternalLink(raw?: string): string {
  const value = raw?.trim();
  if (!value) {
    return "";
  }

  try {
    const parsed = new URL(value);
    return parsed.protocol === "http:" || parsed.protocol === "https:" ? parsed.toString() : "";
  } catch {
    return "";
  }
}
