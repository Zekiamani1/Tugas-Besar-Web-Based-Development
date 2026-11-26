import { NextResponse, type NextRequest } from "next/server";
import { parseSessionClaims, SESSION_COOKIE } from "@/lib/session";

const DATABASE_ROUTE_PATTERN = /^\/databases\/([^/]+)(?:\/|$)/;

export function proxy(request: NextRequest) {
  const { pathname } = request.nextUrl;
  const session = parseSessionClaims(request.cookies.get(SESSION_COOKIE)?.value);

  if (pathname === "/") {
    return NextResponse.redirect(
      new URL(session ? "/dashboard" : "/login", request.url)
    );
  }

  if (pathname === "/login") {
    if (session) {
      if (request.nextUrl.searchParams.get("reason") === "session") {
        const response = NextResponse.next();
        response.cookies.set(SESSION_COOKIE, "", { path: "/", maxAge: 0 });
        return response;
      }
      return NextResponse.redirect(new URL("/dashboard", request.url));
    }
    return NextResponse.next();
  }

  if (!session) {
    return NextResponse.redirect(
      new URL("/login?reason=session", request.url)
    );
  }

  if (pathname === "/admin" || pathname.startsWith("/admin/")) {
    if (!session.isAdmin) {
      return NextResponse.rewrite(
        new URL("/forbidden?reason=admin", request.url)
      );
    }
    return NextResponse.next();
  }

  const dbMatch = pathname.match(DATABASE_ROUTE_PATTERN);
  if (dbMatch) {
    const rawDbId = dbMatch[1];
    if (/^\d+$/.test(rawDbId)) {
      const dbId = Number.parseInt(rawDbId, 10);
      if (!session.accessibleDbIds.includes(dbId)) {
        return NextResponse.rewrite(
          new URL("/forbidden?reason=database", request.url)
        );
      }
    }
  }

  return NextResponse.next();
}

export const config = {
  matcher: [
    "/",
    "/login",
    "/dashboard/:path*",
    "/admin/:path*",
    "/databases/:path*",
  ],
};
