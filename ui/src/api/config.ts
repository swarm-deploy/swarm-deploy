import { apiRequest } from "./client";

interface UIConfigResponse {
  web: {
    ui: {
      logViewer: {
        linkFields: Record<string, string>;
      };
    };
  };
}

export async function fetchLogLinkFields(signal: AbortSignal): Promise<Record<string, string>> {
  const config = await apiRequest<UIConfigResponse>("/api/v1/config", { signal });
  return config.web.ui.logViewer.linkFields ?? {};
}
