// Sizing rules for the canvas side panels (palette + inspector) so the middle
// graph never gets crushed on a narrow window. Kept framework-free and pure so
// the clamp math is unit-testable and reused for both drag and window-resize.

export interface Bounds {
  min: number;
  max: number;
  default: number;
}

export const PALETTE: Bounds = { min: 200, max: 460, default: 280 };
// The rail now hosts the Tendril terminal, so its max doubles as a column
// budget: at fontSize 12 (~7.2px/cell, less a ~14px scrollbar) 640px clears
// the de-facto 80-column floor, where 560 gave only ~75. PALETTE.min (200) +
// 640 + MIN_CANVAS (320) = 1160, so a >=1160px window honours it and narrower
// ones are already handled by clampWidth's container rule below.
export const INSPECTOR: Bounds = { min: 260, max: 640, default: 320 };

// Above this palette width, the item list reflows from one column into two.
// Set to a width the palette can actually reach on a halved window: with the
// inspector at its default (320) and MIN_CANVAS (320) reserved, a ~960px window
// caps the palette at 320px -- so a higher threshold could never trigger there.
// At 300px each of the two cells is ~137px (icon + a truncating label), which
// is tight but legible; labels ellipsis inside the cell.
export const PALETTE_TWO_COL_MIN = 300;

// The graph column is never allowed to shrink below this -- the whole point of
// the feature is to keep the workflow visible between the two panels.
export const MIN_CANVAS = 320;

export const STORAGE_KEY = "agentmesh_panel_widths_v1";

// Clamp a requested panel width against (a) its own min/max and (b) the space
// actually left in the row once the other panel and MIN_CANVAS are reserved.
// `containerWidth <= 0` (or NaN, before the row is measured) skips the
// container constraint and clamps to bounds only.
export function clampWidth(
  requested: number,
  bounds: Bounds,
  containerWidth: number,
  otherPanelWidth: number,
): number {
  let w = requested;
  if (!Number.isFinite(w)) w = bounds.default;
  w = Math.max(bounds.min, Math.min(bounds.max, w));

  if (Number.isFinite(containerWidth) && containerWidth > 0) {
    const maxAllowed = containerWidth - otherPanelWidth - MIN_CANVAS;
    // If there isn't even room for the panel's min, fall back to min and let
    // the canvas column (min-width:0) absorb the overflow -- best we can do
    // without a collapse toggle.
    w = maxAllowed >= bounds.min ? Math.min(w, maxAllowed) : bounds.min;
  }

  return Math.round(w);
}

export interface PanelWidths {
  paletteW: number;
  inspectorW: number;
}

// Best-effort persistence -- mirrors the tolerant load pattern used by the
// credits store. Never throws; returns null when unavailable or corrupt.
export function loadWidths(): PanelWidths | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<PanelWidths>;
    if (
      typeof parsed.paletteW !== "number" ||
      typeof parsed.inspectorW !== "number"
    ) {
      return null;
    }
    return { paletteW: parsed.paletteW, inspectorW: parsed.inspectorW };
  } catch {
    return null;
  }
}

export function saveWidths(widths: PanelWidths): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(widths));
  } catch {
    /* ignore storage quota / availability errors */
  }
}
