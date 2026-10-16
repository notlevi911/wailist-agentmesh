import { describe, it, expect } from "vitest";
import {
  MIN_RADIUS_M,
  MAX_RADIUS_M,
  accuracyTooVague,
  checkRadius,
  distanceM,
  formatCoords,
  formatDistance,
  isInside,
  radiusToSlider,
  sliderToRadius,
} from "./geofence";

describe("checkRadius", () => {
  // The bounds these mirror live in backend/internal/api/handlers/geofence.go.
  // If the server ever moves them, these numbers are what should fail first.
  it("accepts the server's bounds exactly", () => {
    expect(checkRadius(MIN_RADIUS_M)).toEqual({ ok: true, radiusM: 50 });
    expect(checkRadius(MAX_RADIUS_M)).toEqual({ ok: true, radiusM: 100_000 });
  });

  it("rejects one metre outside either bound", () => {
    expect(checkRadius(MIN_RADIUS_M - 1).ok).toBe(false);
    expect(checkRadius(MAX_RADIUS_M + 1).ok).toBe(false);
  });

  it("explains itself rather than just refusing", () => {
    const tooSmall = checkRadius(10);
    expect(tooSmall.ok).toBe(false);
    // The reason matters more than the refusal: a bare "invalid" invites the
    // user to try 20, then 30, then give up.
    if (!tooSmall.ok) expect(tooSmall.message).toMatch(/50 m/);
  });

  it("rounds to whole metres, since the server stores no more than that", () => {
    expect(checkRadius(120.4)).toEqual({ ok: true, radiusM: 120 });
    // Rounding must not smuggle a value past the floor: 49.6 rounds to 50 and
    // is legitimately in range, but 49.4 must still be refused.
    expect(checkRadius(49.6)).toEqual({ ok: true, radiusM: 50 });
    expect(checkRadius(49.4).ok).toBe(false);
  });

  it("refuses NaN rather than passing it to the server", () => {
    expect(checkRadius(Number.NaN).ok).toBe(false);
    expect(checkRadius(Number.POSITIVE_INFINITY).ok).toBe(false);
  });
});

describe("slider mapping", () => {
  it("pins both ends to the bounds", () => {
    expect(sliderToRadius(0)).toBe(MIN_RADIUS_M);
    expect(sliderToRadius(1)).toBe(MAX_RADIUS_M);
  });

  it("clamps a position outside the track instead of extrapolating", () => {
    expect(sliderToRadius(-0.5)).toBe(MIN_RADIUS_M);
    expect(sliderToRadius(2)).toBe(MAX_RADIUS_M);
  });

  it("round-trips a radius back to its own position", () => {
    for (const r of [50, 120, 500, 2_000, 25_000, 100_000]) {
      expect(sliderToRadius(radiusToSlider(r))).toBe(r);
    }
  });

  // The reason the mapping is logarithmic at all. Linear would put every
  // radius below a kilometre inside the first 1% of the track.
  it("gives small zones a usable share of the track", () => {
    // Half the travel should still be well under the maximum -- geometric
    // midpoint of 50 and 100,000 is ~2.2 km, not 50 km.
    const halfway = sliderToRadius(0.5);
    expect(halfway).toBeGreaterThan(1_500);
    expect(halfway).toBeLessThan(3_500);
  });
});

describe("distanceM", () => {
  it("is zero for a point against itself", () => {
    expect(
      distanceM({ lat: 51.5, lng: -0.12 }, { lat: 51.5, lng: -0.12 }),
    ).toBe(0);
  });

  // A degree of latitude is ~111 km everywhere, which makes this the one
  // check that needs no reference table to believe.
  it("measures a degree of latitude at about 111 km", () => {
    const d = distanceM({ lat: 0, lng: 0 }, { lat: 1, lng: 0 });
    expect(d).toBeGreaterThan(110_000);
    expect(d).toBeLessThan(112_000);
  });

  it("shortens a degree of longitude as latitude rises", () => {
    const atEquator = distanceM({ lat: 0, lng: 0 }, { lat: 0, lng: 1 });
    const atSixty = distanceM({ lat: 60, lng: 0 }, { lat: 60, lng: 1 });
    // cos(60°) = 0.5, so the higher one should be about half.
    expect(atSixty / atEquator).toBeGreaterThan(0.45);
    expect(atSixty / atEquator).toBeLessThan(0.55);
  });

  it("crosses the antimeridian the short way", () => {
    // 179.9E to 179.9W is 0.2 degrees apart, not 359.8.
    const d = distanceM({ lat: 0, lng: 179.9 }, { lat: 0, lng: -179.9 });
    expect(d).toBeLessThan(25_000);
  });
});

describe("isInside", () => {
  const centre = { lat: 51.5007, lng: -0.1246 };

  it("includes the boundary itself", () => {
    // A point exactly on the edge counts as inside, matching <= in the source.
    expect(isInside(centre, 0, centre)).toBe(true);
  });

  it("separates a point just inside from one just outside", () => {
    // ~111 m north.
    const north = { lat: centre.lat + 0.001, lng: centre.lng };
    expect(isInside(centre, 200, north)).toBe(true);
    expect(isInside(centre, 50, north)).toBe(false);
  });
});

describe("formatDistance", () => {
  it("uses metres below a kilometre", () => {
    expect(formatDistance(0)).toBe("0 m");
    expect(formatDistance(50)).toBe("50 m");
    expect(formatDistance(999)).toBe("999 m");
  });

  it("drops to one decimal of a kilometre, then to none", () => {
    expect(formatDistance(1_000)).toBe("1.0 km");
    expect(formatDistance(1_249)).toBe("1.2 km");
    expect(formatDistance(9_999)).toBe("10.0 km");
    expect(formatDistance(10_000)).toBe("10 km");
    expect(formatDistance(100_000)).toBe("100 km");
  });

  it("does not render a reading it has not got", () => {
    expect(formatDistance(Number.NaN)).toBe("-");
  });
});

describe("formatCoords", () => {
  it("fixes to five places, which is about a metre", () => {
    expect(formatCoords({ lat: 51.500729, lng: -0.124621 })).toBe(
      "51.50073, -0.12462",
    );
  });

  it("keeps the sign and both axes when either is negative", () => {
    expect(formatCoords({ lat: -33.8688, lng: 151.2093 })).toBe(
      "-33.86880, 151.20930",
    );
  });
});

describe("accuracyTooVague", () => {
  it("accepts a fix tighter than the zone, and the exact boundary", () => {
    expect(accuracyTooVague(10, 150)).toBe(false);
    // Equal is allowed: the error circle is the zone, not wider than it.
    expect(accuracyTooVague(150, 150)).toBe(false);
  });

  it("rejects a fix one metre wider than the zone", () => {
    expect(accuracyTooVague(151, 150)).toBe(true);
  });

  it("rejects the indoor case this exists for", () => {
    // A fix off cell towers rather than GPS, against the smallest zone the
    // radius floor permits.
    expect(accuracyTooVague(2_000, MIN_RADIUS_M)).toBe(true);
  });

  it("does not treat an unreported accuracy as a bad fix", () => {
    // coords.accuracy is required by the spec but not every browser is honest
    // about it. Absence is not evidence, and must not block a save.
    expect(accuracyTooVague(null, MIN_RADIUS_M)).toBe(false);
    expect(accuracyTooVague(Number.NaN, MIN_RADIUS_M)).toBe(false);
  });

  it("rejects an infinite accuracy rather than letting it through", () => {
    // Infinity is not finite, so the null-ish guard would swallow it if the
    // check were written as a bare !Number.isFinite bail-out. It is a real
    // claim of total uncertainty and must fail.
    expect(accuracyTooVague(Number.POSITIVE_INFINITY, MAX_RADIUS_M)).toBe(true);
  });
});
