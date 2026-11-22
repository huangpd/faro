package main

import (
	"embed"
	"path"
	"strings"

	"github.com/egoist/mygo/ui"
)

// The icons are Lucide's (ISC licence), one family, one stroke.
//
//go:embed ui_assets/icons/*.svg
var iconFiles embed.FS

var iconSet = loadIcons()

// logo is Faro's mark: a lighthouse throwing a beam each way.
//
//go:embed ui_assets/faro-logo.svg
var logoSVG []byte

var logo = ui.MustParseSVG(logoSVG)

// version is shown under the name.
const version = "0.1.0"

func loadIcons() map[string]*ui.SVG {
	m := map[string]*ui.SVG{}
	entries, _ := iconFiles.ReadDir("ui_assets/icons")
	for _, e := range entries {
		b, err := iconFiles.ReadFile(path.Join("ui_assets/icons", e.Name()))
		if err != nil {
			continue
		}
		if svg, err := ui.ParseSVG(b); err == nil {
			m[strings.TrimSuffix(e.Name(), ".svg")] = svg
		}
	}
	return m
}

// icon draws a named icon at a size, in a color; a missing one draws nothing.
func icon(c *ui.Context, name string, size float32, color ui.Color) ui.Element {
	svg := iconSet[name]
	if svg == nil {
		return ui.Box(c).Size(size, size)
	}
	return ui.Icon(c, svg).FontSize(size).TextColor(color)
}
