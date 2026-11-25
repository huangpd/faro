package main

import (
	"context"
	"errors"
	"hash/fnv"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"faro/internal/engine"
)

// The README's screenshots, from made-up data: example nodes, an example exit
// range (198.51.100.0/24 is for documentation), and an exit check and timings
// that are made up here. Off unless asked:
//
//	FARO_SHOTS=docs/screenshots go test -run ReadmeShots
func TestReadmeShots(t *testing.T) {
	dir := os.Getenv("FARO_SHOTS")
	if dir == "" {
		t.Skip("set FARO_SHOTS to a directory")
	}
	os.MkdirAll(dir, 0o755)
	bin, _ := filepath.Abs("bin/sing-box")
	if _, err := os.Stat(bin); err != nil {
		t.Skip("no bin/sing-box")
	}
	res, _ := filepath.Abs("resources")
	t.Setenv("FARO_CORE", bin)
	t.Setenv("HOME", t.TempDir())
	eng, err := engine.New(engine.Options{
		ResourcesDir: res, ProxyRunner: noProxy,
		ExitIP: func(context.Context, string) (string, error) { return "198.51.100.7", nil },
		RTT: func(_ context.Context, tag string) (int, error) {
			h := fnv.New32a()
			h.Write([]byte(tag))
			return 60 + int(h.Sum32()%240), nil
		},
		Probe: func(context.Context, string) (int, error) { return 120, nil }, // it carries a request: 通
		FetchSet: func(context.Context, string, string, int) error { return errors.New("offline") },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	a := newApp(eng)
	withNodes(t, a, "🇬🇧 London 01", "🇬🇧 London 02", "🇩🇪 Frankfurt 01", "🇩🇪 Frankfurt 02",
		"🇯🇵 Tokyo 01", "🇯🇵 Osaka 01", "🇺🇸 Los Angeles 01", "🇸🇬 Singapore 01", "🇳🇱 Amsterdam 01")
	if errs := eng.ApplyRules(`route claude region UK gate ipv4 noudp noipv6 exit:198.51.100.0/24 check:5m
route openai region UK gate ipv4
route google region DE gate ipv4

# What the applications do not say: sent to google, but YouTube above takes it.
google domain:youtube.com
`); errs != nil {
		t.Fatal(errs)
	}
	for _, x := range [][2]string{{"claude", "claude"}, {"claude", "claude-code"}, {"openai", "chatgpt"}, {"openai", "codex"},
		{"google", "google"}, {"google", "gemini"}} {
		if err := eng.AddApp(x[0], x[1]); err != nil {
			t.Fatal(x, err)
		}
	}
	for _, x := range [][3]string{
		{"claude", "出口查询", "ip.example.net full:api.example.org"},
		{"proxy", "YouTube", "youtube.com youtu.be googlevideo.com ytimg.com"},
		{"direct", "中国大陆", "set:geosite-cn"},
	} {
		if err := eng.AddCustomApp(x[0], x[1], x[2]); err != nil {
			t.Fatal(x, err)
		}
	}
	eng.Choose("claude", "🇬🇧 London 02")
	eng.Choose("openai", "🇬🇧 London 02")
	eng.Start()
	wait(t, "running", func() bool { return eng.State().Status == engine.Running })
	wait(t, "the exit check", func() bool { return eng.State().Exits["claude"].OK })
	eng.TestAll()
	wait(t, "timings", func() bool { return len(eng.State().Delays) >= 9 })

	shot := func(name string, w, h int, dark bool, set func()) {
		set()
		tt := ui.NewTester(a.view, w, h)
		tt.SetTitleBar(ui.TitleBar{Height: 28, Left: 76})
		tt.SetDark(dark)
		tt.Frame()
		tt.Frame() // the second frame knows what the first laid out
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		png.Encode(f, tt.Image())
		f.Close()
	}
	shot("now-dark", 1100, 760, true, func() { a.setPage(pageNow) })
	shot("now-light", 1100, 760, false, func() { a.setPage(pageNow) })
	shot("exits", 1100, 1000, false, func() {
		a.setPage(pageRules)
		a.rulesTab = tabExits
		a.exitOpen["claude"], a.exitOpen["openai"] = true, true
		a.startExitEdit(eng.State(), "claude")
	})
	shot("library", 1100, 860, true, func() {
		a.setPage(pageRules)
		a.rulesTab = tabExits
		delete(a.exitEdit, "claude")
		a.exitOpen["openai"] = false
		a.addingTo, a.appQuery = "claude", ""
	})
	shot("order", 1100, 1500, false, func() {
		a.setPage(pageRules)
		a.rulesTab = tabOrder
		a.addingTo = ""
	})
}
