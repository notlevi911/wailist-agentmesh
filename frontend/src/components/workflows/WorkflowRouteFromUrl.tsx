"use client";
import { useSearchParams } from "next/navigation";
import { WorkflowRoute } from "./WorkflowRoute";
import { IS_NATIVE } from "@/lib/nativeAuth";

// Which workflow this page is showing, resolved from the URL.
//
// On the web the answer is simply the route segment: /workflows/<id>. The
// native shell cannot use that. Its bundle is a static export served from the
// device, and a static export can only contain pages that existed at build
// time -- it has no way to prerender a page per workflow, because the ids
// belong to users who have not signed up yet. So the mobile build emits ONE
// shell page and the real id travels as ?id=.
//
// Resolved here, in a client component, rather than from the server
// component's searchParams: a static export prerenders that server component
// once, with no request and therefore no query string, so the answer has to be
// read in the browser.
//
// `key` is what makes switching workflows reset the editor rather than carry
// state across, so it has to be the EFFECTIVE id -- keying on the route
// segment would be a constant in the native shell and never remount.
export function WorkflowRouteFromUrl({ routeId }: { routeId: string }) {
  const fromQuery = useSearchParams().get("id");
  // Only the native shell's ?id= is meant to override the route segment --
  // the web build's routeId already IS the real id, straight from the path.
  // Applying fromQuery unconditionally let a web URL like
  // /workflows/<real-id>?id=<other-id> silently open a different workflow
  // than the path says, since a query param a user (or a stale/crafted link)
  // added would win over the actual route.
  const workflowId = IS_NATIVE && fromQuery ? fromQuery : routeId;
  return <WorkflowRoute key={workflowId} workflowId={workflowId} />;
}
