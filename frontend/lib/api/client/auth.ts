import { SESSION_COOKIE, type SessionClaims } from "@/lib/session";
import type { TargetDBInfo, UserSummary } from "@/lib/types";
import type { AuthService } from "../contract";
import { ApiError, requestJson } from "../http";

export const authService: AuthService = {
  async login(username, password) {
    const trimmedUsername = username.trim();
    if (!trimmedUsername || !password) {
      return {
        ok: false,
        error: "Username dan password wajib diisi.",
      };
    }
    try {
      const res = await requestJson<{ message?: string }>("/login", {
        method: "POST",
        body: JSON.stringify({ username: trimmedUsername, password }),
      });

      const [meRes, dbRes] = await Promise.all([
        requestJson<{ user?: UserSummary }>("/me"),
        requestJson<{ databases?: TargetDBInfo[] }>("/databases"),
      ]);
      const user = meRes.user ?? {
        id: 1,
        username: trimmedUsername,
        isAdmin: false,
      };
      const accessibleDbIds = Array.isArray(dbRes.databases)
        ? dbRes.databases
            .map((db) => db.id)
            .filter((id) => Number.isSafeInteger(id) && id > 0)
        : [];

      const claims: SessionClaims = {
        userId: user.id || 1,
        username: user.username || trimmedUsername,
        isAdmin: Boolean(user.isAdmin),
        accessibleDbIds,
      };
      if (typeof document !== "undefined") {
        document.cookie = `${SESSION_COOKIE}=${encodeURIComponent(JSON.stringify(claims))}; Path=/; SameSite=Lax`;
      }

      return {
        ok: true,
        message: res.message,
        data: {
          id: claims.userId,
          username: claims.username ?? trimmedUsername,
          isAdmin: claims.isAdmin,
        },
      };
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        return { ok: false, error: "Username atau password salah." };
      }
      return {
        ok: false,
        error:
          err instanceof Error && err.message ? err.message : "Login gagal.",
      };
    }
  },

  async logout() {
    try {
      await requestJson("/logout", { method: "POST" });
    } catch {
      // Ignore backend errors during logout so client navigation still clears session.
    } finally {
      if (typeof document !== "undefined") {
        document.cookie = `${SESSION_COOKIE}=; Path=/; Max-Age=0; SameSite=Lax`;
      }
    }
  },
};
