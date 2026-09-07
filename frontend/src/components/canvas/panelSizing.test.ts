import { describe, it, expect } from "vitest";
import { clampWidth, PALETTE, INSPECTOR, MIN_CANVAS } from "./panelSizing";

const WIDE = 1600; // plenty of room -- container constraint never binds

describe("clampWidth", () => {
  it("clamps below the minimum up to min", () => {
    expect(clampWidth(50, PALETTE, WIDE, INSPECTOR.default)).toBe(PALETTE.min);
  });

  it("clamps above the maximum down to max", () => {
    expect(clampWidth(9999, PALETTE, WIDE, INSPECTOR.default)).toBe(
      PALETTE.max,
    );
  });

  // Pins the rail's upper bound: it is a terminal column budget now, not just
  // a layout preference (see the comment on INSPECTOR).
  it("clamps the inspector above its maximum down to max", () => {
    expect(clampWidth(9999, INSPECTOR, WIDE, PALETTE.default)).toBe(
      INSPECTOR.max,
    );
  });

  it("passes a valid width through unchanged in a wide container", () => {
    expect(clampWidth(320, PALETTE, WIDE, INSPECTOR.default)).toBe(320);
  });

  it("reserves MIN_CANVAS + the other panel in a tight container", () => {
    // container 900, inspector 320 → palette may take at most 900-320-320 = 260
    const w = clampWidth(460, PALETTE, 900, 320);
    expect(w).toBe(260);
    expect(900 - w - 320).toBeGreaterThanOrEqual(MIN_CANVAS);
  });

  it("falls back to min when there is no room even for min", () => {
    // container 700, inspector 320 → 700-320-320 = 60 < palette.min(200)
    expect(clampWidth(300, PALETTE, 700, 320)).toBe(PALETTE.min);
  });

  it("ignores the container constraint when width is unmeasured (0)", () => {
    expect(clampWidth(400, INSPECTOR, 0, 280)).toBe(400);
  });

  it("ignores the container constraint when width is NaN", () => {
    expect(clampWidth(400, INSPECTOR, NaN, 280)).toBe(400);
  });

  it("falls back to default for a non-finite requested width", () => {
    expect(clampWidth(NaN, INSPECTOR, WIDE, 280)).toBe(INSPECTOR.default);
  });

  it("returns an integer", () => {
    expect(Number.isInteger(clampWidth(300.7, PALETTE, WIDE, 320))).toBe(true);
  });
});
