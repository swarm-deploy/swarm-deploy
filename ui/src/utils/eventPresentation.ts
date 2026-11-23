import type { EventHistoryItem } from "../api/types";
import { enumLabel, t } from "../i18n";

const stack = ["stack_name"];
const service = [...stack, "service_name"];
const replicas = [...service, "previous_replicas", "current_replicas"];
const eventParameters = new Map<string, string[]>([
  ["AlertOpened", []],
  ["AlertResolved", []],
  ["AssistantPromptInjectionDetected", []],
  ["ConfigurationChanged", []],
  ["DeployFailed", stack],
  ["DeployInterrupted", stack],
  ["DeployPreparationFailed", stack],
  ["DeploySuccess", stack],
  ["GitPullFailed", []],
  ["GitPullSucceeded", []],
  ["MessengerAvailable", []],
  ["MessengerDeliverySucceeded", []],
  ["NetworkCreated", ["network_name"]],
  ["NodeConnected", ["node"]],
  ["NodeDisconnected", ["node"]],
  ["NodeJoined", ["node"]],
  ["SendNotificationFailed", ["destination", "channel", "event_type"]],
  ["ServiceCatalogUpdated", []],
  ["ServiceDriftFixed", service],
  ["ServiceDrifted", [...service, "reason"]],
  ["ServiceMissed", service],
  ["ServicePruned", service],
  ["ServiceRemovedFromDesired", []],
  ["ServiceReplicasDecreased", replicas],
  ["ServiceReplicasIncreased", replicas],
  ["ServiceRestarted", service],
  ["SyncManualStarted", []],
  ["SyncPaused", ["target"]],
  ["SyncPauseExpired", ["target"]],
  ["SyncResumed", ["target"]],
  ["UserAuthenticated", ["username"]],
  ["WebhookReceived", []],
]);

export function eventMessage(event: EventHistoryItem): string {
  // MessengerUnavailable and MessengerMisconfigured carry the diagnostic only in message.
  const required = eventParameters.get(event.type);
  const fallback = event.message || t("events.noMessage");
  if (!required) return fallback;
  const details = event.details ?? {};
  if (event.type === "UserAuthenticated" && details.username === "") {
    return t("eventMessages.UserAuthenticatedAnonymous");
  }
  const params: Record<string, string> = {};
  for (const key of required) {
    let value = details[key];
    if (key === "node") value = details.node_name || details.node_id;
    if (key === "target") value = syncTarget(details);
    if (!value?.trim()) return fallback;
    params[key] = value;
  }
  return t(`eventMessages.${event.type}`, params);
}

function syncTarget(details: Record<string, string>): string {
  if (!details.stack_name?.trim()) return "";
  if (details.scope === "stack") return details.stack_name;
  if (details.scope === "service" && details.service_name?.trim()) {
    return `${details.stack_name}/${details.service_name}`;
  }
  return "";
}

export function eventSeverityLabel(severity: string): string {
  return enumLabel("eventSeverities", severity);
}

export function eventCategoryLabel(category: string): string {
  return enumLabel("eventCategories", category);
}

export function eventDetailLabel(key: string): string {
  return enumLabel("eventFields", key);
}
