// Package geo holds the geofence maths, kept pure and dependency-free so the
// enter/leave decision can be tested without a database, an HTTP request, or
// a clock. The handler layer decides what to DO about a crossing; this decides
// only whether a point is inside a circle, which is the part worth pinning
// with real coordinates.
package geo

import "math"

// Mean Earth radius (IUGG). A sphere is the right model here: geofences in
// this product are tens to thousands of metres, where the difference between
// spherical and ellipsoidal distance is far below GPS accuracy. Using a
// spheroid would add precision the input does not have.
const earthRadiusM = 6371008.8

// DistanceM returns the great-circle distance in metres between two points.
//
// Haversine rather than the spherical law of cosines: the latter loses
// precision catastrophically at small distances, which is the only distance
// range a geofence ever cares about.
func DistanceM(lat1, lng1, lat2, lng2 float64) float64 {
	p1 := lat1 * math.Pi / 180
	p2 := lat2 * math.Pi / 180
	dp := (lat2 - lat1) * math.Pi / 180
	dl := (lng2 - lng1) * math.Pi / 180

	a := math.Sin(dp/2)*math.Sin(dp/2) +
		math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * earthRadiusM * math.Asin(math.Min(1, math.Sqrt(a)))
}

// Inside reports whether (lat,lng) falls within radiusM of the centre.
//
// The boundary counts as inside. Which way the boundary falls is arbitrary,
// but it has to be decided once and stated, or an implementation detail
// decides it differently on each side of a refactor.
func Inside(centreLat, centreLng, radiusM, lat, lng float64) bool {
	return DistanceM(centreLat, centreLng, lat, lng) <= radiusM
}

// ValidCoord reports whether a latitude/longitude pair is on the globe.
// A silently-accepted out-of-range fix would place a geofence somewhere the
// user never goes and then never fire, which is a very quiet way to fail.
func ValidCoord(lat, lng float64) bool {
	if math.IsNaN(lat) || math.IsNaN(lng) || math.IsInf(lat, 0) || math.IsInf(lng, 0) {
		return false
	}
	return lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180
}
