import { enumLabel, locale } from "../i18n";

export function deploymentStatus(status?: string, lowerCase = false): string {
  const value = status?.trim() || "unknown";
  const label = enumLabel("deploymentStatuses", value, value.charAt(0).toUpperCase() + value.slice(1));
  return lowerCase ? label.toLocaleLowerCase(locale()) : label;
}

export function deploymentPhase(phase?: string, lowerCase = false): string {
  if (!phase) return "—";
  const label = enumLabel("deploymentPhases", phase);
  return lowerCase ? label.toLocaleLowerCase(locale()) : label;
}
