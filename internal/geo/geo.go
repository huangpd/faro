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

// The map is a part of the world a span of degrees wide, turned so that the
// place it is made for is at a fixed place across it. Every map is framed the
// same, so that they can be read against each other. A place outside it is past
// 1 on the right, and what is west of its left edge is past 1 as well.
type World struct{ L0, Span float64 }

// Top and Bottom are the latitudes the map shows, from north to south.
const (
	Top    = 78.0
	Bottom = -61.0
)

// WorldFrom frames a span of degrees so that origin is x0 of the way from the left.
func WorldFrom(origin Point, x0, span float64) World {
	return World{L0: origin.Lon - x0*span, Span: span}
}

// XY places a point on the map: x from 0 at the left to 1 at the right (more,
// when it is off the map), y from 0 at the top to 1 at the bottom.
func (w World) XY(p Point) (x, y float64) {
	x = math.Mod(p.Lon-w.L0, 360) / w.Span
	if x < 0 {
		x += 360 / w.Span
	}
	return x, (Top - p.Lat) / (Top - Bottom)
}

// Reach is how far from one place the other is across the map, the shortest way
// round the Earth, in widths of the map: positive going east, which is to the right.
func (w World) Reach(from, to Point) float64 {
	d := math.Mod(to.Lon-from.Lon, 360)
	if d > 180 {
		d -= 360
	} else if d < -180 {
		d += 360
	}
	return d / w.Span
}

// Coarse is the land as a grid twice as coarse as Land, a cell of it
// land where half of its cells are: dots far enough apart for a small map.
var Coarse = coarsen(landText, 2)

func coarsen(text string, f int) []Point {
	var rows []string
	for _, line := range strings.Split(text, "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			rows = append(rows, line)
		}
	}
	var out []Point
	for r := 0; r+f <= len(rows); r += f {
		for c := 0; c+f <= len(rows[r]); c += f {
			n := 0
			for i := 0; i < f; i++ {
				for j := 0; j < f; j++ {
					if rows[r+i][c+j] == '#' {
						n++
					}
				}
			}
			if n*3 >= f*f {
				out = append(out, Point{Top - Cell*(float64(r)+float64(f)/2), -180 + Cell*(float64(c)+float64(f)/2)})
			}
		}
	}
	return out
}

// CoarseCell is the size of a cell of Coarse, in degrees.
const CoarseCell = Cell * 2
