import type { Recommendation } from "../api/types";
import { enumLabel, t } from "../i18n";

const recommendationKeys = new Map<string, string>([
  ["service.resources.unspecified", "resources"],
  ["service.resources.limits.unspecified", "resourceLimits"],
  ["service.image.latest", "imageLatest"],
  ["service.image.digest.unspecified", "imageDigest"],
  ["service.restart-policy.unspecified", "restartPolicy"],
  ["service.self-heal.scheduled-service", "scheduledService"],
  ["service.secrets.environment", "environmentSecrets"],
  ["service.docker-socket.mount", "dockerSocket"],
  ["service.capabilities.all", "capabilitiesAll"],
  ["service.capabilities.privileged", "capabilitiesPrivileged"],
  ["service.capabilities.sensitive", "capabilitiesSensitive"],
  ["service.host-port.start-first", "hostPortStartFirst"],
  ["service.swarm-deploy.docker-config.missing", "dockerConfig"],
]);

export function recommendationTitle(recommendation: Recommendation): string {
  const key = recommendationKeys.get(recommendation.type);
  return key ? t(`recommendationTitles.${key}`) : recommendation.title;
}

export function recommendationAdvice(recommendation: Recommendation): string {
  const key = recommendationKeys.get(recommendation.type);
  // These two analyzers embed data that is not recoverable from the type alone.
  if (!key || key === "scheduledService" || key === "environmentSecrets") {
    return recommendation.recommendation || t("recommendations.noAdvice");
  }
  return t(`recommendationAdvice.${key}`);
}

export function recommendationSeverityLabel(severity: string): string {
  return enumLabel("recommendationSeverities", severity);
}
