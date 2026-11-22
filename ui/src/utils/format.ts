import { formatNumber, locale, t } from "../i18n";

export function formatDate(raw: string | undefined, options?: Intl.DateTimeFormatOptions): string {
  if (!raw) {
    return t("common.notApplicable");
  }

  const parsed = new Date(raw);
  if (Number.isNaN(parsed.valueOf())) {
    return raw;
  }

  return parsed.toLocaleString(locale(), options);
}

export function formatBytes(value: number | undefined): string {
  if (value === undefined || value === null || Number.isNaN(Number(value))) {
    return t("common.notApplicable");
  }

  const bytes = Number(value);
  const units = ["byte", "kilobyte", "megabyte", "gigabyte", "terabyte"];
  let amount = bytes;
  let index = 0;
  while (amount >= 1024 && index < units.length - 1) {
    amount /= 1024;
    index += 1;
  }

  const digits = index === 0 ? 0 : 2;
  return t("common.bytes", { amount: formatDecimal(amount, digits), unit: t(`common.${units[index]}`) });
}

export function formatNanoCPU(value: number | undefined): string {
  if (value === undefined || value === null || Number.isNaN(Number(value))) {
    return t("common.notApplicable");
  }

  const nano = Number(value);
  const cpu = nano / 1_000_000_000;
  if (!Number.isFinite(cpu)) {
    return t("common.notApplicable");
  }

  return t("common.cpuAmount", { amount: formatDecimal(cpu, cpu >= 1 ? 2 : 3) });
}

export function shortCommitHash(raw: string | undefined, length = 6): string {
  const value = String(raw || "").trim();
  if (!value) {
    return t("common.notApplicable");
  }

  const normalizedLength = Number.isFinite(length) ? Math.max(1, Math.floor(length)) : 6;
  return value.slice(0, normalizedLength);
}

export function formatRelativeTime(raw: string | undefined): string {
  if (!raw) return t("common.notApplicable");
  const timestamp = new Date(raw).valueOf();
  if (Number.isNaN(timestamp)) return raw;

  const elapsedSeconds = Math.max(0, Math.floor((Date.now() - timestamp) / 1_000));
  if (elapsedSeconds < 60) return t("common.now");
  if (elapsedSeconds < 3_600) return t("common.minutesAgoCompact", { count: formatNumber(Math.floor(elapsedSeconds / 60)) });
  if (elapsedSeconds < 86_400) return t("common.hoursAgoCompact", { count: formatNumber(Math.floor(elapsedSeconds / 3_600)) });
  return t("common.daysAgoCompact", { count: formatNumber(Math.floor(elapsedSeconds / 86_400)) });
}

export function formatDecimal(value: number, digits: number): string {
  return formatNumber(value, { minimumFractionDigits: digits, maximumFractionDigits: digits });
}
