package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"faro/internal/engine"
)

// Renders each page to a PNG, for looking at the design. Off unless asked:
//
//	FARO_SNAP=/some/dir go test -run Snapshots
func TestSnapshots(t *testing.T) {
	dir := os.Getenv("FARO_SNAP")
	if dir == "" {
		t.Skip("set FARO_SNAP to a directory")
	}
	os.MkdirAll(dir, 0o755)
	a := newTestApp(t)
	withNodes(t, a,
		"🇬🇧英国-UK-1-流量倍率:0.9", "🇩🇪德国-DE-2-流量倍率:1", "🇩🇪德国-DE-3-流量倍率:1",
		"🇯🇵日本-TY-1-流量倍率:1.2", "🇯🇵日本-TY-2-流量倍率:1", "🇺🇸美国-US-1-流量倍率:1", "🇸🇬新加坡-SG-1-流量倍率:1",
		"🇭🇰中国香港-HK-1-流量倍率:0.7", "🇳🇱荷兰-NL-4-流量倍率:0.1")
	usePreset(t, a, "ai-accounts")
	for _, g := range []struct{ name, to, sites string }{
		{"AI 应用", "claude", "claude.ai anthropic.com chatgpt.com openai.com"},
		{"视频", "proxy", "youtube.com googlevideo.com ytimg.com"},
		{"直连", "direct", "intranet.example.com"},
	} {
		if err := a.eng.NewGroup(g.name, g.to, g.sites); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"geosite-google", "geosite-google-gemini"} {
		if err := a.eng.AddSet(a.eng.State().Groups[0].Name, name); err != nil {
			t.Fatal(err)
		}
	}
	search := "goo"
	a.setQuery[a.eng.State().Groups[0].Name] = &search
	a.eng.Start()
	wait(t, "running", func() bool { return a.eng.State().Status == engine.Running })

	a.eng.TestAll()
	wait(t, "timings", func() bool { return len(a.eng.State().Delays) >= 9 })

	pages := []struct {
		p    page
		name string
	}{{pageNow, "now"}, {pageSubs, "subs"}, {pageConns, "conns"}, {pageRules, "domains"}, {pageRules, "rules"}, {pageLogs, "logs"}, {pageSettings, "settings"}}
	for _, dark := range []bool{true, false} {
		for _, pg := range pages {
			a.setPage(pg.p)
			a.rulesTab = 1 // the rules text; "domains" is the other tab
			if pg.name == "domains" {
				a.rulesTab = 0
			}
			if pg.p == pageConns && dark {
				// Connections that stay open while the page is drawn.
				for _, host := range []string{"192.0.2.10:443", "192.0.2.11:443", "198.51.100.7:443", "203.0.113.5:443"} {
					conn, err := dial(a.eng.State().MixedPort)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", host, host)
				}
			}
			if pg.p == pageConns {
				wait(t, "connections", func() bool { return len(a.eng.State().Conns) >= 4 })
				a.connSel = a.eng.State().Conns[0].ID
			}
			tt := ui.NewTester(a.view, 1040, 700)
			tt.SetDark(dark)
			tt.SetTitleBar(ui.TitleBar{Height: 28, Left: 76})
			tt.Frame()
			tt.Advance(2 * time.Second) // the arcs on the map have run
			tt.Frame()
			mode := "light"
			if dark {
				mode = "dark"
			}
			f, err := os.Create(filepath.Join(dir, pg.name+"-"+mode+".png"))
			if err != nil {
				t.Fatal(err)
			}
			png.Encode(f, tt.Image())
			f.Close()
		}
	}
	a.setPage(pageNow)
	// The lane picker open, and a gate shut.
	a.expanded["claude"] = true
	a.eng.SetGate("claude", true)
	tt := ui.NewTester(a.view, 1040, 700)
	tt.SetDark(true)
	tt.SetTitleBar(ui.TitleBar{Height: 28, Left: 76})
	tt.Frame()
	f, _ := os.Create(filepath.Join(dir, "now-gate-shut-dark.png"))
	png.Encode(f, tt.Image())
	f.Close()
}
