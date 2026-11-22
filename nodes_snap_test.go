package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// the user's real subscription shape, in the order the provider lists it
var realNames = []string{
	"有效期2028-11-06, 剩余 45.01 GB", "荷兰-NL-4-流量倍率:0.1", "爱沙尼亚-EE-1-流量倍率:0.2", "中国香港-HK-1-流量倍率:0.7", "中国香港-HK-2-流量倍率:0.7",
	"中国香港-IPLC-HK-3-流量倍率:1", "中国香港-IPLC-HK-4-流量倍率:1", "荷兰-NL-1-流量倍率:0.5", "荷兰-NL-2-HY2-流量倍率:0.5",
	"日本-TY-1-流量倍率:1.2", "日本-TY-2-流量倍率:1", "日本-TY-3-流量倍率:0.6", "日本-TY-3-2-流量倍率:0.6", "日本-TY-4-HY2-流量倍率:0.6", "日本-TY-5-HY2-流量倍率:1",
	"日本-OS-1-流量倍率:0.6", "日本-OS-2-流量倍率:0.6", "日本-OS-3-流量倍率:1", "日本-FW-JP1-流量倍率:0.6", "日本-FW-JP2-流量倍率:0.6", "日本-FW-JP3-流量倍率:0.6", "日本-FW-JP4-流量倍率:0.6",
	"美国-FW-US1-流量倍率:1", "美国-FW-US2-流量倍率:1", "美国-FW-US3-流量倍率:1", "美国-US-1-流量倍率:1", "美国-US-2-流量倍率:1", "美国-US-3-流量倍率:1",
	"德国-DE-2-流量倍率:1", "德国-DE-3-流量倍率:1", "英国-UK-1-流量倍率:0.9", "新加坡-SG-1-流量倍率:1", "新加坡-SG-2-流量倍率:1", "新加坡-SG-3-1-流量倍率:1", "新加坡-SG-3-2-流量倍率:1",
	"澳大利亚-AU-1-流量倍率:1.6", "澳大利亚-AU-2-流量倍率:1.4", "澳大利亚-AU-3-流量倍率:2", "澳大利亚-FW-AU2-流量倍率:1.4", "中国台湾-TW-1-流量倍率:1.1", "中国台湾-TW-2-流量倍率:1.1",
	"美国-V6-US3-流量倍率:1", "荷兰-V6-NL1-流量倍率:0.5",
}

func TestNodesSnapshot(t *testing.T) {
	dir := os.Getenv("FARO_SNAP")
	if dir == "" {
		t.Skip()
	}
	os.MkdirAll(dir, 0o755)
	a := newTestApp(t)
	withNodes(t, a, realNames...)
	usePreset(t, a, "ai-accounts")
	a.eng.Start()
	wait(t, "running", func() bool { return a.eng.State().Status == "running" })
	a.expanded["proxy"] = true
	tt := ui.NewTester(a.view, 1040, 1100)
	tt.SetDark(true)
	tt.SetTitleBar(ui.TitleBar{Height: 28, Left: 76})
	tt.Frame()
	f, _ := os.Create(filepath.Join(dir, os.Getenv("FARO_NAME")+".png"))
	png.Encode(f, tt.Image())
	f.Close()
}
