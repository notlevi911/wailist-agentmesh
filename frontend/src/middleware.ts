import { NextRequest, NextResponse } from "next/server";

const PROTECTED = ["/workflows", "/billing", "/usage", "/bazaar"];
// agentmesh_ui is a non-sensitive first-party cookie set by useAuth on the
// frontend domain. The real auth is the HttpOnly agentmesh_token cookie sent
// directly to the backend -- that cookie lives on the API domain and is never
// visible here. agentmesh_ui is just the signal for this middleware check.
const AUTH_COOKIE = "agentmesh_ui";

export function middleware(req: NextRequest) {
  const { pathname } = req.nextUrl;

  const isProtected = PROTECTED.some(
    (p) => pathname === p || pathname.startsWith(p + "/"),
  );

  if (isProtected && !req.cookies.get(AUTH_COOKIE)?.value) {
    const url = req.nextUrl.clone();
    url.pathname = "/signin";
    // Keep the query string: deep links like /usage?workflow=<id> must survive
    // the auth round-trip or the page loses its filter after sign-in.
    url.search = "";
    url.searchParams.set("next", pathname + req.nextUrl.search);
    return NextResponse.redirect(url);
  }

  return NextResponse.next();
}

export const config = {
  matcher: [
    "/workflows",
    "/workflows/:path*",
    "/billing",
    "/billing/:path*",
    "/usage",
    "/usage/:path*",
    "/bazaar",
    "/bazaar/:path*",
  ],
};
