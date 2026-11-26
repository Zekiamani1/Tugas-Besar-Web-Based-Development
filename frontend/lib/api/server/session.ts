import { cookies } from "next/headers";
import { cache } from "react";
import {
  parseSessionClaims,
  SESSION_COOKIE,
  type SessionClaims,
} from "@/lib/session";
import type { UserSummary } from "@/lib/types";

export const getServerSessionContext = cache(async (): Promise<{
  claims: SessionClaims | null;
  user: UserSummary | null;
  cookieHeader: string;
}> => {
  const cookieStore = await cookies();
  const rawSession = cookieStore.get(SESSION_COOKIE)?.value;
  const claims = parseSessionClaims(rawSession);
  const backendSessionId = cookieStore.get("session_id")?.value;
  const cookieHeader = backendSessionId ? `session_id=${backendSessionId}` : "";
  const user: UserSummary | null = claims
    ? {
        id: claims.userId,
        username:
          claims.username ??
          (claims.isAdmin ? "Admin" : `User #${claims.userId}`),
        isAdmin: claims.isAdmin,
      }
    : null;
  return {
    claims,
    user,
    cookieHeader,
  };
});
