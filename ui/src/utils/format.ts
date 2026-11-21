export function formatDate(raw: string | undefined): string {
  if (!raw) {
    return "n/a";
  }

  const parsed = new Date(raw);
  if (Number.isNaN(parsed.valueOf())) {
    return raw;
  }

  return parsed.toLocaleString();
}

export function formatBytes(value: number | undefined): string {
  if (value === undefined || value === null || Number.isNaN(Number(value))) {
    return "n/a";
  }

  const bytes = Number(value);
  const units = ["B", "KB", "MB", "GB", "TB"];
  let amount = bytes;
  let index = 0;
  while (amount >= 1024 && index < units.length - 1) {
    amount /= 1024;
    index += 1;
  }

  return `${amount.toFixed(index === 0 ? 0 : 2)} ${units[index]}`;
}

export function formatNanoCPU(value: number | undefined): string {
  if (value === undefined || value === null || Number.isNaN(Number(value))) {
    return "n/a";
  }

  const nano = Number(value);
  const cpu = nano / 1_000_000_000;
  if (!Number.isFinite(cpu)) {
    return "n/a";
  }

  return `${cpu.toFixed(cpu >= 1 ? 2 : 3)} CPU`;
}

export function shortCommitHash(raw: string | undefined, length = 6): string {
  const value = String(raw || "").trim();
  if (!value) {
    return "n/a";
  }

  const normalizedLength = Number.isFinite(length) ? Math.max(1, Math.floor(length)) : 6;
  return value.slice(0, normalizedLength);
}

export function formatRelativeTime(raw: string | undefined): string {
  if (!raw) return "n/a";
  const timestamp = new Date(raw).valueOf();
  if (Number.isNaN(timestamp)) return raw;

  const elapsedSeconds = Math.max(0, Math.floor((Date.now() - timestamp) / 1_000));
  if (elapsedSeconds < 60) return "now";
  if (elapsedSeconds < 3_600) return `${Math.floor(elapsedSeconds / 60)}m ago`;
  if (elapsedSeconds < 86_400) return `${Math.floor(elapsedSeconds / 3_600)}h ago`;
  return `${Math.floor(elapsedSeconds / 86_400)}d ago`;
}
