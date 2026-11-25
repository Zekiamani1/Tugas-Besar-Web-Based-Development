import { NextRequest } from "next/server";
import { describe, expect, it } from "vitest";
import {
  parseSessionClaims,
  SESSION_COOKIE,
  type SessionClaims,
} from "@/lib/session";
import { proxy } from "@/proxy";

function buildSessionCookie(claims: SessionClaims): string {
  return `${SESSION_COOKIE}=${encodeURIComponent(JSON.stringify(claims))}`;
}

function buildRequest(path: string, cookieHeader?: string): NextRequest {
  const headers = new Headers();
  if (cookieHeader) {
    headers.set("cookie", cookieHeader);
  }
  return new NextRequest(`http://localhost:3000${path}`, { headers });
}

const ADMIN_CLAIMS: SessionClaims = {
  userId: 1,
  isAdmin: true,
  accessibleDbIds: [1, 2, 3],
};

const DEVELOPER_CLAIMS: SessionClaims = {
  userId: 2,
  isAdmin: false,
  accessibleDbIds: [1, 2, 3],
};

const ANALYST_CLAIMS: SessionClaims = {
  userId: 3,
  isAdmin: false,
  accessibleDbIds: [1, 2],
};

describe("Session claims serialization & Next.js 16 proxy middleware", () => {
  it("parses valid session claims while rejecting malformed cookies", () => {
    const encoded = encodeURIComponent(JSON.stringify(ANALYST_CLAIMS));
    expect(parseSessionClaims(encoded)).toEqual(ANALYST_CLAIMS);

    expect(parseSessionClaims(undefined)).toBeNull();
    expect(parseSessionClaims("")).toBeNull();
    expect(parseSessionClaims("not-json")).toBeNull();
    expect(
      parseSessionClaims(
        encodeURIComponent(
          JSON.stringify({ userId: 0, isAdmin: false, accessibleDbIds: [1] })
        )
      )
    ).toBeNull();
    expect(
      parseSessionClaims(
        encodeURIComponent(
          JSON.stringify({
            userId: 1,
            isAdmin: "yes",
            accessibleDbIds: [1],
          })
        )
      )
    ).toBeNull();
    expect(
      parseSessionClaims(
        encodeURIComponent(
          JSON.stringify({
            userId: 1,
            isAdmin: false,
            accessibleDbIds: [1, -5],
          })
        )
      )
    ).toBeNull();
  });

  it("redirects / based on authentication status", () => {
    const unauthRes = proxy(buildRequest("/"));
    expect(unauthRes.status).toBe(307);
    expect(unauthRes.headers.get("location")).toBe("http://localhost:3000/login");

    const authRes = proxy(buildRequest("/", buildSessionCookie(ADMIN_CLAIMS)));
    expect(authRes.status).toBe(307);
    expect(authRes.headers.get("location")).toBe(
      "http://localhost:3000/dashboard"
    );
  });

  it("allows /login when unauthenticated and redirects to /dashboard when authenticated", () => {
    const unauthRes = proxy(buildRequest("/login"));
    expect(unauthRes.status).toBe(200);
    expect(unauthRes.headers.get("x-middleware-next")).toBe("1");

    const authRes = proxy(
      buildRequest("/login", buildSessionCookie(DEVELOPER_CLAIMS))
    );
    expect(authRes.status).toBe(307);
    expect(authRes.headers.get("location")).toBe(
      "http://localhost:3000/dashboard"
    );
  });

  it("redirects unauthenticated access on protected routes to /login?reason=session", () => {
    for (const path of [
      "/dashboard",
      "/admin",
      "/databases/1",
      "/databases/1/migrations",
    ]) {
      const res = proxy(buildRequest(path));
      expect(res.status).toBe(307);
      expect(res.headers.get("location")).toBe(
        "http://localhost:3000/login?reason=session"
      );
    }
  });

  it("enforces Platform Admin authorization on /admin routes with /forbidden rewrite", () => {
    const nonAdminRes = proxy(
      buildRequest("/admin", buildSessionCookie(DEVELOPER_CLAIMS))
    );
    expect(nonAdminRes.headers.get("x-middleware-rewrite")).toBe(
      "http://localhost:3000/forbidden?reason=admin"
    );

    const adminRes = proxy(
      buildRequest("/admin", buildSessionCookie(ADMIN_CLAIMS))
    );
    expect(adminRes.status).toBe(200);
    expect(adminRes.headers.get("x-middleware-next")).toBe("1");
  });

  it("enforces per-database authorization on /databases/[dbID] and /migrations routes", () => {
    const cookie = buildSessionCookie(ANALYST_CLAIMS);

    const allowedDb = proxy(buildRequest("/databases/1", cookie));
    expect(allowedDb.status).toBe(200);
    expect(allowedDb.headers.get("x-middleware-next")).toBe("1");

    const forbiddenDb = proxy(buildRequest("/databases/3", cookie));
    expect(forbiddenDb.headers.get("x-middleware-rewrite")).toBe(
      "http://localhost:3000/forbidden?reason=database"
    );

    const forbiddenMig = proxy(buildRequest("/databases/3/migrations", cookie));
    expect(forbiddenMig.headers.get("x-middleware-rewrite")).toBe(
      "http://localhost:3000/forbidden?reason=database"
    );
  });

  it("enforces dynamic database lists for custom session claims", () => {
    const customCookie = buildSessionCookie({
      userId: 5,
      isAdmin: false,
      accessibleDbIds: [4],
    });

    expect(proxy(buildRequest("/databases/4", customCookie)).status).toBe(200);

    const deniedOtherDb = proxy(buildRequest("/databases/1", customCookie));
    expect(deniedOtherDb.headers.get("x-middleware-rewrite")).toBe(
      "http://localhost:3000/forbidden?reason=database"
    );

    const deniedAdmin = proxy(buildRequest("/admin", customCookie));
    expect(deniedAdmin.headers.get("x-middleware-rewrite")).toBe(
      "http://localhost:3000/forbidden?reason=admin"
    );
  });
});
