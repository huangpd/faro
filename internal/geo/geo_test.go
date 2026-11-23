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

func TestTheWorldIsTurnedToTheOriginAtTheLeft(t *testing.T) {
	here, _ := Place("HK")
	w := WorldFrom(here, 0.14)
	x, _ := w.XY(here)
	if math.Abs(x-0.14) > 1e-9 {
		t.Errorf("the origin is at %v", x)
	}
	for _, p := range []Point{{0, -179.9}, {0, 179.9}, {60, 0}, {-30, 300 - 360}} {
		if x, y := w.XY(p); x < 0 || x >= 1 || y < 0 || y > 1 {
			t.Errorf("%v is off the map: %v,%v", p, x, y)
		}
	}
	uk, _ := Place("UK")
	us, _ := Place("US")
	jp, _ := Place("JP")
	if d := Way(here, uk); d > -0.3 || d < -0.36 { // west, over Asia and Europe: a third of the way round
		t.Errorf("hong kong to the UK is %v of the map", d)
	}
	if d := Way(here, us); d < 0.3 || d > 0.45 { // east, over the Pacific
		t.Errorf("hong kong to the US is %v of the map", d)
	}
	if Way(here, jp) <= 0 || Way(here, here) != 0 {
		t.Error("Japan is east; here is nowhere from here")
	}
	// The UK, west of the origin, is at the far right of the map.
	if x, _ := w.XY(uk); x < 0.7 {
		t.Errorf("the UK is at %v", x)
	}
	// The top of the map is the north, and the poles' edges are at the edges.
	if _, y := w.XY(Point{Top, 0}); y != 0 {
		t.Errorf("top %v", y)
	}
	if _, y := w.XY(Point{Bottom, 0}); y != 1 {
		t.Errorf("bottom %v", y)
	}
}

func TestCoarseLandKeepsTheShapeOfTheWorld(t *testing.T) {
	if len(Coarse) < 400 || len(Coarse) > 1500 {
		t.Errorf("%d coarse cells", len(Coarse))
	}
	has := func(lat, lon, within float64) bool {
		for _, p := range Coarse {
			if math.Abs(p.Lat-lat) <= within && math.Abs(p.Lon-lon) <= within {
				return true
			}
		}
		return false
	}
	if !has(51, 5, 5) || !has(35, 105, 5) || !has(-25, 135, 6) || !has(39, -98, 5) {
		t.Error("Europe, Asia, Australia and the US should be land")
	}
	if has(0, -150, 9) {
		t.Error("the Pacific should not be land")
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
