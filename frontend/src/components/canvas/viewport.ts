// Pan/zoom transform maths for the canvas, kept pure and framework-free so the
// arithmetic is unit-testable on its own -- the same reason panelSizing.ts sits
// beside it rather than living inside CanvasGraph.
//
// The view is a screen-space transform applied as
// `translate(x, y) scale(k)`, so a world point (wx, wy) lands on screen at
// (wx * k + x, wy * k + y).

export interface ViewState {
  x: number;
  y: number;
  k: number;
}

// The zoom range the on-screen controls and the wheel have always used.
export const MIN_SCALE = 0.3;
export const MAX_SCALE = 2;

export const DEFAULT_VIEW: ViewState = { x: 40, y: 40, k: 0.95 };

export function clampScale(k: number): number {
  if (Number.isNaN(k)) return DEFAULT_VIEW.k;
  return Math.min(MAX_SCALE, Math.max(MIN_SCALE, k));
}

// Zoom to `nextK` while keeping the screen point (px, py) pinned under the
// cursor or the pinch's midpoint. Without the anchor the graph slides out from
// under the fingers, which is what makes a pinch feel broken.
export function zoomAbout(
  view: ViewState,
  nextK: number,
  px: number,
  py: number,
): ViewState {
  const k = clampScale(nextK);
  const f = k / view.k;
  return { x: px - (px - view.x) * f, y: py - (py - view.y) * f, k };
}

export function zoomByFactor(
  view: ViewState,
  factor: number,
  px: number,
  py: number,
): ViewState {
  return zoomAbout(view, view.k * factor, px, py);
}

export interface Point {
  x: number;
  y: number;
}

// Screen point (relative to the canvas box) -> world point. The exact
// inverse of the projection the view applies: a world point lands on screen
// at `w * k + offset`, so recovering it is `(screen - offset) / k`.
//
// Shared by the two ways a node gets placed -- dropped at the cursor, or
// added at the centre of the view by tapping the palette -- so the two
// cannot drift apart.
export function screenToWorld(view: ViewState, px: number, py: number): Point {
  return { x: (px - view.x) / view.k, y: (py - view.y) / view.k };
}

export function distance(a: Point, b: Point): number {
  return Math.hypot(b.x - a.x, b.y - a.y);
}

export function midpoint(a: Point, b: Point): Point {
  return { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 };
}

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

// Frames every node in the viewport at once.
//
// This is what makes the graph usable on a phone: the stored view is whatever
// the last editor left behind, and at 375px wide that regularly puts every node
// off-screen, so the page opens on empty grid with no clue which way to drag.
// Returns DEFAULT_VIEW when there is nothing to frame or no measured viewport.
export function fitToRects(
  rects: readonly Rect[],
  viewportW: number,
  viewportH: number,
  padding = 48,
): ViewState {
  if (rects.length === 0) return DEFAULT_VIEW;
  if (!(viewportW > 0) || !(viewportH > 0)) return DEFAULT_VIEW;

  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  for (const r of rects) {
    minX = Math.min(minX, r.x);
    minY = Math.min(minY, r.y);
    maxX = Math.max(maxX, r.x + r.w);
    maxY = Math.max(maxY, r.y + r.h);
  }
  if (!Number.isFinite(minX) || !Number.isFinite(minY)) return DEFAULT_VIEW;

  const contentW = Math.max(1, maxX - minX);
  const contentH = Math.max(1, maxY - minY);

  // Halve the padding budget per axis (it applies to both edges), and never
  // let a viewport smaller than its own padding produce a zero or negative
  // available size.
  const availW = Math.max(1, viewportW - padding * 2);
  const availH = Math.max(1, viewportH - padding * 2);

  // Never zoom PAST 1 to fill the screen -- a two-node workflow blown up to
  // 200% looks broken rather than framed.
  //
  // clampScale can also refuse to zoom far enough OUT: a sprawling graph on a
  // 375px screen would need well under MIN_SCALE to fit, and at that size the
  // nodes are unreadable anyway. When that happens the content stays centred
  // and overflows, so the viewer opens in the middle of the graph with
  // something legible on screen and pans from there.
  const k = clampScale(Math.min(availW / contentW, availH / contentH, 1));

  // Centre the content box in the viewport at that scale.
  return {
    x: (viewportW - contentW * k) / 2 - minX * k,
    y: (viewportH - contentH * k) / 2 - minY * k,
    k,
  };
}
