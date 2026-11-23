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

// A lane's map: the part of the world map between this Mac and the lane's
// exit, as dots of land, with an arc from one to the other and a light that runs
// along it. It says where the traffic leaves from at a glance; it is drawn from
// data in the app, and nothing is looked up from the network for it.

const (
	mapW, mapH = 196, 104
	// mapScale is how many pixels the still layer has to a DIP: enough for sharp dots
	// on a screen that shows two.
	mapScale = 2
)

type mapMode int

const (
	mapLive   mapMode = iota // an arc to the exit
	mapClosed                // the gate is shut: nowhere to go
)

// mapLayer is the part of a map that does not move, and what it was drawn for.
type mapLayer struct {
	cv  *ui.Canvas
	key string
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
	aspect := float64(r.W / r.H)
	live := exit != nil && mode == mapLive
	to := from
	if live {
		to = *exit
	}
	vp := geo.Frame(from, to, aspect, 46)

	// What does not move is drawn once into a canvas and shown from it: the land
	// is some hundreds of dots, too many to draw again every frame.
	key := fmt.Sprintf("%.2f %.2f %.1f %v %v %v", vp.Lon, vp.Lat, vp.SpanLon, hue, a.k.dark, r.W)
	layer := a.mapLayers[id]
	if layer == nil {
		layer = &mapLayer{cv: ui.NewCanvas(int(r.W*mapScale), int(r.H*mapScale))}
		a.mapLayers[id] = layer
	}
	if layer.key != key {
		layer.cv.Draw(p, func(cp *ui.Painter) { a.paintLand(cp, vp, hue, r.W*mapScale, r.H*mapScale, mapScale) })
		layer.key = key
	}
	p.Image(layer.cv.Bitmap(), r, ui.FillBox)

	at := func(pt geo.Point) (float32, float32) {
		x, y := vp.XY(pt)
		return r.X + float32(x)*r.W, r.Y + float32(y)*r.H
	}
	ax, ay := at(from)
	dot := func(x, y, rad float32, col ui.Color) {
		p.Fill(ui.Rect{X: x - rad, Y: y - rad, W: 2 * rad, H: 2 * rad}, col, rad)
	}
	now := p.Now()
	ring := func(x, y float32, col ui.Color, phase float64) {
		u := float32(math.Mod(float64(now.UnixMilli())/1900+phase, 1))
		rr := 3 + 9*u
		p.Stroke(ui.Rect{X: x - rr, Y: y - rr, W: 2 * rr, H: 2 * rr}, col.Alpha((1-u)*0.55), rr, 1.2)
	}
	if !live {
		col := a.k.text
		if mode == mapClosed {
			col = a.k.danger
		}
		dot(ax, ay, 2.8, col)
		return // nothing moves: nothing asks for another frame
	}

	// The arc bows up from the straight line between the two, by a third of its length.
	bx, by := at(to)
	dx, dy := bx-ax, by-ay
	mx, my := (ax+bx)/2, (ay+by)/2
	nx, ny := -dy, dx // a normal to the line, turned to point up
	if ny > 0 {
		nx, ny = -nx, -ny
	}
	if n := float32(math.Hypot(float64(nx), float64(ny))); n > 1e-3 {
		k := 0.34 * float32(math.Hypot(float64(dx), float64(dy))) / n
		nx, ny = nx*k, ny*k
	}
	cx, cy := mx+nx, my+ny
	arc := func() *ui.Path { return new(ui.Path).MoveTo(ax, ay).QuadTo(cx, cy, bx, by) }
	p.StrokePath(arc().Cap(ui.RoundCap), 1.3, hue.Alpha(0.34))
	t := float32(math.Mod(float64(now.UnixMilli())/2800, 1))
	for k, w := range []float32{1.4, 2, 2.6} { // a tail, brighter toward the head
		from, to := t-0.06*float32(3-k), t-0.06*float32(2-k)
		if to > 0 && from < 1 {
			p.StrokePath(arc().Trim(max(from, 0), min(to, 1)).Cap(ui.RoundCap), w, hue.Alpha(0.35+0.25*float32(k)))
		}
	}
	// The light itself, at t along the curve.
	u := 1 - t
	hx := u*u*ax + 2*u*t*cx + t*t*bx
	hy := u*u*ay + 2*u*t*cy + t*t*by
	p.FillRadialGradient(ui.Rect{X: hx - 9, Y: hy - 9, W: 18, H: 18}, ui.RadialGradient{Radius: 9,
		Stops: []ui.GradientStop{{Color: hue.Alpha(0.7), At: 0}, {Color: hue.Alpha(0), At: 1}}}, 9)
	dot(hx, hy, 1.8, ui.RGBA(255, 255, 255, 1))

	ring(ax, ay, a.k.text, 0)
	dot(ax, ay, 2.8, a.k.text)
	ring(bx, by, hue, 0.5)
	dot(bx, by, 3.4, hue)
	if label != "" {
		lx := bx + 8
		if bx > r.X+r.W*0.7 { // not off the edge
			lx = bx - 8 - 7*float32(len(label))
		}
		p.Text(lx, by-6, label, 10, a.k.text2)
	}
	p.AnimationFrameRate(15)
}

// paintLand paints the land as dots, k pixels to a DIP, over a soft glow.
func (a *app) paintLand(p *ui.Painter, vp geo.Viewport, hue ui.Color, w, h, k float32) {
	p.FillRadialGradient(ui.Rect{W: w, H: h}, ui.RadialGradient{
		Center: ui.Point{Y: -0.25}, Radius: w * 0.7,
		Stops: []ui.GradientStop{{Color: hue.Alpha(0.13), At: 0}, {Color: hue.Alpha(0), At: 1}},
	}, 0)
	pitch := float32(geo.Cell/vp.SpanLon) * w // how far apart the dots are
	sz := min(k*2.2, pitch*0.46)
	for _, c := range geo.Land {
		x, y := vp.XY(c)
		if x < -0.02 || x > 1.02 || y < -0.02 || y > 1.02 {
			continue
		}
		px, py := float32(x)*w, float32(y)*h
		p.Fill(ui.Rect{X: px - sz/2, Y: py - sz/2, W: sz, H: sz}, a.k.text2.Alpha(0.42), sz/2)
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
