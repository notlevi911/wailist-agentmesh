"use client";
import { useSyncExternalStore } from "react";

// Below this width the studio cannot show its three columns at once: the
// palette (200 min) + canvas (320 min) + rail (260 min) already need 780px
// before any chrome, so a compact viewport gets a restructured tree rather
// than a squeezed one. Matches the 1024px breakpoint responsive.css uses, in
// the `.98` form so it can never overlap a `min-width: 1024px` rule.
export const COMPACT_QUERY = "(max-width: 1023.98px)";

// Layout that only *styles* differently by width belongs in responsive.css.
// This hook is for the cases CSS genuinely cannot express — where the compact
// layout is a different React tree (the rail becomes a bottom sheet, the
// palette stops mounting at all), not the same tree with different rules.

// Cached, not re-created per call: useSyncExternalStore invokes getSnapshot()
// on every render of every component that calls useIsCompact(), and
// window.matchMedia() allocates a new MediaQueryList each time it's called.
// The query itself is fixed for the module's lifetime -- only its .matches
// value changes, which subscribe() below already listens for -- so caching
// the MediaQueryList is safe. Same fix as useReadOnly.ts's identical pattern.
let cachedQuery: MediaQueryList | null = null;

function query(): MediaQueryList | null {
  if (typeof window === "undefined" || !window.matchMedia) return null;
  if (!cachedQuery) cachedQuery = window.matchMedia(COMPACT_QUERY);
  return cachedQuery;
}

function subscribe(onChange: () => void): () => void {
  const mql = query();
  if (!mql) return () => {};
  mql.addEventListener("change", onChange);
  return () => mql.removeEventListener("change", onChange);
}

function getSnapshot(): boolean {
  return query()?.matches ?? false;
}

// The server has no viewport to measure, so it always renders the desktop
// tree. React reconciles to the compact one on the client's first commit if
// the media query matches. Returning anything else here would mean the SSR
// markup and the first client render disagree — a hydration error.
function getServerSnapshot(): boolean {
  return false;
}

export function useIsCompact(): boolean {
  return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}
