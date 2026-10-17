import { clampWidth, MIN_CANVAS, type Bounds } from "./panelSizing";

// Match .am-studio-rail in responsive.css.
export const RAIL_W = 26;
// Match the occupied width of ResizeHandle.
const HANDLE_W = 6;

export function clampStudioPanelWidth(
  requested: number,
  bounds: Bounds,
  rowWidth: number,
  otherWidth: number,
  otherOpen: boolean,
): number {
  return clampWidth(
    requested,
    bounds,
    rowWidth > 0 ? rowWidth - HANDLE_W - (otherOpen ? HANDLE_W : 0) : rowWidth,
    otherWidth,
  );
}

export interface StudioFitInput {
  /** Measured width of the panel row. 0 or NaN = not measured yet. */
  rowWidth: number;
  paletteW: number;
  inspectorW: number;
  /** Reader has not collapsed the palette, and is allowed to see one. */
  paletteWanted: boolean;
  /** Reader has not collapsed the chat/inspector rail. */
  inspectorWanted: boolean;
  /** Last expanded panel. Apply only while both panels cannot fit. */
  prefer?: "palette" | "inspector" | null;
}

export interface StudioFit {
  palette: boolean;
  inspector: boolean;
  /** True when the window, not the reader, closed something. */
  forced: boolean;
}

function widthOf(open: boolean, w: number): number {
  return open ? w + HANDLE_W : RAIL_W;
}

function fits(
  rowWidth: number,
  palette: boolean,
  inspector: boolean,
  paletteW: number,
  inspectorW: number,
): boolean {
  return (
    widthOf(palette, paletteW) + widthOf(inspector, inspectorW) + MIN_CANVAS <=
    rowWidth
  );
}

export function fitPanels(input: StudioFitInput): StudioFit {
  const {
    rowWidth,
    paletteW,
    inspectorW,
    paletteWanted,
    inspectorWanted,
    prefer = null,
  } = input;

  // Unmeasured: the reader's choice stands until the ResizeObserver lands.
  if (!Number.isFinite(rowWidth) || rowWidth <= 0) {
    return {
      palette: paletteWanted,
      inspector: inspectorWanted,
      forced: false,
    };
  }

  // An explicit single-panel choice remains usable even below MIN_CANVAS.
  if (!paletteWanted || !inspectorWanted) {
    return {
      palette: paletteWanted,
      inspector: inspectorWanted,
      forced: false,
    };
  }

  if (fits(rowWidth, true, true, paletteW, inspectorW)) {
    return { palette: true, inspector: true, forced: false };
  }

  // Expanding one rail must not record a permanent collapse of its neighbor.
  if (prefer) {
    return {
      palette: prefer === "palette",
      inspector: prefer === "inspector",
      forced: true,
    };
  }

  // No preference yet -- opening the studio narrow shows the graph, and the
  // rails are there to ask for the rest.
  if (fits(rowWidth, false, true, paletteW, inspectorW)) {
    return { palette: false, inspector: true, forced: true };
  }
  return { palette: false, inspector: false, forced: true };
}
