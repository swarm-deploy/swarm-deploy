import { syncPauseDurations } from "../api/syncPauses";
import { plural, t } from "../i18n";

export function syncPauseDurationOptions(): { value: string; label: string }[] {
  return syncPauseDurations.map(({ value, milliseconds }) => ({
    value,
    label: milliseconds === null ? t("syncPause.noExpiration") : plural("syncPause.hours", Number(value)),
  }));
}
