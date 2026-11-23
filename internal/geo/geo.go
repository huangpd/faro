// Package geo is the little geography the map needs: where the land is,
// where a region's nodes are taken to be, where this Mac is taken to be, and
// the part of the map to show for two places. It is all data embedded in the app; nothing is
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

// Cell is the size of a cell of the land grid, in degrees.
const Cell = 1.5

// Land is the middle of each cell of the grid that is land.
var Land = parseLand(landText)

func parseLand(text string) []Point {
	var out []Point
	row := 0
	for _, line := range strings.Split(text, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lat := 85 - Cell*(float64(row)+0.5)
		for col, ch := range line {
			if ch == '#' {
				out = append(out, Point{lat, -180 + Cell*(float64(col)+0.5)})
			}
		}
		row++
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

// Viewport is the part of the flat map that is shown: its middle, and how many
// degrees it spans across and up.
type Viewport struct{ Lon, Lat, SpanLon, SpanLat float64 }

// wrap puts a longitude within 180 degrees of the middle.
func wrap(lon, mid float64) float64 {
	d := math.Mod(lon-mid, 360)
	if d > 180 {
		d -= 360
	} else if d < -180 {
		d += 360
	}
	return mid + d
}

// Frame is the part of the map that shows two places with room round them, at
// the shape aspect (width over height). It goes the short way round the Earth,
// so that a route over the Pacific is not drawn across the whole map, and shows
// at least minSpan degrees across, so that two near places are not blown up.
func Frame(a, b Point, aspect, minSpan float64) Viewport {
	b.Lon = wrap(b.Lon, a.Lon)
	w := math.Abs(b.Lon-a.Lon)*1.5 + 24
	h := math.Abs(b.Lat-a.Lat)*1.9 + 14
	span := math.Max(math.Max(w, h*aspect), minSpan)
	span = math.Min(span, 360)
	v := Viewport{
		Lon: (a.Lon + b.Lon) / 2, Lat: (a.Lat + b.Lat) / 2,
		SpanLon: span, SpanLat: span / aspect,
	}
	if v.SpanLat > 160 {
		v.SpanLat, v.SpanLon = 160, 160*aspect
	}
	// Keep the middle where there is map to show.
	if lim := 85 - v.SpanLat/2; v.Lat > lim {
		v.Lat = lim
	} else if lim := -65 + v.SpanLat/2; v.Lat < lim {
		v.Lat = lim
	}
	return v
}

// XY places a point on the viewport: x and y in fractions of its width and
// height from the top left, outside 0..1 when it is off the part shown.
func (v Viewport) XY(p Point) (x, y float64) {
	lon := wrap(p.Lon, v.Lon)
	return 0.5 + (lon-v.Lon)/v.SpanLon, 0.5 - (p.Lat-v.Lat)/v.SpanLat
}
