// Command genicon draws the menu-bar icon: Faro's lighthouse as a template
// image, black on transparent, which macOS tints for a light or a dark menu
// bar. It is drawn here, at 2x (36x36 for 18 points), from the same shapes as
// the logo, so it needs no outside tool.
//
//	go run ./cmd/genicon resources/tray.png
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

const (
	size = 36 // pixels: 18 points at 2x
	ss   = 8  // samples per pixel side, for smooth edges
)

// shapes are in a 36x36 box. A point is inside if any shape holds it.
var inside = []func(x, y float64) bool{
	// the tower: a trapezoid, wide at the foot, with the lamp room on top of it
	func(x, y float64) bool {
		if y < 15 || y > 29.5 {
			return false
		}
		t := (y - 15) / 14.5
		return math.Abs(x-18) <= 4.2+t*4.6
	},
	// the base
	func(x, y float64) bool { return y >= 29.5 && y <= 32.5 && x >= 8 && x <= 28 },
	// the lamp: a solid disc, large enough to read at 18 points
	func(x, y float64) bool { return math.Hypot(x-18, y-9.5) <= 4.6 },
	// the lamp's neck, joining it to the tower
	func(x, y float64) bool { return y >= 13 && y <= 15.5 && math.Abs(x-18) <= 3.4 },
	// one beam each side: a thick arc, apart from the lamp
	func(x, y float64) bool { return ring(x, y, 18, 9.5, 10.6, 2.0, true) },
}

// ring is a part of a circle's line: the left and right sides only, not the top and bottom.
func ring(x, y, cx, cy, r, w float64, both bool) bool {
	d := math.Hypot(x-cx, y-cy)
	if math.Abs(d-r) > w/2 {
		return false
	}
	return math.Abs(y-cy) < r*0.55 && math.Abs(x-cx) > r*0.75 // the sides, not the top
}

func main() {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			n := 0
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := float64(px) + (float64(sx)+0.5)/ss
					y := float64(py) + (float64(sy)+0.5)/ss
					for _, f := range inside {
						if f(x, y) {
							n++
							break
						}
					}
				}
			}
			a := uint8(255 * n / (ss * ss))
			img.SetNRGBA(px, py, color.NRGBA{0, 0, 0, a})
		}
	}
	f, err := os.Create(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer f.Close()
	png.Encode(f, img)
}
