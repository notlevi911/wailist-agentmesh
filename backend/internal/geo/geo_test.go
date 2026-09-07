package geo

import (
	"math"
	"testing"
)

// Real coordinates with independently known distances, so a broken formula
// fails against the world rather than against another copy of itself.
func TestDistanceM(t *testing.T) {
	cases := []struct {
		name                   string
		lat1, lng1, lat2, lng2 float64
		wantM                  float64
		tolM                   float64
	}{
		{"identical point is zero", 51.5007, -0.1246, 51.5007, -0.1246, 0, 0.001},
		// Big Ben -> Buckingham Palace, ~1.3 km.
		{"short city hop", 51.5007, -0.1246, 51.5014, -0.1419, 1200, 150},
		// London -> Paris, ~344 km.
		{"intercity", 51.5007, -0.1246, 48.8566, 2.3522, 343900, 2000},
		// A degree of latitude is ~111 km anywhere on the globe.
		{"one degree of latitude", 0, 0, 1, 0, 111195, 100},
		// Longitude degrees shrink with the cosine of latitude: at 60N a
		// degree is half what it is at the equator. A flat x/y approximation
		// gets this wrong, which is the bug this case exists to catch.
		{"one degree of longitude at 60N", 60, 0, 60, 1, 55597, 100},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DistanceM(c.lat1, c.lng1, c.lat2, c.lng2)
			if math.Abs(got-c.wantM) > c.tolM {
				t.Fatalf("DistanceM = %.1f m, want %.1f +/- %.1f", got, c.wantM, c.tolM)
			}
		})
	}
}

func TestDistanceIsSymmetric(t *testing.T) {
	a := DistanceM(51.5007, -0.1246, 48.8566, 2.3522)
	b := DistanceM(48.8566, 2.3522, 51.5007, -0.1246)
	if math.Abs(a-b) > 0.001 {
		t.Fatalf("not symmetric: %.4f vs %.4f", a, b)
	}
}

func TestInside(t *testing.T) {
	// 100 m radius around Big Ben.
	const cLat, cLng, r = 51.5007, -0.1246, 100.0

	if !Inside(cLat, cLng, r, cLat, cLng) {
		t.Error("the centre must be inside its own circle")
	}
	// ~55 m north.
	if !Inside(cLat, cLng, r, 51.5012, -0.1246) {
		t.Error("a point well within the radius must be inside")
	}
	// ~1.3 km away.
	if Inside(cLat, cLng, r, 51.5014, -0.1419) {
		t.Error("a point far outside the radius must be outside")
	}
}

// The boundary is documented as inside; pin it so a later refactor cannot
// flip it silently. A point exactly r away must count, one just beyond must not.
func TestInsideBoundaryCountsAsInside(t *testing.T) {
	const cLat, cLng = 51.5007, -0.1246
	// Walk north until we find the offset that is ~100 m out, then test either
	// side of it using the distance function itself as the reference.
	target := 51.5007 + (100.0 / 111195.0) // ~100 m north
	d := DistanceM(cLat, cLng, target, cLng)

	if !Inside(cLat, cLng, d, target, cLng) {
		t.Error("radius exactly equal to the distance must count as inside")
	}
	if Inside(cLat, cLng, d-0.01, target, cLng) {
		t.Error("a radius just short of the distance must be outside")
	}
}

func TestValidCoord(t *testing.T) {
	valid := [][2]float64{{0, 0}, {90, 180}, {-90, -180}, {51.5, -0.12}}
	for _, c := range valid {
		if !ValidCoord(c[0], c[1]) {
			t.Errorf("ValidCoord(%v, %v) = false, want true", c[0], c[1])
		}
	}
	invalid := [][2]float64{
		{91, 0}, {-91, 0}, {0, 181}, {0, -181},
		{math.NaN(), 0}, {0, math.NaN()},
		{math.Inf(1), 0}, {0, math.Inf(-1)},
	}
	for _, c := range invalid {
		if ValidCoord(c[0], c[1]) {
			t.Errorf("ValidCoord(%v, %v) = true, want false", c[0], c[1])
		}
	}
}
