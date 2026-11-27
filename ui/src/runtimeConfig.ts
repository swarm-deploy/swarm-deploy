export type Locale = "en" | "ru";

interface RuntimeConfig {
  basePath?: string;
  locale?: string;
  ntfyTopic?: string;
}

const runtimeConfigElementID = "swarm-deploy-runtime-config";

function readRuntimeConfig(): RuntimeConfig {
  const element = document.getElementById(runtimeConfigElementID);
  if (!element?.textContent) {
    return {};
  }

  try {
    return (JSON.parse(element.textContent) as RuntimeConfig | null) ?? {};
  } catch {
    return {};
  }
}

export function getWebBasePath(): string {
  const basePath = String(readRuntimeConfig().basePath || "/").trim();
  return basePath === "/" ? "/" : basePath.replace(/\/+$/, "");
}

export function getNtfyTopic(): string {
  return readRuntimeConfig().ntfyTopic ?? "";
}

export function getLocale(): Locale {
  return readRuntimeConfig().locale === "ru" ? "ru" : "en";
}

export function withWebBasePath(path: string): string {
  const basePath = getWebBasePath();
  if (basePath === "/") {
    return path;
  }

  return `${basePath}${path}`;
}
