export const SESSION_COOKIE = "wbd_session";

export interface SessionClaims {
  userId: number;
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
    const decoded = decodeURIComponent(raw.trim());
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
    return {
      userId: candidate.userId,
      isAdmin: candidate.isAdmin,
      accessibleDbIds: candidate.accessibleDbIds,
    };
  } catch {
    return null;
  }
}
