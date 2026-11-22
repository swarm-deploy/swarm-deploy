import { formatNumber, t } from "../i18n";
import type { StackStatus } from "../api/types";

// Keep the Overview and Stack Details presentation of the API counters identical.
export function stackSyncStatus(status: StackStatus | undefined) {
  const synced = Number(status?.synced ?? 0);
  const outOfSync = Number(status?.out_of_synced ?? 0);
  if (outOfSync > 0) return { kind: "out-of-sync", label: t("stack.outOfSyncCount", { count: formatNumber(outOfSync) }) };
  if (synced > 0) return { kind: "synced", label: t("stack.synced") };
  return { kind: "unknown", label: t("common.unknown") };
}
