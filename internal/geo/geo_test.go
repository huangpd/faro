package geo

import (
	"math"
	"testing"
	"time"
)

func TestLandIsWhereLandIs(t *testing.T) {
	if len(Land) < 5000 {
		t.Errorf("%d land cells", len(Land))
	}
	has := func(lat, lon, within float64) bool {
		for _, p := range Land {
			if math.Abs(p.Lat-lat) <= within && math.Abs(p.Lon-lon) <= within {
				return true
			}
		}
		return false
	}
	if !has(51.5, 0, 3) || !has(31, 121, 3) || !has(39, -98, 3) {
		t.Error("London, Shanghai and the middle of the US should be land")
	}
	if has(0, -150, 8) || has(-45, -120, 8) {
		t.Error("the Pacific should not be land")
	}
	for _, p := range Land {
		if p.Lat < -90 || p.Lat > 90 || p.Lon < -180 || p.Lon > 180 {
			t.Fatalf("cell %v is not on the Earth", p)
		}
	}
}

func TestPlaces(t *testing.T) {
	for _, id := range []string{"HK", "uk", "DE", "SG", "US", "JP"} {
		if _, ok := Place(id); !ok {
			t.Errorf("no place for %s", id)
		}
	}
	if _, ok := Place("ZZ"); ok {
		t.Error("a place for ZZ")
	}
}

func TestFrameShowsBothPlacesAndGoesTheShortWayRound(t *testing.T) {
	const aspect = 2.0
	check := func(name string, a, b Point) Viewport {
		t.Helper()
		v := Frame(a, b, aspect, 40)
		for _, p := range []Point{a, b} {
			x, y := v.XY(p)
			if x < 0.05 || x > 0.95 || y < 0.05 || y > 0.95 {
				t.Errorf("%s: %v is at %.2f,%.2f, near or past the edge of %+v", name, p, x, y, v)
			}
		}
		if math.Abs(v.SpanLon/v.SpanLat-aspect) > 0.01 {
			t.Errorf("%s: the shape is %v", name, v.SpanLon/v.SpanLat)
		}
		return v
	}
	hk, _ := Place("HK")
	uk, _ := Place("UK")
	us, _ := Place("US")
	sg, _ := Place("SG")
	check("hk-uk", hk, uk)
	check("sg-hk", sg, hk)
	// Across the Pacific, not the long way over Asia and Europe and the Atlantic.
	v := check("hk-us", hk, us)
	if v.Lon < 150 && v.Lon > -150 {
		t.Errorf("the middle of a route over the Pacific is at %v", v.Lon)
	}
	// Two near places are not blown up, and the same place is shown.
	near := Frame(hk, sg, aspect, 40)
	if near.SpanLon < 40 {
		t.Errorf("span %v", near.SpanLon)
	}
	same := check("same", hk, hk)
	if same.SpanLon < 40 {
		t.Errorf("span %v", same.SpanLon)
	}
	// Never past the poles, however far north the places are.
	north := Frame(Point{Lat: 70, Lon: 20}, Point{Lat: 69, Lon: 25}, aspect, 40)
	if north.Lat+north.SpanLat/2 > 90 || north.Lat-north.SpanLat/2 < -90 {
		t.Errorf("north %+v", north)
	}
}

func TestXYUnwrapsLongitude(t *testing.T) {
	v := Viewport{Lon: 180, Lat: 0, SpanLon: 60, SpanLat: 30}
	x1, _ := v.XY(Point{0, 170})
	x2, _ := v.XY(Point{0, -170}) // 190 degrees east: the other side of the date line
	if math.Abs(x1-(0.5-10.0/60)) > 1e-9 || math.Abs(x2-(0.5+10.0/60)) > 1e-9 {
		t.Errorf("x %v %v", x1, x2)
	}
	if x, y := v.XY(Point{0, 180}); x != 0.5 || y != 0.5 {
		t.Errorf("the middle is at %v,%v", x, y)
	}
}

func TestHereKnowsAZoneAndGuessesTheRest(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if p, _ := ZoneOf("Asia/Shanghai", now); math.Abs(p.Lon-121.5) > 0.1 {
		t.Errorf("shanghai %v", p)
	}
	loc := time.FixedZone("x", 8*3600)
	if p, _ := ZoneOf("Nowhere/Land", now.In(loc)); math.Abs(p.Lon-120) > 0.1 {
		t.Errorf("an unknown zone is placed by its offset: %v", p)
	}
	if h := Here(); h.Lat < -90 || h.Lat > 90 {
		t.Errorf("here %v", h)
	}
}
