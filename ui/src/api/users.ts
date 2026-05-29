import { apiRequest } from "./client";
import type { CurrentUserResponse } from "./types";

export function fetchCurrentUser(): Promise<CurrentUserResponse> {
  return apiRequest<CurrentUserResponse>("/api/v1/users/me");
}
