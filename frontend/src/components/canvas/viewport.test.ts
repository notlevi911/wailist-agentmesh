import { describe, it, expect } from "vitest";
import {
  screenToWorld,
  DEFAULT_VIEW,
  MIN_SCALE,
  MAX_SCALE,
  clampScale,
  zoomAbout,
  zoomByFactor,
  distance,
  midpoint,
  fitToRects,
  type ViewState,
} from "./viewport";

// Where a world point lands on screen under a view -- the invariant every
// zoom assertion below is really about.
const project = (v: ViewState, wx: number, wy: number) => ({
  x: wx * v.k + v.x,
  y: wy * v.k + v.y,
});

describe("clampScale", () => {
  it("holds the zoom inside its range", () => {
    expect(clampScale(0.1)).toBe(MIN_SCALE);
    expect(clampScale(9)).toBe(MAX_SCALE);
    expect(clampScale(1.25)).toBe(1.25);
  });

  it("falls back to the default rather than propagating NaN", () => {
    expect(clampScale(NaN)).toBe(DEFAULT_VIEW.k);
    expect(clampScale(Infinity)).toBe(MAX_SCALE);
  });
});

describe("zoomAbout", () => {
  // The whole point of the anchor: the pixel under the cursor or the pinch
  // midpoint must not move. If it does, the graph slides out from under you.
  it("pins the anchor point on screen", () => {
    const v: ViewState = { x: 40, y: 40, k: 1 };
    const [px, py] = [200, 150];
    // The world point currently under the anchor.
    const wx = (px - v.x) / v.k;
    const wy = (py - v.y) / v.k;

    for (const k of [0.5, 1.4, 2]) {
      const next = zoomAbout(v, k, px, py);
      const after = project(next, wx, wy);
      expect(after.x).toBeCloseTo(px, 6);
      expect(after.y).toBeCloseTo(py, 6);
    }
  });

  it("clamps without letting the anchor drift", () => {
    const v: ViewState = { x: 10, y: 20, k: 1 };
    const next = zoomAbout(v, 50, 100, 100);
    expect(next.k).toBe(MAX_SCALE);
    const wx = (100 - v.x) / v.k;
    const wy = (100 - v.y) / v.k;
    const after = project(next, wx, wy);
    expect(after.x).toBeCloseTo(100, 6);
    expect(after.y).toBeCloseTo(100, 6);
  });

  it("zoomByFactor scales relative to the current zoom", () => {
    const v: ViewState = { x: 0, y: 0, k: 1 };
    expect(zoomByFactor(v, 1.15, 0, 0).k).toBeCloseTo(1.15, 6);
    expect(zoomByFactor({ ...v, k: 0.4 }, 0.5, 0, 0).k).toBe(MIN_SCALE);
  });
});

describe("screenToWorld", () => {
  // The property that matters: it must undo `project` exactly, or a node
  // lands somewhere other than where it was dropped.
  it("is the inverse of the projection", () => {
    for (const v of [
      { x: 40, y: 40, k: 1 },
      { x: -318, y: 96, k: 0.55 },
      { x: 12, y: -400, k: 1.9 },
    ]) {
      for (const [wx, wy] of [
        [0, 0],
        [420, 260],
        [-800, -600],
      ]) {
        const s = project(v, wx, wy);
        const back = screenToWorld(v, s.x, s.y);
        expect(back.x).toBeCloseTo(wx, 6);
        expect(back.y).toBeCloseTo(wy, 6);
      }
    }
  });

  it("maps the view origin to the world origin", () => {
    const v = { x: 40, y: 40, k: 0.95 };
    expect(screenToWorld(v, 40, 40)).toEqual({ x: 0, y: 0 });
  });
});

describe("pinch geometry", () => {
  it("measures distance and midpoint between two touches", () => {
    expect(distance({ x: 0, y: 0 }, { x: 3, y: 4 })).toBe(5);
    expect(midpoint({ x: 0, y: 0 }, { x: 10, y: 20 })).toEqual({ x: 5, y: 10 });
  });
});

describe("fitToRects", () => {
  const NODE = { w: 180, h: 60 };

  it("centres a single node without zooming past 1", () => {
    const v = fitToRects([{ x: 500, y: 400, ...NODE }], 800, 600);
    // A lone small node must not be blown up to fill the screen.
    expect(v.k).toBe(1);
    const centre = project(v, 500 + NODE.w / 2, 400 + NODE.h / 2);
    expect(centre.x).toBeCloseTo(400, 6);
    expect(centre.y).toBeCloseTo(300, 6);
  });

  it("shrinks a graph until it fits, and centres it", () => {
    const rects = [
      { x: 0, y: 0, ...NODE },
      { x: 400, y: 300, ...NODE },
    ];
    const v = fitToRects(rects, 375, 700, 24);
    expect(v.k).toBeLessThan(1);
    expect(v.k).toBeGreaterThan(MIN_SCALE);

    // Every corner of the content lands inside the viewport.
    const tl = project(v, 0, 0);
    const br = project(v, 400 + NODE.w, 300 + NODE.h);
    expect(tl.x).toBeGreaterThanOrEqual(0);
    expect(tl.y).toBeGreaterThanOrEqual(0);
    expect(br.x).toBeLessThanOrEqual(375);
    expect(br.y).toBeLessThanOrEqual(700);
  });

  // A graph too big to fit even at MIN_SCALE must not zoom out past it --
  // the nodes would be unreadable. It centres and overflows instead, so the
  // viewer starts in the middle of the graph rather than at a far corner.
  it("stops at MIN_SCALE and centres what it cannot fit", () => {
    const rects = [
      { x: 0, y: 0, ...NODE },
      { x: 2000, y: 900, ...NODE },
    ];
    const v = fitToRects(rects, 375, 700, 24);
    expect(v.k).toBe(MIN_SCALE);

    const tl = project(v, 0, 0);
    const br = project(v, 2000 + NODE.w, 900 + NODE.h);
    expect((tl.x + br.x) / 2).toBeCloseTo(375 / 2, 6);
    expect((tl.y + br.y) / 2).toBeCloseTo(700 / 2, 6);
  });

  it("handles negative coordinates", () => {
    const v = fitToRects([{ x: -800, y: -600, ...NODE }], 400, 400);
    const centre = project(v, -800 + NODE.w / 2, -600 + NODE.h / 2);
    expect(centre.x).toBeCloseTo(200, 6);
    expect(centre.y).toBeCloseTo(200, 6);
  });

  it("falls back to the default view when there is nothing to measure", () => {
    expect(fitToRects([], 800, 600)).toEqual(DEFAULT_VIEW);
    expect(fitToRects([{ x: 0, y: 0, ...NODE }], 0, 0)).toEqual(DEFAULT_VIEW);
  });

  // A viewport narrower than its own padding budget must still produce a
  // usable view rather than a zero or negative scale.
  it("survives a viewport smaller than its padding", () => {
    const v = fitToRects([{ x: 0, y: 0, ...NODE }], 40, 40, 48);
    expect(v.k).toBeGreaterThanOrEqual(MIN_SCALE);
    expect(Number.isFinite(v.x)).toBe(true);
    expect(Number.isFinite(v.y)).toBe(true);
  });
});
