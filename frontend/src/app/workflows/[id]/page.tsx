import { Suspense } from "react";
import { WorkflowRouteFromUrl } from "@/components/workflows/WorkflowRouteFromUrl";

// The native shell (mobile/) ships a static export, which cannot prerender a
// page per workflow -- the ids belong to users who do not exist at build time.
// So that build emits a single shell page and the real id arrives as ?id=,
// resolved in WorkflowRouteFromUrl. The web build returns no params here and
// keeps rendering every workflow on demand exactly as before.
export const MOBILE_SHELL_ID = "app";

export function generateStaticParams() {
  return process.env.MOBILE_BUILD === "1" ? [{ id: MOBILE_SHELL_ID }] : [];
}

export default async function CanvasPageRoute({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = await params;
  // Suspense boundary: WorkflowRouteFromUrl calls useSearchParams(), and
  // CanvasPage below it does too for the Bazaar `?add=` handoff -- Next 16
  // requires a boundary somewhere above those calls or `next build` fails.
  return (
    <Suspense fallback={null}>
      <WorkflowRouteFromUrl routeId={id} />
    </Suspense>
  );
}
