import { SESSION_COOKIE } from "@/lib/session";

export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

export function getApiBaseUrl(): string {
  const raw =
    typeof window === "undefined"
      ? process.env.INTERNAL_API_URL ||
        process.env.NEXT_PUBLIC_API_URL ||
        "http://localhost:8080"
      : process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";
  return raw.replace(/\/+$/, "");
}

export interface RequestJsonOptions extends RequestInit {
  cookieHeader?: string;
  onResponse?: (response: Response) => void;
}

export async function requestJson<T>(
  url: string,
  options: RequestJsonOptions = {}
): Promise<T> {
  const {
    cookieHeader,
    onResponse,
    headers: initHeaders,
    credentials = "include",
    ...rest
  } = options;

  const headers = new Headers(initHeaders);
  if (!headers.has("Accept")) {
    headers.set("Accept", "application/json");
  }
  if (rest.body !== undefined && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  if (cookieHeader && !headers.has("Cookie")) {
    headers.set("Cookie", cookieHeader);
  }

  const resolvedUrl = url.startsWith("/") ? `${getApiBaseUrl()}${url}` : url;
  const response = await fetch(resolvedUrl, {
    ...rest,
    credentials,
    headers,
  });

  if (response.status === 401 && typeof document !== "undefined") {
    document.cookie = `${SESSION_COOKIE}=; Path=/; Max-Age=0; SameSite=Lax`;
  }

  let payload: unknown = null;
  const contentType = response.headers.get("content-type") ?? "";
  try {
    if (contentType.includes("application/json")) {
      payload = await response.json();
    } else {
      const text = (await response.text()).trim();
      payload = text.length > 0 ? text : null;
    }
  } catch {
    payload = null;
  }

  if (!response.ok) {
    let message = response.statusText || "Request failed";
    if (typeof payload === "string" && payload.trim().length > 0) {
      message = payload.trim();
    } else if (
      typeof payload === "object" &&
      payload !== null &&
      "error" in payload
    ) {
      const errorValue = (payload as { error?: unknown }).error;
      if (typeof errorValue === "string" && errorValue.trim().length > 0) {
        message = errorValue.trim();
      }
    }
    throw new ApiError(response.status, message);
  }

  onResponse?.(response);

  return payload as T;
}
