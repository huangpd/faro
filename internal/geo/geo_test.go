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

func TestTheMapIsFramedTheSameWithTheOriginAtTheRight(t *testing.T) {
	here, _ := Place("HK")
	w := WorldFrom(here, 0.78, 200)
	x, _ := w.XY(here)
	if math.Abs(x-0.78) > 1e-9 {
		t.Errorf("the origin is at %v", x)
	}
	uk, _ := Place("UK")
	de, _ := Place("DE")
	jp, _ := Place("JP")
	sg, _ := Place("SG")
	us, _ := Place("US")
	in := func(p Point) float64 { x, _ := w.XY(p); return x }
	for name, p := range map[string]Point{"UK": uk, "DE": de, "JP": jp, "SG": sg} {
		if x := in(p); x < 0.02 || x > 0.98 {
			t.Errorf("%s is at %v: not on the map", name, x)
		}
	}
	if !(in(uk) < in(de) && in(de) < in(here) && in(here) < in(jp)) {
		t.Errorf("west to east: UK %v, DE %v, here %v, Japan %v", in(uk), in(de), in(here), in(jp))
	}
	if x := in(us); x <= 1 {
		t.Errorf("the US is at %v: it is across the Pacific, off the map", x)
	}
	// What is reached the short way: the UK is west, the US east.
	if r := w.Reach(here, uk); r > -0.5 || r < -0.7 {
		t.Errorf("the UK is %v widths away", r)
	}
	if r := w.Reach(here, us); r < 0.6 {
		t.Errorf("the US is %v widths away", r)
	}
	if r := w.Reach(here, here); r != 0 {
		t.Errorf("here is %v from here", r)
	}
	// The same place, either way, is the same place.
	x1, _ := w.XY(Point{0, here.Lon + 180})
	x2, _ := w.XY(Point{0, here.Lon - 180})
	if math.Abs(x1-x2) > 1e-9 {
		t.Errorf("the other side of the Earth is at %v and %v", x1, x2)
	}
	if _, y := w.XY(Point{Top, 0}); y != 0 {
		t.Errorf("top %v", y)
	}
	if _, y := w.XY(Point{Bottom, 0}); y != 1 {
		t.Errorf("bottom %v", y)
	}
}

func TestCoarseLandKeepsTheShapeOfTheWorld(t *testing.T) {
	if len(Coarse) < 1200 || len(Coarse) > 4000 {
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
