import { describe, expect, it } from "vitest";
import { clampStudioPanelWidth, fitPanels, RAIL_W } from "./studioLayout";
import { INSPECTOR, MIN_CANVAS, PALETTE } from "./panelSizing";

const base = {
  paletteW: PALETTE.default, // 280
  inspectorW: INSPECTOR.default, // 320
  paletteWanted: true,
  inspectorWanted: true,
};

describe("fitPanels", () => {
  it("keeps both panels open when the row can hold them", () => {
    expect(fitPanels({ ...base, rowWidth: 1400 })).toEqual({
      palette: true,
      inspector: true,
      forced: false,
    });
    expect(fitPanels({ ...base, rowWidth: 932 }).palette).toBe(true);
    expect(fitPanels({ ...base, rowWidth: 931 }).palette).toBe(false);
  });

  it("drops the palette first, and keeps the chat rail", () => {
    // The open inspector also occupies a six-pixel resize handle.
    const fit = fitPanels({ ...base, rowWidth: 919 });
    expect(fit).toEqual({ palette: false, inspector: true, forced: true });
  });

  it("collapses both only when the inspector alone no longer fits", () => {
    expect(fitPanels({ ...base, rowWidth: 672 }).inspector).toBe(true);
    expect(fitPanels({ ...base, rowWidth: 671 })).toEqual({
      palette: false,
      inspector: false,
      forced: true,
    });
  });

  // The promise the rails make: at any width there is still something to
  // click. Two rails plus MIN_CANVAS is the floor, and it is well under any
  // window a pointer client is dragged to.
  it("needs only two rails and MIN_CANVAS at its narrowest", () => {
    expect(RAIL_W * 2 + MIN_CANVAS).toBe(372);
    const fit = fitPanels({ ...base, rowWidth: 372 });
    expect(fit.palette).toBe(false);
    expect(fit.inspector).toBe(false);
  });

  it("respects a panel the reader collapsed even when there is room", () => {
    const fit = fitPanels({
      ...base,
      rowWidth: 1400,
      paletteWanted: false,
    });
    expect(fit).toEqual({ palette: false, inspector: true, forced: false });
  });

  // A reader who collapsed the palette themselves frees the room the
  // inspector needs, so the inspector must not also be taken away.
  it("does not force the inspector shut when the palette is already collapsed", () => {
    const fit = fitPanels({
      ...base,
      rowWidth: 700,
      paletteWanted: false,
    });
    expect(fit.inspector).toBe(true);
    expect(fit.forced).toBe(false);
  });

  it("trusts the reader's choice before the row has been measured", () => {
    for (const rowWidth of [0, NaN, -1]) {
      expect(fitPanels({ ...base, rowWidth })).toEqual({
        palette: true,
        inspector: true,
        forced: false,
      });
    }
  });

  it("accounts for the panels' actual widths, not their defaults", () => {
    // A palette dragged out to its max needs a wider row before it fits.
    const wide = { ...base, paletteW: PALETTE.max, inspectorW: INSPECTOR.max };
    expect(fitPanels({ ...wide, rowWidth: 1431 }).palette).toBe(false);
    expect(fitPanels({ ...wide, rowWidth: 1432 }).palette).toBe(true);
  });
});

describe("studio resize clearance", () => {
  it("leaves a 320px canvas beside a collapsed rail at 700px", () => {
    expect(clampStudioPanelWidth(500, PALETTE, 700, RAIL_W, false)).toBe(348);
    expect(clampStudioPanelWidth(500, INSPECTOR, 700, RAIL_W, false)).toBe(348);
  });

  it("reserves both resize handles when both panels are visible", () => {
    expect(clampStudioPanelWidth(460, PALETTE, 960, 320, true)).toBe(308);
  });

  it("keeps unmeasured rows bounded by the panel's own limits", () => {
    expect(clampStudioPanelWidth(500, PALETTE, 0, RAIL_W, false)).toBe(460);
  });
});

describe("fitPanels with one side already closed", () => {
  const narrow = {
    rowWidth: 520,
    paletteW: PALETTE.min,
    inspectorW: INSPECTOR.min,
  };

  it("opens the palette at a width where both could never fit", () => {
    // 200 + 26 + 320 = 546, wider than the row -- and it opens anyway.
    expect(
      fitPanels({ ...narrow, paletteWanted: true, inspectorWanted: false }),
    ).toEqual({ palette: true, inspector: false, forced: false });
  });

  it("opens the chat rail on the same terms", () => {
    expect(
      fitPanels({ ...narrow, paletteWanted: false, inspectorWanted: true }),
    ).toEqual({ palette: false, inspector: true, forced: false });
  });

  it("still closes both when both are wanted and neither fits", () => {
    expect(
      fitPanels({ ...narrow, paletteWanted: true, inspectorWanted: true }),
    ).toEqual({ palette: false, inspector: false, forced: true });
  });

  it("leaves both closed when the reader closed both", () => {
    expect(
      fitPanels({ ...narrow, paletteWanted: false, inspectorWanted: false }),
    ).toEqual({ palette: false, inspector: false, forced: false });
  });
});

// From code review: expanding a rail used to mark the OTHER panel collapsed,
// which is reader-intent state -- so widening the window afterwards left it a
// rail forever, though nobody had asked to close it. It is a preference now,
// consulted only while both cannot fit.
describe("prefer", () => {
  const narrow = { ...base, rowWidth: 520 };

  it("collapses both when the reader has not asked for either", () => {
    expect(fitPanels(narrow)).toEqual({
      palette: false,
      inspector: false,
      forced: true,
    });
  });

  // The regression this replaced: at 520px neither panel fits beside the
  // other's rail plus MIN_CANVAS, so clicking a rail did nothing at all.
  it("opens the asked-for panel at a width where neither would otherwise fit", () => {
    expect(fitPanels({ ...narrow, prefer: "palette" })).toEqual({
      palette: true,
      inspector: false,
      forced: true,
    });
    expect(fitPanels({ ...narrow, prefer: "inspector" })).toEqual({
      palette: false,
      inspector: true,
      forced: true,
    });
  });

  it("is ignored once both fit, so widening restores the other side", () => {
    for (const prefer of ["palette", "inspector"] as const) {
      expect(fitPanels({ ...base, rowWidth: 1400, prefer })).toEqual({
        palette: true,
        inspector: true,
        forced: false,
      });
    }
  });

  it("still drops only the palette at a width that fits the chat rail alone", () => {
    expect(fitPanels({ ...base, rowWidth: 919 })).toEqual({
      palette: false,
      inspector: true,
      forced: true,
    });
  });
});
