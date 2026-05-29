interface ApiErrorPayload {
  error_message?: string;
}

export class ApiError extends Error {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

async function readErrorMessage(response: Response): Promise<string> {
  try {
    const payload = (await response.json()) as ApiErrorPayload;
    if (payload.error_message) {
      return payload.error_message;
    }
  } catch {
    // Keep default fallback.
  }

  return `HTTP ${response.status}`;
}

export async function apiRequest<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, init);
  if (!response.ok) {
    const errorMessage = await readErrorMessage(response);
    throw new ApiError(errorMessage, response.status);
  }

  return (await response.json()) as T;
}
