// The arithmetic behind the geofence screen, kept away from the DOM.
//
// Same shape as device.ts and panelSizing.ts: pure functions over plain
// numbers, so every edge (a radius one metre under the floor, a slider at rest
// on its minimum, a pole crossing) is testable without rendering anything.

// Mirrors minGeofenceRadiusM / maxGeofenceRadiusM in
// backend/internal/api/handlers/geofence.go. Duplicated rather than fetched:
// the screen has to reject a bad radius before it sends, and a bound that
// arrives over the network cannot do that.
//
// The floor is not arbitrary. GPS jitters by tens of metres indoors, so a
// smaller circle flaps -- enter, leave, enter -- and each flap is a workflow
// run the user did not ask for and is charged for.
export const MIN_RADIUS_M = 50;
export const MAX_RADIUS_M = 100_000;

export interface Point {
  lat: number;
  lng: number;
}

export type RadiusCheck =
  { ok: true; radiusM: number } | { ok: false; message: string };

/**
 * Validates a radius against the server's bounds, returning the message the
 * user should see rather than a boolean.
 *
 * Rejecting here is not a substitute for the server's own check -- it is so
 * the answer arrives instantly and in the user's words, instead of as a 400
 * carrying the API's phrasing.
 */
export function checkRadius(radiusM: number): RadiusCheck {
  if (!Number.isFinite(radiusM)) {
    return { ok: false, message: "Enter a radius." };
  }
  const rounded = Math.round(radiusM);
  if (rounded < MIN_RADIUS_M) {
    return {
      ok: false,
      message: `A zone must be at least ${MIN_RADIUS_M} m across its radius: anything tighter flickers as GPS drifts, and every flicker starts a run.`,
    };
  }
  if (rounded > MAX_RADIUS_M) {
    return {
      ok: false,
      message: `A zone can reach ${formatDistance(MAX_RADIUS_M)} at most.`,
    };
  }
  return { ok: true, radiusM: rounded };
}

// A slider position (0..1) mapped to metres logarithmically.
//
// Linear would make the control useless: 50 m and 500 m -- the difference
// between a building and a neighbourhood, and the range nearly every fence
// lives in -- would share the first half-percent of the track, while 90% of
// the travel argued about tens of kilometres nobody picks. Log spacing gives
// each order of magnitude the same length of track.
const LOG_MIN = Math.log(MIN_RADIUS_M);
const LOG_MAX = Math.log(MAX_RADIUS_M);

export function sliderToRadius(position: number): number {
  const clamped = Math.min(1, Math.max(0, position));
  return Math.round(Math.exp(LOG_MIN + (LOG_MAX - LOG_MIN) * clamped));
}

export function radiusToSlider(radiusM: number): number {
  const clamped = Math.min(MAX_RADIUS_M, Math.max(MIN_RADIUS_M, radiusM));
  return (Math.log(clamped) - LOG_MIN) / (LOG_MAX - LOG_MIN);
}

/**
 * Distance in metres between two points on the earth.
 *
 * Haversine on a sphere, not an ellipsoid: at the scale of a geofence the
 * difference from the true geodesic is a few metres in a hundred kilometres,
 * which is far inside the GPS error the floor above already accounts for.
 */
export function distanceM(a: Point, b: Point): number {
  const R = 6_371_000;
  const toRad = (d: number) => (d * Math.PI) / 180;
  const dLat = toRad(b.lat - a.lat);
  const dLng = toRad(b.lng - a.lng);
  const lat1 = toRad(a.lat);
  const lat2 = toRad(b.lat);
  const h =
    Math.sin(dLat / 2) ** 2 +
    Math.cos(lat1) * Math.cos(lat2) * Math.sin(dLng / 2) ** 2;
  return 2 * R * Math.asin(Math.min(1, Math.sqrt(h)));
}

/**
 * Metres as a person would say them.
 *
 * Precision falls away as the number grows, because it stops meaning anything:
 * "1.2 km" is a useful answer and "1,247 m" is false precision on a reading
 * whose own error is tens of metres.
 */
export function formatDistance(m: number): string {
  if (!Number.isFinite(m)) return "-";
  const abs = Math.abs(m);
  if (abs < 1_000) return `${Math.round(m)} m`;
  if (abs < 10_000) return `${(m / 1_000).toFixed(1)} km`;
  return `${Math.round(m / 1_000)} km`;
}

/** Coordinates, fixed to roughly a metre. More digits is noise. */
export function formatCoords(p: Point): string {
  return `${p.lat.toFixed(5)}, ${p.lng.toFixed(5)}`;
}

/**
 * Whether a point sits inside a zone.
 *
 * Only ever used to describe the CURRENT reading to the user. The crossing
 * that actually starts a run is decided by Android's GeofencingClient on the
 * device and by RecordGeofenceFix on the server -- this must never become a
 * third opinion that any of them acts on.
 */
export function isInside(centre: Point, radiusM: number, at: Point): boolean {
  return distanceM(centre, at) <= radiusM;
}

/**
 * Whether a position fix is too vague to centre a zone of this size on.
 *
 * A fix carries its own error bar: `coords.accuracy` is metres of radius at
 * 68% confidence, and indoors or off cell towers it reaches hundreds or
 * thousands. When that circle is wider than the zone, the true position can
 * sit entirely outside the zone the user believes they drew, and MIN_RADIUS_M
 * is nowhere near large enough to absorb it.
 *
 * This is a predicate rather than a warning string because it decides whether
 * a save is allowed: a misplaced zone does not fail visibly, it fires at the
 * wrong place, and every firing costs a run. An unknown accuracy (null) is not
 * treated as bad -- a browser that declines to report one is not evidence of a
 * poor fix.
 */
// A type predicate, not a plain boolean: returning true necessarily means the
// accuracy was a real number, and callers all go on to format it into the
// message they show. Without this every call site needs a second null check
// that can never fire.
export function accuracyTooVague(
  accuracyM: number | null,
  radiusM: number,
): accuracyM is number {
  // null and NaN mean "not reported", which is not evidence of a poor fix.
  // Infinity is the opposite: a claim of unbounded error, and it must fail --
  // so this cannot be written as a single !Number.isFinite bail-out.
  if (accuracyM === null || Number.isNaN(accuracyM)) return false;
  return accuracyM > radiusM;
}
