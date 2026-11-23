// Package geo is the little geography the globe needs: where the land is,
// where a region's nodes are taken to be, where this Mac is taken to be, and
// the arc between two places. It is all data embedded in the app; nothing is
// looked up from the network, and nothing of the person's is sent anywhere.
package geo

import (
	_ "embed"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

//go:embed data/land.txt
var landText string

//go:embed data/places.tsv
var placesText string

//go:embed data/zones.tsv
var zonesText string

// Point is a place on the Earth, in degrees.
type Point struct{ Lat, Lon float64 }

// Land is dots spread evenly over the land of the Earth.
var Land = parsePoints(landText, 0)

func parsePoints(text string, skip int) []Point {
	var out []Point
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 2+skip || strings.HasPrefix(f[0], "#") {
			continue
		}
		la, err1 := strconv.ParseFloat(f[skip], 64)
		lo, err2 := strconv.ParseFloat(f[skip+1], 64)
		if err1 == nil && err2 == nil {
			out = append(out, Point{la, lo})
		}
	}
	return out
}

func parseTable(text string) map[string]Point {
	m := map[string]Point{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) != 3 || strings.HasPrefix(f[0], "#") {
			continue
		}
		la, err1 := strconv.ParseFloat(f[1], 64)
		lo, err2 := strconv.ParseFloat(f[2], 64)
		if err1 == nil && err2 == nil {
			m[f[0]] = Point{la, lo}
		}
	}
	return m
}

var (
	places = parseTable(placesText)
	zones  = parseTable(zonesText)
)

// Place is where a region's nodes are taken to be, by the region's id.
func Place(region string) (Point, bool) {
	p, ok := places[strings.ToUpper(region)]
	return p, ok
}

// Here is where this Mac is taken to be, from its time zone; failing that, from
// how far its clock is from UTC, which says the longitude and little else.
func Here() Point {
	if p, ok := ZoneOf(zoneName(), time.Now()); ok {
		return p
	}
	return Point{}
}

func zoneName() string {
	if tz := os.Getenv("TZ"); tz != "" {
		return strings.TrimPrefix(tz, ":")
	}
	if l, err := os.Readlink("/etc/localtime"); err == nil {
		if i := strings.Index(l, "zoneinfo/"); i >= 0 {
			return l[i+len("zoneinfo/"):]
		}
	}
	return ""
}

// ZoneOf places a time zone by name; one it does not know is placed on its
// UTC offset at now: 15 degrees of longitude an hour, at a middle latitude.
func ZoneOf(name string, now time.Time) (Point, bool) {
	if p, ok := zones[name]; ok {
		return p, true
	}
	_, off := now.Zone()
	return Point{Lat: 35, Lon: float64(off) / 3600 * 15}, true
}

// vec is a point as a unit vector.
func vec(p Point) [3]float64 {
	la, lo := rad(p.Lat), rad(p.Lon)
	return [3]float64{math.Cos(la) * math.Cos(lo), math.Cos(la) * math.Sin(lo), math.Sin(la)}
}

func rad(d float64) float64 { return d * math.Pi / 180 }

// Distance is the angle between two places, in degrees.
func Distance(a, b Point) float64 {
	va, vb := vec(a), vec(b)
	d := va[0]*vb[0] + va[1]*vb[1] + va[2]*vb[2]
	return math.Acos(math.Max(-1, math.Min(1, d))) * 180 / math.Pi
}

// Mid is the point halfway along the shortest way between two places.
func Mid(a, b Point) Point {
	va, vb := vec(a), vec(b)
	m := [3]float64{va[0] + vb[0], va[1] + vb[1], va[2] + vb[2]}
	n := math.Sqrt(m[0]*m[0] + m[1]*m[1] + m[2]*m[2])
	if n < 1e-9 { // opposite sides: any way round is as short
		return Point{Lat: 90, Lon: 0}
	}
	return Point{math.Asin(m[2]/n) * 180 / math.Pi, math.Atan2(m[1], m[0]) * 180 / math.Pi}
}

// Spot is a place on the globe's face as seen from a view: x to the right and y
// up, both from the middle, in fractions of the radius; Depth is how far toward
// the viewer it is, below 0 on the far side.
type Spot struct{ X, Y, Depth float64 }

// View is where the globe is looked at from: the place at its middle.
type View struct{ sinLa, cosLa, lon float64 }

// ViewOf looks at the Earth with p in the middle.
func ViewOf(p Point) View { return View{math.Sin(rad(p.Lat)), math.Cos(rad(p.Lat)), rad(p.Lon)} }

// Project places a point, raised to height radius above the surface (1 is on it).
func (v View) Project(p Point, height float64) Spot {
	la, dl := rad(p.Lat), rad(p.Lon)-v.lon
	sl, cl := math.Sin(la), math.Cos(la)
	return Spot{
		X:     height * cl * math.Sin(dl),
		Y:     height * (v.cosLa*sl - v.sinLa*cl*math.Cos(dl)),
		Depth: height * (v.sinLa*sl + v.cosLa*cl*math.Cos(dl)),
	}
}

// Arc is n+1 points from a to b along the shortest way, with the height each is
// raised to: the arc lifts off the surface and comes back, as flight paths are
// drawn, peaking at 1+lift halfway.
func Arc(a, b Point, n int, lift float64) (pts []Point, heights []float64) {
	va, vb := vec(a), vec(b)
	d := math.Acos(math.Max(-1, math.Min(1, va[0]*vb[0]+va[1]*vb[1]+va[2]*vb[2])))
	for i := 0; i <= n; i++ {
		t := float64(i) / float64(n)
		var v [3]float64
		if d < 1e-6 {
			v = va
		} else {
			s := math.Sin(d)
			wa, wb := math.Sin((1-t)*d)/s, math.Sin(t*d)/s
			v = [3]float64{wa*va[0] + wb*vb[0], wa*va[1] + wb*vb[1], wa*va[2] + wb*vb[2]}
		}
		pts = append(pts, Point{math.Asin(math.Max(-1, math.Min(1, v[2]))) * 180 / math.Pi, math.Atan2(v[1], v[0]) * 180 / math.Pi})
		heights = append(heights, 1+lift*math.Sin(math.Pi*t))
	}
	return pts, heights
}
