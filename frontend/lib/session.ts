export const SESSION_COOKIE = "wbd_session";

export interface SessionClaims {
  userId: number;
  username?: string;
  isAdmin: boolean;
  accessibleDbIds: number[];
}

export function parseSessionClaims(
  raw: string | undefined | null
): SessionClaims | null {
  if (!raw) {
    return null;
  }
  try {
    const trimmed = raw.trim();
    const decoded = trimmed.startsWith("{")
      ? trimmed
      : decodeURIComponent(trimmed);
    const parsed: unknown = JSON.parse(decoded);
    if (typeof parsed !== "object" || parsed === null) {
      return null;
    }
    const candidate = parsed as Record<string, unknown>;
    if (
      typeof candidate.userId !== "number" ||
      !Number.isSafeInteger(candidate.userId) ||
      candidate.userId <= 0
    ) {
      return null;
    }
    if (typeof candidate.isAdmin !== "boolean") {
      return null;
    }
    if (
      !Array.isArray(candidate.accessibleDbIds) ||
      !candidate.accessibleDbIds.every(
        (id) => typeof id === "number" && Number.isSafeInteger(id) && id > 0
      )
    ) {
      return null;
    }
    const claims: SessionClaims = {
      userId: candidate.userId,
      isAdmin: candidate.isAdmin,
      accessibleDbIds: candidate.accessibleDbIds,
    };
    if (
      typeof candidate.username === "string" &&
      candidate.username.trim().length > 0
    ) {
      claims.username = candidate.username;
    }
    return claims;
  } catch {
    return null;
  }
}
