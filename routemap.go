package main

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"faro/internal/engine"
	"faro/internal/geo"
	"faro/internal/rules"
	"faro/internal/singcfg"
)

// A lane's map: the world as dots of land, turned so that this Mac is near the
// left edge, with an arc from here to the lane's exit that runs once and stands
// still. Every lane's map is framed the same, so they read against each other.
// It is drawn from data in the app; nothing is looked up from the network.

const (
	mapW, mapH = 250, 104
	// mapOrigin is how far from the left edge this Mac is, as a fraction of the width.
	mapOrigin = 0.13
	// mapScale is how many pixels the still layer has to a DIP: enough for sharp dots
	// on a screen that shows two.
	mapScale = 2
	// mapRunTime is how long the light takes to run from here to the exit, and
	// mapEvery how often it runs.
	mapRunTime = 1100 * time.Millisecond
	mapEvery   = 5 * time.Second
	mapTail    = 16 // points of the light's tail
	mapN       = 56 // points along the arc
)

type mapMode int

const (
	mapLive   mapMode = iota // an arc to the exit
	mapClosed                // the gate is shut: nowhere to go
)

// mapLayer is the part of the map that does not move, and what it was drawn for.
type mapLayer struct {
	cv  *ui.Canvas
	key string
}

// mapRun is a lane's arc: where it went, and when its light first ran.
type mapRun struct {
	exit  string
	start time.Time
}

// laneMap is the map of a lane. exit is where its exit is, nil if that is not
// known; label is what the exit is called there.
func (a *app) laneMap(c *ui.Context, id string, hue ui.Color, exit *geo.Point, label string, mode mapMode) {
	if a.here == nil {
		h := geo.Here()
		a.here = &h
	}
	from := *a.here
	ui.Box(c).Size(mapW, mapH).Radius(10).Clip().Background(a.k.surface2.Alpha(0.5)).Border(1, a.k.border).
		Shrink(0).Role(ui.RoleNone).Draw(func(p *ui.Painter, r ui.Rect) {
		a.drawMap(p, id, r, from, exit, label, hue, mode)
	})
}

func (a *app) drawMap(p *ui.Painter, id string, r ui.Rect, from geo.Point, exit *geo.Point, label string, hue ui.Color, mode mapMode) {
	world := geo.WorldFrom(from, mapOrigin)
	live := exit != nil && mode == mapLive

	// A glow in the lane's hue, and the land under it: the land is the same for
	// every lane, drawn once into a canvas and shown from it.
	p.FillRadialGradient(r, ui.RadialGradient{Center: ui.Point{Y: -0.3}, Radius: r.W * 0.6,
		Stops: []ui.GradientStop{{Color: hue.Alpha(0.14), At: 0}, {Color: hue.Alpha(0), At: 1}}}, 0)
	key := fmt.Sprintf("%.2f %v %v", world.L0, a.k.dark, r.W)
	if a.mapLand == nil || a.mapLand.key != key {
		if a.mapLand == nil {
			a.mapLand = &mapLayer{cv: ui.NewCanvas(int(r.W*mapScale), int(r.H*mapScale))}
		}
		a.mapLand.cv.Draw(p, func(cp *ui.Painter) { a.paintLand(cp, world, r.W*mapScale, r.H*mapScale, mapScale) })
		a.mapLand.key = key
	}
	p.Image(a.mapLand.cv.Bitmap(), r, ui.FillBox)

	at := func(pt geo.Point) (float32, float32) {
		x, y := world.XY(pt)
		return r.X + float32(x)*r.W, r.Y + float32(y)*r.H
	}
	dot := func(x, y, rad float32, col ui.Color) {
		p.Fill(ui.Rect{X: x - rad, Y: y - rad, W: 2 * rad, H: 2 * rad}, col, rad)
	}
	ax, ay := at(from)
	if !live {
		col := a.k.text
		if mode == mapClosed {
			col = a.k.danger
		}
		dot(ax, ay, 2.8, col)
		return
	}

	// The arc is there all the time, and every few seconds a light runs along it
	// from here to the exit. Between runs nothing is drawn but the still picture,
	// and the window is asked for a frame only when the next run is due: a map
	// that flows costs a little for a moment, not all the time.
	run := a.mapRuns[id]
	where := fmt.Sprintf("%.2f %.2f", exit.Lat, exit.Lon)
	now := p.Now()
	if run == nil {
		run = &mapRun{}
		a.mapRuns[id] = run
	}
	if run.exit != where { // a new exit: the light runs at once
		run.exit, run.start = where, now
	}
	phase := time.Duration(math.Mod(float64(now.Sub(run.start)), float64(mapEvery)))
	running := phase < mapRunTime

	_, ey := at(*exit)
	ex := ax + float32(geo.Way(from, *exit))*r.W // not wrapped: past an edge, the arc goes on from the other
	pts := mapArc(ax, ay, ex, ey, r.X, r.W, mapN)
	for _, seg := range runsOf(pts, r.W) {
		p.StrokePolyline(seg, 1.4, hue.Alpha(0.42))
	}
	dot(ax, ay, 2.8, a.k.text)
	bx, by := pts[mapN][0], pts[mapN][1]
	p.Stroke(ui.Rect{X: bx - 7, Y: by - 7, W: 14, H: 14}, hue.Alpha(0.4), 7, 1.2)
	dot(bx, by, 3.4, hue)
	if label != "" {
		lx := bx + 10
		if bx > r.X+r.W*0.7 { // not off the edge
			lx = bx - 10 - 7*float32(len(label))
		}
		p.Text(lx, by-6, label, 10, a.k.text2)
	}
	if !running {
		p.After(mapEvery - phase)
		return
	}
	head := int(float64(phase) / float64(mapRunTime) * mapN)
	for k, w := range []float32{1.8, 2.6} { // a tail that brightens toward the light
		from := max(0, head-int(mapTail)*(2-k)/2)
		for _, seg := range runsOf(pts[from:head+1], r.W) {
			p.StrokePolyline(seg, w, hue.Alpha(0.5+0.45*float32(k)))
		}
	}
	hx, hy := pts[head][0], pts[head][1]
	p.FillRadialGradient(ui.Rect{X: hx - 9, Y: hy - 9, W: 18, H: 18}, ui.RadialGradient{Radius: 9,
		Stops: []ui.GradientStop{{Color: hue.Alpha(0.7), At: 0}, {Color: hue.Alpha(0)}}}, 9)
	dot(hx, hy, 1.8, ui.RGBA(255, 255, 255, 1))
	p.AnimationFrameRate(15)
}

// mapArc is n+1 points of an arc from (ax, ay) to (ex, ey) that bows up from the
// straight line between them, by a third of its length. An end past the edge of the
// map, which is left to left+width, goes on from the other edge: the points are
// put within the map, so that the arc leaves at one side and comes in at the other.
func mapArc(ax, ay, ex, ey, left, width float32, n int) [][2]float32 {
	dx, dy := ex-ax, ey-ay
	nx, ny := -dy, dx // a normal to the line, turned to point up
	if ny > 0 {
		nx, ny = -nx, -ny
	}
	nx, ny = nx*0.34, ny*0.34 // the normal is as long as the line: a third of it
	cx, cy := (ax+ex)/2+nx, (ay+ey)/2+ny
	pts := make([][2]float32, n+1)
	for i := range pts {
		t := float32(i) / float32(n)
		u := 1 - t
		x := u*u*ax + 2*u*t*cx + t*t*ex
		y := u*u*ay + 2*u*t*cy + t*t*ey
		x = left + float32(math.Mod(float64(x-left), float64(width)))
		if x < left {
			x += width
		}
		pts[i] = [2]float32{x, y}
	}
	return pts
}

// runsOf splits points where they jump from one side of the map to the other, so
// that no line is drawn across it.
func runsOf(pts [][2]float32, width float32) [][][2]float32 {
	var out [][][2]float32
	start := 0
	for i := 1; i <= len(pts); i++ {
		if i == len(pts) || math.Abs(float64(pts[i][0]-pts[i-1][0])) > float64(width)/2 {
			if i-start > 1 {
				out = append(out, pts[start:i])
			}
			start = i
		}
	}
	return out
}

// paintLand paints the land as dots, k pixels to a DIP.
func (a *app) paintLand(p *ui.Painter, world geo.World, w, h, k float32) {
	pitch := float32(geo.CoarseCell/360) * w // how far apart the dots are
	sz := min(k*2.2, pitch*0.62)
	for _, c := range geo.Coarse {
		x, y := world.XY(c)
		px, py := float32(x)*w, float32(y)*h
		p.Fill(ui.Rect{X: px - sz/2, Y: py - sz/2, W: sz, H: sz}, a.k.text2.Alpha(0.45), sz/2)
	}
}

// exitPlace is where a lane's exit is taken to be: the node it is on, or the
// fastest of its nodes when it follows the fastest. ok is false when the node's
// country has no place on the globe.
func exitPlace(st engine.State, chosen string, nodes []string) (geo.Point, bool) {
	p, _, ok := exitPlaceLabel(st, chosen, nodes)
	return p, ok
}

// exitPlaceLabel is exitPlace, with the region's id as well.
func exitPlaceLabel(st engine.State, chosen string, nodes []string) (geo.Point, string, bool) {
	tag := chosen
	if tag == "" {
		best := -1
		for _, n := range nodes {
			if d := st.Delays[n]; d > 0 && (best < 0 || d < best) {
				tag, best = n, d
			}
		}
		if tag == "" && len(nodes) > 0 {
			tag = nodes[0]
		}
	}
	if tag == "" {
		return geo.Point{}, "", false
	}
	id := st.Plan.RegionOf(tag)
	p, ok := geo.Place(id)
	return p, id, ok
}

// laneExitPlace is exitPlace for a route, with its region's id.
func laneExitPlace(st engine.State, r rules.Route) (*geo.Point, string) {
	if r.Closed || len(r.Nodes) == 0 {
		return nil, ""
	}
	if p, id, ok := exitPlaceLabel(st, r.Chosen, r.Nodes); ok {
		return &p, id
	}
	return nil, ""
}

// mainExitPlace is exitPlace for what the rules do not name.
func mainExitPlace(st engine.State) (*geo.Point, string) {
	var nodes []string
	for _, n := range st.Nodes {
		if st.Plan == nil || !st.Plan.IsNotice(n.Tag) {
			nodes = append(nodes, n.Tag)
		}
	}
	if p, id, ok := exitPlaceLabel(st, st.Choices[singcfg.TagProxy], nodes); ok {
		return &p, id
	}
	return nil, ""
}
