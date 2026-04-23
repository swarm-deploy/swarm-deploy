import { apiRequest } from "./client";
import type { SearchResponse } from "./types";

export function fetchSearch(query: string): Promise<SearchResponse> {
  const encodedQuery = encodeURIComponent(query);
  return apiRequest<SearchResponse>(`/api/v1/search?query=${encodedQuery}`);
}
