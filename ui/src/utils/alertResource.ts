import type { Alert } from "../api/types";

export function alertResourceRoute({ resourceType, resourceId }: Pick<Alert, "resourceType" | "resourceId">) {
  if (resourceType === "stack" && resourceId) {
    return { name: "stack-details", params: { stack: resourceId } };
  }
  if (resourceType !== "service") return null;

  const separator = resourceId.indexOf("/");
  if (separator <= 0 || separator === resourceId.length - 1) return null;
  return {
    name: "service-details",
    params: { stack: resourceId.slice(0, separator), service: resourceId.slice(separator + 1) },
  };
}
