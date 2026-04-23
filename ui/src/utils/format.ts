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
