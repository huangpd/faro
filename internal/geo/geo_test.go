package geo

import (
	"math"
	"testing"
	"time"
)

func near(t *testing.T, what string, got, want, eps float64) {
	t.Helper()
	if math.Abs(got-want) > eps {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func TestDataIsThere(t *testing.T) {
	if len(Land) < 800 {
		t.Errorf("%d land dots", len(Land))
	}
	for _, p := range Land {
		if p.Lat < -90 || p.Lat > 90 || p.Lon < -180 || p.Lon > 180 {
			t.Fatalf("dot %v is not on the Earth", p)
		}
	}
	for _, id := range []string{"HK", "uk", "DE", "SG", "US", "JP"} {
		if _, ok := Place(id); !ok {
			t.Errorf("no place for %s", id)
		}
	}
	if _, ok := Place("ZZ"); ok {
		t.Error("a place for ZZ")
	}
	// Dots lie on land: London is among the dots near 51N, and the middle of the Pacific has none.
	near51, pacific := false, false
	for _, p := range Land {
		if math.Abs(p.Lat-51.5) < 3 && math.Abs(p.Lon-0) < 4 {
			near51 = true
		}
		if math.Abs(p.Lat-0) < 8 && math.Abs(p.Lon+150) < 8 {
			pacific = true
		}
	}
	if !near51 || pacific {
		t.Errorf("land is off: london %v, mid-pacific %v", near51, pacific)
	}
}

func TestDistanceMidAndProjection(t *testing.T) {
	hk, _ := Place("HK")
	uk, _ := Place("UK")
	near(t, "HK-UK degrees", Distance(hk, uk), 82, 8)
	near(t, "distance to itself", Distance(hk, hk), 0, 1e-6)
	m := Mid(hk, uk)
	near(t, "half way", Distance(hk, m), Distance(hk, uk)/2, 0.01)
	near(t, "half way, other end", Distance(uk, m), Distance(hk, uk)/2, 0.01)

	v := ViewOf(Point{20, 30})
	mid := v.Project(Point{20, 30}, 1)
	near(t, "centre x", mid.X, 0, 1e-9)
	near(t, "centre y", mid.Y, 0, 1e-9)
	near(t, "centre depth", mid.Depth, 1, 1e-9)
	east := v.Project(Point{20, 120}, 1) // a quarter turn away: on the edge, on the right
	if east.X < 0.9 || math.Abs(east.Depth) > 0.4 {
		t.Errorf("east %+v", east)
	}
	if far := v.Project(Point{-20, -150}, 1); far.Depth >= 0 {
		t.Errorf("the other side of the Earth is in front: %+v", far)
	}
}

func TestArcLeavesAndComesBack(t *testing.T) {
	a, b := Point{22.3, 114.2}, Point{51.5, -0.1}
	pts, hs := Arc(a, b, 40, 0.3)
	if len(pts) != 41 || len(hs) != 41 {
		t.Fatal(len(pts), len(hs))
	}
	near(t, "start lat", pts[0].Lat, a.Lat, 1e-6)
	near(t, "end lat", pts[40].Lat, b.Lat, 1e-6)
	near(t, "start lon", pts[0].Lon, a.Lon, 1e-6)
	near(t, "end lon", pts[40].Lon, b.Lon, 1e-6)
	near(t, "on the ground at the start", hs[0], 1, 1e-9)
	near(t, "on the ground at the end", hs[40], 1, 1e-9)
	near(t, "highest half way", hs[20], 1.3, 1e-9)
	near(t, "the middle is the midpoint", Distance(pts[20], Mid(a, b)), 0, 0.01)
	// The same place at both ends does not divide by nothing.
	same, _ := Arc(a, a, 4, 0.3)
	near(t, "a point", same[2].Lat, a.Lat, 1e-6)
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
