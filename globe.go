package main

import (
	"fmt"
	"math"

	"github.com/egoist/mygo/ui"

	"faro/internal/engine"
	"faro/internal/geo"
	"faro/internal/rules"
	"faro/internal/singcfg"
)

// A lane's globe: the Earth as dots of land, with an arc from where this Mac is
// to where the lane's exit is, and a light that runs along it. It says where
// the traffic leaves from at a glance; it is drawn from data in the app, and
// nothing is looked up from the network for it.

const globeSize = 116

type globeMode int

const (
	globeLive   globeMode = iota // an arc to the exit
	globeClosed                  // the gate is shut: nowhere to go
)

// laneGlobe is the globe of a lane. exit is where its exit is, nil if that is not known.
func (a *app) laneGlobe(c *ui.Context, id string, hue ui.Color, exit *geo.Point, mode globeMode) {
	if a.here == nil {
		h := geo.Here()
		a.here = &h
	}
	from := *a.here
	ui.Box(c).Size(globeSize, globeSize).Shrink(0).Role(ui.RoleNone).Draw(func(p *ui.Painter, r ui.Rect) {
		a.drawGlobe(p, id, r, from, exit, hue, mode)
	})
}

func (a *app) drawGlobe(p *ui.Painter, id string, r ui.Rect, from geo.Point, exit *geo.Point, hue ui.Color, mode globeMode) {
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	R := min(r.W, r.H)/2 - 9

	// Where the globe looks: at the middle of the arc, a little from above, so
	// that both ends are on the face.
	look := from
	if exit != nil && mode == globeLive {
		if geo.Distance(from, *exit) < 150 {
			look = geo.Mid(from, *exit)
		} else {
			look = *exit
		}
	}
	look.Lat = look.Lat*0.7 + 8
	view := geo.ViewOf(look)
	at := func(s geo.Spot) (float32, float32) { return cx + float32(s.X)*R, cy - float32(s.Y)*R }

	// What does not move is drawn once into a canvas and shown from it: the
	// land alone is some hundreds of dots, too many to draw again every frame.
	key := fmt.Sprintf("%.1f %.1f %v %v %v", look.Lat, look.Lon, hue, a.k.dark, r.W)
	layer := a.globeLayers[id]
	if layer == nil {
		layer = &globeLayer{cv: ui.NewCanvas(int(r.W*globeScale), int(r.H*globeScale))}
		a.globeLayers[id] = layer
	}
	if layer.key != key {
		layer.cv.Draw(p, func(cp *ui.Painter) {
			a.paintGlobeBody(cp, view, hue, float32(globeScale)*r.W/2, float32(globeScale)*r.H/2, R*globeScale, globeScale)
		})
		layer.key = key
	}
	p.Image(layer.cv.Bitmap(), r, ui.FillBox)

	now := p.Now()
	ring := func(x, y float32, col ui.Color, phase float64) {
		u := float32(math.Mod(float64(now.UnixMilli())/1900+phase, 1))
		rr := 3 + 9*u
		p.Stroke(ui.Rect{X: x - rr, Y: y - rr, W: 2 * rr, H: 2 * rr}, col.Alpha((1-u)*0.55), rr, 1.2)
	}
	dot := func(x, y, rad float32, col ui.Color) {
		p.Fill(ui.Rect{X: x - rad, Y: y - rad, W: 2 * rad, H: 2 * rad}, col, rad)
	}

	hs := view.Project(from, 1)
	if exit == nil || mode != globeLive {
		if hs.Depth > 0 {
			x, y := at(hs)
			col := a.k.text
			if mode == globeClosed {
				col = a.k.danger
			}
			dot(x, y, 2.6, col)
		}
		return // nothing moves: nothing asks for another frame
	}

	// The arc: all of it faint, and a light running from here to there, with a tail.
	const n = 56
	pts, heights := geo.Arc(from, *exit, n, 0.3)
	spots := make([]geo.Spot, len(pts))
	for i := range pts {
		spots[i] = view.Project(pts[i], heights[i])
	}
	run := func(i0, i1 int, w float32, col ui.Color) {
		var line [][2]float32
		flush := func() {
			if len(line) > 1 {
				p.StrokePolyline(line, w, col)
			}
			line = line[:0]
		}
		for i := i0; i <= i1; i++ {
			if spots[i].Depth <= 0 {
				flush()
				continue
			}
			x, y := at(spots[i])
			line = append(line, [2]float32{x, y})
		}
		flush()
	}
	run(0, n, 1.3, hue.Alpha(0.32))
	t := math.Mod(float64(now.UnixMilli())/2600, 1)
	head := int(t * n)
	for k, w := range []float32{1.6, 2.2, 2.8} { // the tail is brighter toward the head
		i0 := max(0, head-(3-k)*5)
		i1 := min(n, head-(2-k)*5)
		if i1 > i0 {
			run(i0, i1, w, hue.Alpha(0.35+0.25*float32(k)))
		}
	}
	if s := spots[head]; s.Depth > 0 {
		x, y := at(s)
		p.FillRadialGradient(ui.Rect{X: x - 9, Y: y - 9, W: 18, H: 18}, ui.RadialGradient{Radius: 9,
			Stops: []ui.GradientStop{{Color: hue.Alpha(0.7), At: 0}, {Color: hue.Alpha(0), At: 1}}}, 9)
		dot(x, y, 1.8, ui.RGBA(255, 255, 255, 1))
	}

	// The two ends: here, and the exit.
	if hs.Depth > 0 {
		x, y := at(hs)
		ring(x, y, a.k.text, 0)
		dot(x, y, 2.6, a.k.text)
	}
	if es := view.Project(*exit, 1); es.Depth > 0 {
		x, y := at(es)
		ring(x, y, hue, 0.5)
		dot(x, y, 3.2, hue)
	}
	p.AnimationFrameRate(24)
}

// globeScale is how many pixels the still layer has to a DIP: enough for a
// sharp edge on a screen that shows two.
const globeScale = 2

// globeLayer is the part of a globe that does not move, and what it was drawn for.
type globeLayer struct {
	cv  *ui.Canvas
	key string
}

// paintGlobeBody paints the body of the globe: a glow past its rim, its sphere
// lit from above left, a hairline for its edge, and the land as dots, bigger and
// clearer toward the middle. Lengths are in the pixels of the painter, k to a DIP.
func (a *app) paintGlobeBody(p *ui.Painter, view geo.View, hue ui.Color, cx, cy, R, k float32) {
	box := ui.Rect{X: cx - R, Y: cy - R, W: 2 * R, H: 2 * R}
	whole := ui.Rect{X: cx - cx, Y: cy - cy, W: 2 * cx, H: 2 * cy}
	p.FillRadialGradient(whole, ui.RadialGradient{
		Radius: R * 1.16,
		Stops:  []ui.GradientStop{{Color: hue.Alpha(0), At: 0.7}, {Color: hue.Alpha(0.16), At: 0.86}, {Color: hue.Alpha(0), At: 1}},
	}, 0)
	p.FillRadialGradient(box, ui.RadialGradient{
		Center: ui.Point{X: 0.18, Y: -0.2}, Radius: R * 1.5,
		Stops: []ui.GradientStop{{Color: a.k.surface2.Mix(hue, 0.18), At: 0}, {Color: a.k.surface, At: 1}},
	}, R)
	p.Stroke(box, hue.Alpha(0.38), R, k)
	for _, pt := range geo.Land {
		s := view.Project(pt, 1)
		if s.Depth < 0.04 {
			continue
		}
		x, y := cx+float32(s.X)*R, cy-float32(s.Y)*R
		sz := k * float32(1.1+0.9*s.Depth)
		p.Fill(ui.Rect{X: x - sz/2, Y: y - sz/2, W: sz, H: sz}, a.k.text2.Alpha(float32(0.18+0.5*s.Depth)), sz/2)
	}
}

// exitPlace is where a lane's exit is taken to be: the node it is on, or the
// fastest of its nodes when it follows the fastest. ok is false when the node's
// country has no place on the globe.
func exitPlace(st engine.State, chosen string, nodes []string) (geo.Point, bool) {
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
		return geo.Point{}, false
	}
	return geo.Place(st.Plan.RegionOf(tag))
}

// laneExitPlace is exitPlace for a route.
func laneExitPlace(st engine.State, r rules.Route) *geo.Point {
	if r.Closed || len(r.Nodes) == 0 {
		return nil
	}
	if p, ok := exitPlace(st, r.Chosen, r.Nodes); ok {
		return &p
	}
	return nil
}

// mainExitPlace is exitPlace for what the rules do not name.
func mainExitPlace(st engine.State) *geo.Point {
	var nodes []string
	for _, n := range st.Nodes {
		if st.Plan == nil || !st.Plan.IsNotice(n.Tag) {
			nodes = append(nodes, n.Tag)
		}
	}
	if p, ok := exitPlace(st, st.Choices[singcfg.TagProxy], nodes); ok {
		return &p
	}
	return nil
}
