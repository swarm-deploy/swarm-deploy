import type { Alert, AlertFinding } from "../api/types";
import { enumLabel, t } from "../i18n";
import en from "../i18n/en.json";

export function alertTitle(alert: Alert): string {
  const canonicalTitle = en.alertTitles[alert.kind];
  const hasInterruptedTitle = alert.kind === "sync_failed" && alert.title === en.alerts.syncOutcomeUnknown;
  // Keep older or custom titles that may carry diagnostic context of their own.
  if (alert.title && alert.title !== canonicalTitle && !hasInterruptedTitle) return alert.title;
  if (alert.kind === "sync_failed" && (
    hasInterruptedTitle || alert.errorCode === "process_interrupted" || alert.errorCode === "verification_unknown"
  )) {
    return t("alerts.syncOutcomeUnknown");
  }
  return enumLabel("alertTitles", alert.kind, t("alerts.alert"));
}

export function alertKindLabel(kind: string): string {
  return enumLabel("alertKinds", kind);
}

export function alertStatusLabel(status: string): string {
  return enumLabel("alertStatuses", status);
}

export function alertResourceLabel(resourceType: string): string {
  return enumLabel("alertResources", resourceType);
}

export function alertFindingTitle(finding: AlertFinding): string {
  return enumLabel("alertFindings", finding.code, finding.summary || finding.code);
}
