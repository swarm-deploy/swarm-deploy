interface RuntimeConfig {
  basePath?: string;
}

const runtimeConfigElementID = "swarm-deploy-runtime-config";

export function getWebBasePath(): string {
  const element = document.getElementById(runtimeConfigElementID);
  if (!element?.textContent) {
    return "/";
  }

  try {
    const config = JSON.parse(element.textContent) as RuntimeConfig;
    const basePath = String(config.basePath || "/").trim();
    if (basePath === "/") {
      return "/";
    }

    return basePath.replace(/\/+$/, "");
  } catch {
    return "/";
  }
}

export function withWebBasePath(path: string): string {
  const basePath = getWebBasePath();
  if (basePath === "/") {
    return path;
  }

  return `${basePath}${path}`;
}
