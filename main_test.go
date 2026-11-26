package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"faro/internal/clashapi"
	"faro/internal/engine"
	"faro/internal/fixtures"
	"faro/internal/geo"
	"faro/internal/presets"
	"faro/internal/rules"
)

// The view runs without a window in tests, which click and read it as a
// user would. What the app does is the engine's, tested there; these tests
// are about what is drawn, and that a click reaches the engine.

func noProxy(...string) (string, error) { return "", nil }

func newTestApp(t *testing.T) *app {
	t.Helper()
	bin, _ := filepath.Abs("bin/sing-box")
	if _, err := os.Stat(bin); err != nil {
		t.Skip("no bin/sing-box")
	}
	res, _ := filepath.Abs("resources")
	t.Setenv("FARO_CORE", bin)
	t.Setenv("HOME", t.TempDir())
	eng, err := engine.New(engine.Options{ResourcesDir: res, ProxyRunner: noProxy})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	return newApp(eng)
}

func wait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// withNodes gives the app a subscription holding the named nodes. They are
// named by an address, not a name: a name would have to be looked up, over the
// network, by the DNS server in use, and these tests are not about that.
func withNodes(t *testing.T, a *app, names ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("proxies:\n")
	for i, n := range names {
		fmt.Fprintf(&b, "  - {name: %q, type: ss, server: 192.0.2.%d, port: %d, cipher: aes-256-gcm, password: p}\n", n, 1+i%250, 1000+i)
	}
	p := filepath.Join(t.TempDir(), "profile.yaml")
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	a.eng.AddSubscription(p)
	wait(t, "nodes", func() bool { st := a.eng.State(); return st.Busy == "" && len(st.Nodes) == len(names) })
}

// usePreset puts a setup in as the rules: "ai-accounts" is the test setup of
// accounts held to fixed exits; Faro itself ships no such rules.
func usePreset(t *testing.T, a *app, id string) {
	t.Helper()
	if id != "ai-accounts" {
		t.Fatal("no setup", id)
	}
	if errs := a.eng.ApplyRules(fixtures.AIAccounts()); errs != nil {
		t.Fatal(errs)
	}
	a.rulesEdit = a.eng.State().RulesText
}

// view renders the app at a size, after clicking the given texts.
func view(a *app, clicks ...string) *ui.Tester {
	tt := ui.NewTester(a.view, 1040, 700)
	tt.SetTitleBar(ui.TitleBar{Height: 28, Left: 76})
	for _, c := range clicks {
		tt.Click(c)
	}
	return tt
}

func need(t *testing.T, tt *ui.Tester, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !tt.HasText(w) {
			t.Errorf("the screen lacks %q: %q", w, tt.Texts())
		}
	}
}

func TestShellHasEveryPlaceAndTheLocalProxy(t *testing.T) {
	a := newTestApp(t)
	tt := view(a)
	need(t, tt, "Faro", "现在", "订阅", "连接", "规则", "日志", "设置", "未运行", "启动")
	if !tt.HasText(fmt.Sprintf("127.0.0.1:%d", a.eng.State().MixedPort)) {
		t.Error("the proxy address is not shown")
	}
	// Nothing to show yet: the page says what to do.
	need(t, tt, "还没有节点", "添加订阅")
}

func TestNavigationChangesThePage(t *testing.T) {
	a := newTestApp(t)
	for _, c := range []struct {
		click string
		page  page
		shows string
	}{
		{"订阅", pageSubs, "还没有订阅"},
		{"连接", pageConns, "核心未运行"},
		{"规则", pageRules, "规则文本"},
		{"日志", pageLogs, "没有日志"},
		{"设置", pageSettings, "本地代理"},
		{"现在", pageNow, "出口"},
	} {
		tt := view(a, c.click)
		if a.page != c.page {
			t.Fatalf("clicked %q: on page %d, want %d", c.click, a.page, c.page)
		}
		if c.page != pageNow {
			need(t, tt, c.shows)
		}
	}
}

func TestRunningShowsInTheSidebar(t *testing.T) {
	a := newTestApp(t)
	a.eng.Start()
	wait(t, "running", func() bool { return a.eng.State().Status == engine.Running })
	tt := view(a)
	need(t, tt, "运行中", "停止")
	if tt.HasText("启动") {
		t.Error("Start is offered while running")
	}
	// An error is shown where it can be read.
	a.eng.AddSubscription("ftp://nope")
	wait(t, "the error", func() bool { return a.eng.State().Err != "" })
	found := false
	for _, txt := range view(a).Texts() {
		found = found || strings.Contains(txt, "http(s) subscription URL")
	}
	if !found {
		t.Error("the error is not shown")
	}
}

func TestLanesShowWhereEachKindLeavesFrom(t *testing.T) {
	a := newTestApp(t)
	withNodes(t, a, "🇬🇧 London 01", "🇬🇧 London 02", "Ukraine 01", "🇺🇸 LA 01")
	usePreset(t, a, "ai-accounts")
	tt := view(a)
	need(t, tt, "claude", "google", "openai", "proxy", "UK 节点 · 2 个", "放行", "IPv4",
		"没有符合的节点，发往它的流量会被阻断，不会换别的出口")
	// A route with no node is blocked, and says so; it has no gate to offer.
	if tt.HasText("放行 google") {
		t.Error("a blocked lane offers a gate")
	}
	need(t, tt, "🇬🇧 London 01") // the node it is on, by name
}

func TestPickingANodeOnALane(t *testing.T) {
	a := newTestApp(t)
	withNodes(t, a, "🇬🇧 London 01", "🇬🇧 London 02", "🇩🇪 Frankfurt 01")
	usePreset(t, a, "ai-accounts")
	tt := view(a)
	// Open the first lane (claude), choose its second node.
	if err := tt.Click("更换"); err != nil {
		t.Fatal(err)
	}
	if !a.expanded["claude"] {
		t.Fatalf("the lane did not open: %v", a.expanded)
	}
	tt = view(a)
	if err := tt.Click("🇬🇧 London 02"); err != nil {
		t.Fatal(err)
	}
	if got := a.eng.State().Choices["claude"]; got != "🇬🇧 London 02" {
		t.Fatalf("chose %q", got)
	}
}

func TestMainLaneChoosesForEverythingElse(t *testing.T) {
	a := newTestApp(t)
	withNodes(t, a, "HK 01", "JP 02")
	tt := view(a)
	need(t, tt, "proxy", "自动选最快")
	a.expanded["proxy"] = true
	tt = view(a)
	if err := tt.Click("JP 02"); err != nil {
		t.Fatal(err)
	}
	if got := a.eng.State().Choices["proxy"]; got != "JP 02" {
		t.Fatalf("chose %q", got)
	}
}

func TestGateSwitchCutsAndOpensALane(t *testing.T) {
	a := newTestApp(t)
	withNodes(t, a, "🇬🇧 London 01", "🇬🇧 London 02", "🇩🇪 Frankfurt 01")
	usePreset(t, a, "ai-accounts")
	tt := view(a)
	if tt.HasText("已切断") {
		t.Fatal("the gate starts shut")
	}
	if err := tt.Click("放行 claude"); err != nil {
		t.Fatal(err)
	}
	if a.eng.State().Choices["claude#gate"] != "closed" {
		t.Fatalf("gate not shut: %v", a.eng.State().Choices)
	}
	// Said in words, wherever it is seen: the lane, and a mark on the sidebar.
	if tt = view(a); !tt.HasText("已切断") {
		t.Errorf("a shut gate is not said: %q", tt.Texts())
	}
	if !a.anyGateShut(a.eng.State()) {
		t.Error("the sidebar would not mark it")
	}
	if err := view(a).Click("放行 claude"); err != nil {
		t.Fatal(err)
	}
	if a.eng.State().Choices["claude#gate"] != "" || view(a).HasText("已切断") {
		t.Errorf("gate not opened: %v", a.eng.State().Choices)
	}
}

func TestSubscriptionsPage(t *testing.T) {
	a := newTestApp(t)
	withNodes(t, a, "HK 01", "JP 02")
	tt := view(a, "订阅")
	need(t, tt, "profile.yaml", "1 个订阅", "2 个节点", "更新", "添加")
	if tt.HasText("还没有订阅") {
		t.Error("the empty state is shown with a subscription")
	}
}

func TestRulesPageShowsErrorsWithLines(t *testing.T) {
	a := newTestApp(t)
	a.rulesTab = tabText // the rules text
	good := a.eng.State().RulesText
	tt := view(a, "规则")
	need(t, tt, "解锁编辑", "规则文本")
	if tt.HasText("应用") {
		t.Error("the rules text can be applied before it is unlocked")
	}
	tt.Click("解锁编辑")
	view(a, "规则").Click("解锁")
	need(t, view(a, "规则"), "应用", "锁定")

	a.rulesEdit = "route x region ZZ\nnope domain:a.com\n"
	view(a, "规则").Click("应用")
	if len(a.rulesErrs) != 2 || a.rulesErrs[0].Line != 1 {
		t.Fatalf("errors %v", a.rulesErrs)
	}
	if a.eng.State().RulesText != good {
		t.Error("bad text replaced the rules in force")
	}
	found := false
	for _, txt := range view(a, "规则").Texts() {
		found = found || strings.HasPrefix(txt, "line 1: unknown region \"ZZ\"")
	}
	if !found {
		t.Error("the error with its line is not shown")
	}
	need(t, view(a, "规则"), "改动尚未生效")
}

// Faro ships no one's rules: a new install holds the neutral base text, and the
// rules page offers no ready-made setups to replace it with.
func TestRulesPageShipsNoReadyMadeRules(t *testing.T) {
	a := newTestApp(t)
	a.rulesTab = tabText // the rules text
	if a.eng.State().RulesText != presets.Base() {
		t.Error("a new install does not start with the base rules")
	}
	tt := view(a, "规则")
	need(t, tt, "规则文本", "解锁编辑", "复制给 AI 的提示词")
	if tt.HasText("sing-box") {
		t.Error("the generated config is shown without being asked for")
	}
	for _, txt := range tt.Texts() {
		for _, gone := range []string{"预设", "替换为它", "追加", "AI 账号"} {
			if strings.Contains(txt, gone) {
				t.Errorf("the rules page still offers %q (%q)", gone, txt)
			}
		}
	}
}

func TestSettingsSwitchReachesTheEngine(t *testing.T) {
	a := newTestApp(t)
	tt := view(a, "设置")
	need(t, tt, "系统代理", "本地代理端口", "复制终端命令", "立即更新", "sing-box", "数据目录")
	// The loose lines of environment variables are gone: a button holds them.
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, "export ") || strings.Contains(txt, "HTTPS_PROXY") {
			t.Errorf("the settings show the raw command: %q", txt)
		}
	}
	if err := tt.Click("启用系统代理"); err != nil {
		t.Fatal(err)
	}
	if !a.eng.State().SystemProxy {
		t.Error("the system proxy was not turned on")
	}
}

func TestConnectionsListsAndExplains(t *testing.T) {
	a := newTestApp(t)
	a.eng.Start()
	wait(t, "running", func() bool { return a.eng.State().Status == engine.Running })
	view(a, "连接")
	defer a.setPage(pageNow)
	conn, err := dial(a.eng.State().MixedPort)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "CONNECT 192.0.2.77:443 HTTP/1.1\r\nHost: 192.0.2.77:443\r\n\r\n")
	wait(t, "the connection", func() bool {
		for _, k := range a.eng.State().Conns {
			if k.Host == "192.0.2.77" {
				return true
			}
		}
		return false
	})
	tt := view(a)
	need(t, tt, "192.0.2.77:443", "主机", "进程", "出口")
	// The rows are keyed by connection: the key reached the list (set after a
	// list given its rows, it would not have).
	if a.connList.Key == nil {
		t.Error("the connection rows have no key")
	}
	if err := tt.Click("192.0.2.77:443"); err != nil {
		t.Fatal(err)
	}
	tt = view(a)
	need(t, tt, "为什么走了这里", "命中规则", "没有规则点名，走了 proxy", "路径", "断开")
	// Leaving the page stops the asking.
	a.setPage(pageNow)
	time.Sleep(200 * time.Millisecond)
	if n := len(a.eng.State().Conns); n != 0 {
		t.Errorf("%d connections kept after leaving the page", n)
	}
}

func TestRuleText(t *testing.T) {
	for in, want := range map[string]string{"": "没有规则点名，走了 proxy", "final": "没有规则点名，走了 proxy", "domain_suffix=a.com => route(x)": "domain_suffix=a.com => route(x)"} {
		if got := ruleText(in); got != want {
			t.Errorf("ruleText(%q) = %q", in, got)
		}
	}
}

func TestRateText(t *testing.T) {
	for in, want := range map[int64]string{0: "0 B/s", 1023: "1023 B/s", 1024: "1.0 KB/s", 5 << 20: "5.0 MB/s"} {
		if got := rateText(in); got != want {
			t.Errorf("rateText(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestLogLevels(t *testing.T) {
	for in, want := range map[string]int{"x INFO y": 0, "x WARN y": 1, "x ERROR y": 2, "FATAL z": 2} {
		if got := levelOf(in); got != want {
			t.Errorf("levelOf(%q) = %d, want %d", in, got, want)
		}
	}
}

// ---- the tray's menu

func find(items []*mygo.MenuItem, label string) *mygo.MenuItem {
	for _, it := range items {
		if strings.HasPrefix(it.Label, label) {
			return it
		}
	}
	return nil
}

func labels(items []*mygo.MenuItem) []string {
	var out []string
	for _, it := range items {
		switch {
		case it.Type == mygo.MenuItemSeparator:
			out = append(out, "-")
		case it.Role == mygo.RoleQuit:
			out = append(out, "<quit>")
		default:
			out = append(out, it.Label)
		}
	}
	return out
}

func (a *app) items() []*mygo.MenuItem { return a.trayItems(a.eng.State()) }

func TestTrayMenuStoppedWithoutNodes(t *testing.T) {
	a := newTestApp(t)
	got := strings.Join(labels(a.items()), " | ")
	want := "未运行 | - | 启动 | 系统代理 | - | 轻量模式 | 打开 Faro | <quit>"
	if got != want {
		t.Errorf("menu:\n got  %s\n want %s", got, want)
	}
	if it := find(a.items(), "未运行"); it == nil || !it.Disabled {
		t.Error("the state line must not be clickable")
	}
}

func TestTrayMenuWithNodes(t *testing.T) {
	a := newTestApp(t)
	withNodes(t, a, "🇬🇧 London 01", "🇬🇧 London 02", "🇩🇪 Frankfurt 01", "🇺🇸 LA 01")
	usePreset(t, a, "ai-accounts")
	a.eng.Choose("claude", "🇬🇧 London 02")
	items := a.items()

	cut := find(items, "切断 claude")
	if cut == nil || cut.Type != mygo.MenuItemCheckbox || cut.Checked {
		t.Fatalf("gate item %+v", cut)
	}
	if find(items, "切断 google") != nil {
		t.Error("a gate for a route that has none")
	}
	claude := find(items, "claude · 🇬🇧 London 02")
	if claude == nil || len(claude.Submenu) != 2 {
		t.Fatalf("claude submenu %+v", claude)
	}
	for _, n := range claude.Submenu {
		if n.Type != mygo.MenuItemRadio || n.Checked != (n.Label == "🇬🇧 London 02") {
			t.Errorf("claude node %q checked=%v", n.Label, n.Checked)
		}
	}
	if find(items, "google · 🇩🇪 Frankfurt 01") == nil {
		t.Errorf("google submenu missing: %v", labels(items))
	}
	rest := find(items, "proxy · 自动")
	if rest == nil || !rest.Submenu[0].Checked || rest.Submenu[0].Label != "自动" {
		t.Fatalf("main group submenu %+v", rest)
	}
	// By country, not forty in a row: auto, a separator, then a submenu for each country.
	var countries []string
	for _, it := range rest.Submenu[1:] {
		if it.Type != mygo.MenuItemSeparator {
			countries = append(countries, it.Label)
			if len(it.Submenu) == 0 {
				t.Errorf("%q is not a submenu", it.Label)
			}
		}
	}
	if got := strings.Join(countries, "|"); got != "美国 · US|英国 · UK|德国 · DE" {
		t.Errorf("countries in the menu: %s", got)
	}
}

func TestTrayClicksDoTheSameAsTheWindow(t *testing.T) {
	a := newTestApp(t)
	withNodes(t, a, "🇬🇧 London 01", "🇬🇧 London 02", "🇩🇪 Frankfurt 01")
	usePreset(t, a, "ai-accounts")

	// The gate: the item is toggled before Click runs.
	cut := find(a.items(), "切断 claude")
	cut.Checked = true
	cut.Click(cut, nil)
	if a.eng.State().Choices["claude#gate"] != "closed" {
		t.Fatalf("gate not shut: %v", a.eng.State().Choices)
	}
	if c := find(a.items(), "切断 claude"); !c.Checked {
		t.Error("the rebuilt menu does not show the gate shut")
	}
	cut = find(a.items(), "切断 claude")
	cut.Checked = false
	cut.Click(cut, nil)
	if a.eng.State().Choices["claude#gate"] != "" {
		t.Errorf("gate not opened: %v", a.eng.State().Choices)
	}

	// A node of a route, and of the main group, and back to Auto.
	n := find(find(a.items(), "claude").Submenu, "🇬🇧 London 02")
	n.Click(n, nil)
	if a.eng.State().Choices["claude"] != "🇬🇧 London 02" || find(a.items(), "claude · 🇬🇧 London 02") == nil {
		t.Errorf("route node: %v", a.eng.State().Choices)
	}
	n = find(find(find(a.items(), "proxy").Submenu, "德国 · DE").Submenu, "🇩🇪 Frankfurt 01")
	n.Click(n, nil)
	if a.eng.State().Choices["proxy"] != "🇩🇪 Frankfurt 01" {
		t.Errorf("main node: %v", a.eng.State().Choices)
	}
	auto := find(find(a.items(), "proxy · 🇩🇪 Frankfurt 01").Submenu, "自动")
	auto.Click(auto, nil)
	if a.eng.State().Choices["proxy"] != "" {
		t.Errorf("back to auto: %v", a.eng.State().Choices)
	}

	// The system proxy.
	sp := find(a.items(), "系统代理")
	sp.Checked = true
	sp.Click(sp, nil)
	if !a.eng.State().SystemProxy || !find(a.items(), "系统代理").Checked {
		t.Error("system proxy not turned on")
	}
}

func TestTrayStartStopAndState(t *testing.T) {
	a := newTestApp(t)
	if it := find(a.items(), "启动"); it == nil || it.Disabled {
		t.Errorf("stopped: %v", labels(a.items()))
	}
	a.eng.SetSystemProxy(true)
	start := find(a.items(), "启动")
	start.Click(start, nil)
	wait(t, "running", func() bool { return a.eng.State().Status == engine.Running })
	items := a.items()
	if find(items, "停止") == nil || find(items, "启动") != nil || find(items, "运行中 · 系统代理已开") == nil {
		t.Errorf("running: %v", labels(items))
	}
	stop := find(items, "停止")
	stop.Click(stop, nil)
	wait(t, "stopped", func() bool { return a.eng.State().Status == engine.Stopped })
}

// The menu is made again only when what it shows changes: not with every
// second's traffic sample.
func TestMenuSignature(t *testing.T) {
	a := newTestApp(t)
	withNodes(t, a, "🇬🇧 London 01", "🇩🇪 Frankfurt 01")
	usePreset(t, a, "ai-accounts")
	st := a.eng.State()
	base := menuSignature(st)
	st.Rate.Down = 9 << 20
	if menuSignature(st) != base {
		t.Error("the speed changed the menu")
	}
	a.eng.SetGate("claude", true)
	closed := menuSignature(a.eng.State())
	if closed == base {
		t.Error("a closed gate did not change the menu")
	}
	st2 := a.eng.State()
	st2.Status = engine.Running
	if menuSignature(st2) == closed {
		t.Error("the state did not change the menu")
	}
}

func TestShellProxyCommandUsesThePort(t *testing.T) {
	cmd := shellProxyCommand(7123)
	for _, want := range []string{"export ", "http_proxy=http://127.0.0.1:7123", "https_proxy=http://127.0.0.1:7123",
		"all_proxy=socks5h://127.0.0.1:7123", "HTTPS_PROXY=http://127.0.0.1:7123", "ALL_PROXY=socks5h://127.0.0.1:7123"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("the command lacks %q: %s", want, cmd)
		}
	}
	if strings.Contains(cmd, "\n") {
		t.Error("the command is one line, so that it pastes and runs")
	}
	// A bare socks5:// makes a tool look the name up itself, here, in the clear.
	if strings.Contains(cmd, "socks5://") {
		t.Errorf("the command names plain socks5, which leaks the name to the local resolver: %s", cmd)
	}
}

func TestPortFieldChangesThePortAndSaysWhyNot(t *testing.T) {
	a := newTestApp(t)
	old := a.eng.State().MixedPort
	tt := view(a, "设置")
	// A field with the current port, and nothing to apply.
	if a.portText != fmt.Sprint(old) {
		t.Fatalf("the field holds %q, want %d", a.portText, old)
	}
	// An unusable port: refused, with the reason on screen, and the port unchanged.
	a.portEditing, a.portText = true, "80"
	tt = view(a, "设置")
	if err := tt.Click("应用"); err != nil {
		t.Fatal(err)
	}
	if a.portErr == "" || a.eng.State().MixedPort != old {
		t.Errorf("port 80: err %q, port %d", a.portErr, a.eng.State().MixedPort)
	}
	found := false
	for _, txt := range view(a, "设置").Texts() {
		found = found || strings.Contains(txt, "1024")
	}
	if !found {
		t.Error("the reason is not shown")
	}
	// Not a number.
	a.portEditing, a.portText = true, "abc"
	view(a, "设置").Click("应用")
	if a.portErr != "请输入数字" {
		t.Errorf("err %q", a.portErr)
	}
	// A good one.
	good := freePort(t)
	a.portEditing, a.portText = true, fmt.Sprint(good)
	view(a, "设置").Click("应用")
	if a.eng.State().MixedPort != good || a.portErr != "" || a.portEditing {
		t.Errorf("port %d (want %d), err %q", a.eng.State().MixedPort, good, a.portErr)
	}
}

func TestAIPromptCarriesThisSetup(t *testing.T) {
	a := newTestApp(t)
	withNodes(t, a, "🇬🇧 London 01", "🇩🇪 Frankfurt 01", "剩余流量：5 GB")
	usePreset(t, a, "ai-accounts")
	p := a.aiPrompt(a.eng.State())
	for _, want := range []string{"Faro 规则语法", "route claude region UK gate ipv4", "🇬🇧 London 01", "🇩🇪 Frankfurt 01",
		"geosite-cn", "我的需求", "只输出规则文本"} {
		if !strings.Contains(p, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
	// The applications are named on their exits, with their sets.
	if err := a.eng.AddCustomApp("claude", "Orca", "process:Orca orca.example"); err != nil {
		t.Fatal(err)
	}
	if p := a.aiPrompt(a.eng.State()); !strings.Contains(p, "claude | Orca | orca.example process:Orca") {
		t.Errorf("the application is not in the prompt:\n%s", p)
	}
	// A notice is not a server: it is not offered as a node name.
	if strings.Contains(p, "剩余流量") {
		t.Error("a notice is in the node names")
	}
	// The page offers it.
	need(t, view(a, "规则"), "复制给 AI 的提示词")
}

func TestRuleSetsAgeIsSaidPlainly(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		ago   time.Duration
		text  string
		stale bool
	}{
		{10 * time.Second, "规则集：刚刚更新", false},
		{25 * time.Minute, "规则集：25 分钟前更新", false},
		{5 * time.Hour, "规则集：5 小时前更新", false},
		{2 * 24 * time.Hour, "规则集：2 天前更新", false},
		{3*24*time.Hour + time.Hour, "规则集：3 天前更新", false},
		{4*24*time.Hour + time.Hour, "规则集：4 天没有更新成功", true},
		{6 * 24 * time.Hour, "规则集：6 天没有更新成功", true},
	} {
		text, stale := ruleSetsAge(now.Add(-c.ago), now)
		if text != c.text || stale != c.stale {
			t.Errorf("%v ago: %q stale=%v, want %q stale=%v", c.ago, text, stale, c.text, c.stale)
		}
	}
	if text, stale := ruleSetsAge(time.Time{}, now); !stale || text != "规则集：还没有" {
		t.Errorf("none: %q %v", text, stale)
	}
}

func TestSettingsSaysWhenTheRuleSetsWereUpdated(t *testing.T) {
	a := newTestApp(t)
	tt := view(a, "设置")
	need(t, tt, "立即更新")
	found := false
	for _, txt := range tt.Texts() {
		found = found || (strings.HasPrefix(txt, "规则集：") && strings.HasSuffix(txt, "更新"))
	}
	if !found {
		t.Errorf("no line saying when the rule sets were updated: %q", tt.Texts())
	}
}

func TestShorteningNamesInACountryGroup(t *testing.T) {
	jp := []string{"日本-TY-1-流量倍率:1.2", "日本-TY-2-流量倍率:1", "日本-TY-3-2-流量倍率:0.6", "日本-FW-JP1-流量倍率:0.6"}
	if p := commonPrefix(jp); p != "日本-" {
		t.Errorf("prefix %q", p)
	}
	for tag, want := range map[string][2]string{
		"日本-TY-1-流量倍率:1.2":     {"TY-1", "1.2"},
		"日本-TY-3-2-流量倍率:0.6":   {"TY-3-2", "0.6"},
		"日本-FW-JP1-流量倍率:0.6":   {"FW-JP1", "0.6"},
		"日本-TY-4-HY2-流量倍率:0.6": {"TY-4-HY2", "0.6"},
	} {
		if n, r := chipLabel(tag, "日本-"); n != want[0] || r != want[1] {
			t.Errorf("chipLabel(%q) = %q, %q; want %q, %q", tag, n, r, want[0], want[1])
		}
	}
	// Other providers' notes of a rate, and other separators.
	for tag, want := range map[string][2]string{
		"🇬🇧英国伦敦01|CTCU|0.1x": {"🇬🇧英国伦敦01|CTCU", "0.1"},
		"Tokyo 01 x0.5":      {"Tokyo 01", "0.5"},
		"Tokyo 01 ×2":        {"Tokyo 01", "2"},
		"Tokyo 01":           {"Tokyo 01", ""},
		"HK-1-倍率:3":          {"HK-1", "3"},
	} {
		if n, r := chipLabel(tag, ""); n != want[0] || r != want[1] {
			t.Errorf("chipLabel(%q) = %q, %q; want %q, %q", tag, n, r, want[0], want[1])
		}
	}
	// A name that only looks like it ends in a rate is not cut.
	for _, tag := range []string{"HK-x", "Node x", "Tokyo2x3y", "US-10"} {
		if n, r := chipLabel(tag, ""); n != tag || r != "" {
			t.Errorf("chipLabel(%q) = %q, %q; want it whole", tag, n, r)
		}
	}
	// No common start: nothing is cut. A group of one: whole. A prefix that is a part of a word: not cut.
	if p := commonPrefix([]string{"Tokyo 01", "Osaka 02"}); p != "" {
		t.Errorf("prefix %q", p)
	}
	if p := commonPrefix([]string{"Tokyo 01"}); p != "" {
		t.Errorf("prefix of one %q", p)
	}
	if p := commonPrefix([]string{"JP1", "JP2"}); p != "" {
		t.Errorf("half a word was cut: %q", p)
	}
	if p := commonPrefix([]string{"Tokyo 01 a", "Tokyo 02 a"}); p != "Tokyo " {
		t.Errorf("prefix %q", p)
	}
	// If cutting leaves nothing, the name is whole.
	if n, _ := chipLabel("日本-", "日本-"); n != "日本-" {
		t.Errorf("%q", n)
	}
	if n, r := chipLabel("日本-流量倍率:2", "日本-"); n != "日本-流量倍率:2" && !(n != "" && r == "2") {
		t.Errorf("%q %q", n, r)
	}
}

// ---- picking a node

// The real subscription's shape: a notice first, countries mixed.
func openWithRealNodes(t *testing.T) *app {
	a := newTestApp(t)
	withNodes(t, a, realNames...)
	a.eng.Start()
	wait(t, "running", func() bool { return a.eng.State().Status == engine.Running })
	a.expanded["proxy"] = true
	return a
}

func TestNodesAreListedByCountryInOrder(t *testing.T) {
	a := openWithRealNodes(t)
	tt := view(a)
	// The countries are headed, with their counts, in the order of the list.
	var heads []string
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, " · ") && len(txt) < 20 {
			heads = append(heads, txt)
		}
	}
	want := []string{"香港 · HK", "日本 · JP", "新加坡 · SG", "台湾 · TW", "美国 · US", "英国 · UK", "德国 · DE", "荷兰 · NL", "澳大利亚 · AU", "爱沙尼亚 · EE"}
	at := -1
	for _, w := range want {
		found := -1
		for i, h := range heads {
			if h == w {
				found = i
			}
		}
		if found < 0 {
			t.Errorf("no heading %q in %v", w, heads)
		} else if found < at {
			t.Errorf("%q is out of order in %v", w, heads)
		} else {
			at = found
		}
	}
	// The names are shortened in their country (the country is the heading), with the
	// multiplier apart. The whole name stays where it is wanted: the chip's label, for
	// a reader of the screen, and its tooltip. Seen on the screen it is shortened (the
	// renders show it); here it is counted: those two places, and no third that is the
	// visible text.
	need(t, tt, "TY-1", "×1.2", "HK-1", "×0.7")
	whole := 0
	for _, txt := range tt.Texts() {
		if txt == "日本-TY-1-流量倍率:1.2" {
			whole++
		}
	}
	if whole > 2 {
		t.Errorf("the whole name appears %d times: a third is the visible text", whole)
	}
}

// A usage notice is not a server: it is said once, and cannot be picked.
func TestANoticeCannotBePicked(t *testing.T) {
	a := openWithRealNodes(t)
	tt := view(a)
	need(t, tt, "有效期2099-01-01, 剩余 100.00 GB") // said, as a message
	before := a.eng.State().Choices["proxy"]
	tt.Click("有效期2099-01-01, 剩余 100.00 GB")
	if a.eng.State().Choices["proxy"] != before {
		t.Error("a notice was chosen as the exit")
	}
	for _, g := range a.eng.State().NodeGroups {
		for _, n := range g.Nodes {
			if strings.HasPrefix(n.Tag, "有效期") {
				t.Error("a notice is in a country's list of nodes")
			}
		}
	}
}

// Choosing a node is testing it: no button, the click does both.
func TestClickingANodeChoosesAndTestsIt(t *testing.T) {
	a := openWithRealNodes(t)
	st := a.eng.State()
	if len(st.Delays) != 0 || st.Choices["proxy"] != "" {
		t.Fatal("something was chosen or tested before any click")
	}
	// A tall window: the chips are under the lane's map, and this one is in the second country.
	tall := func() *ui.Tester {
		tt := ui.NewTester(a.view, 1040, 1000)
		tt.SetTitleBar(ui.TitleBar{Height: 28, Left: 76})
		return tt
	}
	tt := tall()
	if err := tt.Click("日本-TY-1-流量倍率:1.2"); err != nil { // the chip's label is the full name
		t.Fatal(err)
	}
	if got := a.eng.State().Choices["proxy"]; got != "日本-TY-1-流量倍率:1.2" {
		t.Fatalf("chose %q", got)
	}
	// It is tested: shown as under test, then it has an answer. Only that one.
	wait(t, "the node's test", func() bool { _, ok := a.eng.State().Delays["日本-TY-1-流量倍率:1.2"]; return ok })
	if n := len(a.eng.State().Delays); n != 1 {
		t.Errorf("%d nodes tested, want only the one clicked: %v", n, a.eng.State().Delays)
	}
	// The answer is on the chip: a number, or that it does not answer (these nodes are not real).
	tt = tall()
	answered := false
	for _, txt := range tt.Texts() {
		answered = answered || txt == "不通"
	}
	if !answered {
		t.Errorf("the chip does not show the answer: %q", tt.Texts())
	}
	// Clicking another changes the choice and tests that one too; the first stays as it was.
	if err := tall().Click("日本-TY-2-流量倍率:1"); err != nil {
		t.Fatal(err)
	}
	wait(t, "the second", func() bool { _, ok := a.eng.State().Delays["日本-TY-2-流量倍率:1"]; return ok })
	if a.eng.State().Choices["proxy"] != "日本-TY-2-流量倍率:1" || len(a.eng.State().Delays) != 2 {
		t.Errorf("choice %q, delays %v", a.eng.State().Choices["proxy"], a.eng.State().Delays)
	}
	// There is no button for testing one node.
	for _, txt := range view(a).Texts() {
		if strings.Contains(txt, "测试此节点") || strings.Contains(txt, "单独测速") {
			t.Errorf("a button for testing one node: %q", txt)
		}
	}
}

func TestTrayClickingANodeAlsoTestsIt(t *testing.T) {
	a := openWithRealNodes(t)
	jp := find(find(a.items(), "proxy").Submenu, "日本 · JP")
	if jp == nil {
		t.Fatalf("no Japan submenu in %v", labels(a.items()))
	}
	n := find(jp.Submenu, "日本-TY-1-流量倍率:1.2")
	n.Click(n, nil)
	wait(t, "the test", func() bool { _, ok := a.eng.State().Delays["日本-TY-1-流量倍率:1.2"]; return ok })
	if a.eng.State().Choices["proxy"] != "日本-TY-1-流量倍率:1.2" {
		t.Error("not chosen")
	}
	// The menu says where the current one is.
	if find(find(a.items(), "proxy").Submenu, "日本 · JP ●") == nil {
		t.Errorf("the menu does not show which country holds the choice")
	}
}

func TestSettingsShowsDNSAndChoosingReachesTheEngine(t *testing.T) {
	a := newTestApp(t)
	need(t, view(a, "设置"), "DNS", "国内网站", "其他网站", "阿里 AliDNS", "Google")
	// A choice reaches the engine, and keeps the other side as it was.
	a.setDNS("dnspod", "")
	if st := a.eng.State(); st.DNS.Domestic != "dnspod" || st.DNS.Foreign != "google" {
		t.Errorf("chose %+v: the side left empty is the first, google", st.DNS)
	}
	a.setDNS("dnspod", "cloudflare")
	if st := a.eng.State(); st.DNS.Foreign != "cloudflare" || st.DNS.Domestic != "dnspod" {
		t.Errorf("chose %+v", st.DNS)
	}
	need(t, view(a, "设置"), "腾讯 DNSPod", "Cloudflare")
}

// The screens say what is so, not how things work. A sentence (a clause, a full
// stop) on a page that is for setting or for looking is a note nobody asked for:
// it goes in the tooltip or it is not written. What is allowed is what a person
// must read: an error, a warning, and a path.
func TestPagesCarryNoExplanatorySentences(t *testing.T) {
	// Twice: with things to show, and with nothing, where the empty states are.
	full := newTestApp(t)
	withNodes(t, full, "🇬🇧 London 01", "🇩🇪 Frankfurt 01")
	usePreset(t, full, "ai-accounts")
	if err := full.eng.NewGroup("AI 应用", "claude", "claude.ai chatgpt.com"); err != nil {
		t.Fatal(err)
	}
	empty := newTestApp(t)
	for name, a := range map[string]*app{"with data": full, "empty": empty} {
		for _, pg := range []string{"现在", "订阅", "连接", "规则", "日志", "设置"} {
			for _, txt := range view(a, pg).Texts() {
				// a path is long and has no sentence; the rules are the person's own text
				if strings.Contains(txt, "/") || strings.HasPrefix(txt, "route ") {
					continue
				}
				if strings.ContainsAny(txt, "，。；") && len([]rune(txt)) > 8 {
					// the one allowed kind: a lane blocked for want of a node, which must be read
					if strings.Contains(txt, "会被阻断") {
						continue
					}
					t.Errorf("%s, page %q has a sentence: %q", name, pg, txt)
				}
			}
		}
	}
}

// apps draws the rules page on the exits, with these exit cards open, and
// these applications open to be edited, in a window tall enough for them all.
func apps(t *testing.T, a *app, exits []string, open ...string) *ui.Tester {
	t.Helper()
	a.setPage(pageRules)
	a.rulesTab = tabExits
	for _, e := range exits {
		a.exitOpen[e] = true
	}
	for _, g := range open {
		a.appOpen[g] = true
	}
	return tall(t, a)
}

// The rules page has three tabs: the applications are under their exits, not
// on a page of their own.
func TestTheRulesPageHasThreeTabs(t *testing.T) {
	a := newTestApp(t)
	tt := view(a, "规则")
	need(t, tt, "出口", "最终顺序", "规则文本")
	if tt.HasText("我的域名") {
		t.Error("the sites tab is still there")
	}
	tt = view(a, "规则", "规则文本")
	need(t, tt, "解锁编辑", "查看生成的配置")
	need(t, view(a, "规则", "规则文本", "查看生成的配置"), "sing-box", "隐藏生成的配置")
}

// Each application is under its exit, with its priority; what the numbers are
// is said once above.
func TestApplicationsAreShownUnderTheirExitsWithTheirPriority(t *testing.T) {
	a := newTestApp(t)
	withNodes(t, a, "🇬🇧 London 01", "🇩🇪 Frankfurt 01")
	usePreset(t, a, "ai-accounts")
	if err := a.eng.NewGroup("AI 应用", "claude", "claude.ai chatgpt.com"); err != nil {
		t.Fatal(err)
	}
	if err := a.eng.NewGroup("视频", "proxy", "youtube.com"); err != nil {
		t.Fatal(err)
	}
	tt := apps(t, a, []string{"claude", "proxy"})
	need(t, tt, "#1", "AI 应用", "claude.ai · chatgpt.com", "#2", "视频", "youtube.com", "自定义",
		"应用前的 #编号 是优先级：同一个网站出现在几个应用里时，编号小的生效。可以在应用右侧的 ⋯ 里上移、下移来调整。")
	// One application: nothing to order, nothing said.
	b := newTestApp(t)
	b.eng.NewGroup("视频", "proxy", "youtube.com")
	if apps(t, b, nil).HasText("#编号 是优先级") {
		t.Error("the priorities are explained with one application")
	}
}

// An application's ⋯ moves it up and down, to another exit, or out; a click
// on it opens it to be edited where it is.
func TestAnApplicationIsMovedAndRemovedFromItsMenu(t *testing.T) {
	a := newTestApp(t)
	a.eng.NewGroup("甲", "direct", "a.example.com")
	a.eng.NewGroup("乙", "direct", "b.example.com")
	names := func() string {
		var out []string
		for _, g := range a.eng.State().Groups {
			out = append(out, g.Name+">"+g.Target)
		}
		return strings.Join(out, " ")
	}
	menu := func(name string, path ...string) {
		t.Helper()
		tt := apps(t, a, []string{"direct", "proxy"})
		if err := tt.Click(name + "的更多操作"); err != nil {
			t.Fatal(err)
		}
		if err := tt.ChooseMenuItem(path...); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	menu("乙", "上移（优先级提高）")
	if names() != "乙>direct 甲>direct" {
		t.Errorf("up: %s", names())
	}
	tt := apps(t, a, []string{"direct"})
	if err := tt.Click("乙的更多操作"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("上移（优先级提高）"); err == nil {
		t.Error("the first could be moved up")
	}
	tt.CloseMenu()
	menu("乙", "下移（优先级降低）")
	if names() != "甲>direct 乙>direct" {
		t.Errorf("down: %s", names())
	}
	menu("乙", "移到其他出口", "proxy")
	if names() != "甲>direct 乙>proxy" || !a.exitOpen["proxy"] {
		t.Errorf("moved: %s", names())
	}
	// Opened by a click, it is edited where it is.
	tt = apps(t, a, []string{"direct"})
	if tt.HasText("甲的内容") || tt.HasText("甲的规则集搜索") {
		t.Error("open before it was clicked")
	}
	if err := tt.Click("甲"); err != nil {
		t.Fatal(err)
	}
	tt = apps(t, a, []string{"direct"})
	need(t, tt, "甲的内容", "甲的规则集搜索")
	if tt.HasText("乙的内容") {
		t.Error("another application opened with it")
	}
	// Removed, after asking.
	menu("甲", "删除")
	need(t, apps(t, a, nil), "移除 甲？")
	tall(t, a, "移除")
	if names() != "乙>proxy" {
		t.Errorf("removed: %s", names())
	}
}

// From a connection: the site is put in an application with a click, as the
// whole domain, or a new one is begun under an exit with the site in it.
func TestAConnectionsSiteCanBeAddedToAnApplication(t *testing.T) {
	a := newTestApp(t)
	a.eng.NewGroup("视频", "proxy", "")
	view(a) // sets the look
	st := a.eng.State()
	draw := func(host string) *ui.Tester {
		return ui.NewTester(func(c *ui.Context) { a.addFromConn(c, st, host) }, 700, 80)
	}
	tt := draw("r3---sn-abc.googlevideo.com")
	need(t, tt, "加入应用", "googlevideo.com", "视频", "新建应用")
	if err := tt.Click("视频"); err != nil {
		t.Fatal(err)
	}
	if g := a.eng.State().Groups[0].Domains; len(g) != 1 || g[0] != "googlevideo.com" {
		t.Errorf("sites %v", g)
	}
	// An address is not a name: nothing to add.
	if draw("192.0.2.7").HasText("加入应用") {
		t.Error("an IP address was offered as a site")
	}
	// A new one: the exit is chosen, and its「添加应用」opens with the site written in.
	b := newTestApp(t)
	view(b)
	tt = ui.NewTester(func(c *ui.Context) { b.addFromConn(c, b.eng.State(), "www.example.org") }, 700, 80)
	if err := tt.Click("新建应用"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("在 直连 下新建"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if b.page != pageRules || b.rulesTab != tabExits || b.addingTo != "direct" || !b.exitOpen["direct"] || b.customText != "example.org" {
		t.Errorf("page %v tab %d adding to %q, text %q", b.page, b.rulesTab, b.addingTo, b.customText)
	}
	need(t, tall(t, b), "自己写")
}

// Light mode: the window stays shut, the core and the menu bar go on.
func TestLightModeFromTheSettingsAndTheMenuBar(t *testing.T) {
	a := newTestApp(t)
	need(t, view(a, "设置"), "轻量模式")
	if a.eng.State().LightMode {
		t.Fatal("light mode is on from the start")
	}
	// The switch in the settings.
	if err := view(a, "设置").Click("启用轻量模式"); err != nil {
		t.Fatal(err)
	}
	if !a.eng.State().LightMode {
		t.Fatal("the switch did not turn light mode on")
	}
	// The menu bar has it, checked, and can turn it off without the window.
	it := find(a.items(), "轻量模式")
	if it == nil || !it.Checked || it.Type != mygo.MenuItemCheckbox {
		t.Fatalf("the menu bar's item: %+v", it)
	}
	it.Checked = false
	it.Click(it, nil)
	if a.eng.State().LightMode {
		t.Error("the menu bar item did not turn it off")
	}
	// The menu is rebuilt when it changes.
	off := menuSignature(a.eng.State())
	a.eng.SetLightMode(true)
	if menuSignature(a.eng.State()) == off {
		t.Error("the state changed and the menu did not")
	}
	// The window is not opened at the start while it is on, nor in the background.
	for _, c := range []struct {
		light, background, opens bool
	}{{false, false, true}, {true, false, false}, {false, true, false}, {true, true, false}} {
		st := engine.State{LightMode: c.light}
		if got := opensWindowAtStart(st, c.background); got != c.opens {
			t.Errorf("light %v background %v: opens %v, want %v", c.light, c.background, got, c.opens)
		}
	}
	// "Open Faro" is still in the menu bar: light mode is not being locked out.
	if find(a.items(), "打开 Faro") == nil {
		t.Error("the menu bar cannot open the window")
	}
}

// The menu bar's speed does not move what is beside it: one unit, one width,
// and it changes no more than every two seconds.
func TestMenuRateIsOneUnitAndOneWidth(t *testing.T) {
	for in, want := range map[float64]string{0: "0.00 MB/s", 500: "0.00 MB/s", 20 << 10: "0.02 MB/s", 1 << 20: "1.00 MB/s",
		1.5 * (1 << 20): "1.50 MB/s", 12.34 * (1 << 20): "12.3 MB/s", 99.9 * (1 << 20): "99.9 MB/s", 250 * (1 << 20): "250 MB/s"} {
		if got := menuRate(in); got != want {
			t.Errorf("menuRate(%v) = %q, want %q", in, got, want)
		}
	}
	// From nothing to ninety-nine MB/s the title is as wide as it was.
	width := -1
	for bps := 0.0; bps < 99.9*(1<<20); bps += 7777 {
		got := menuRate(bps)
		if !strings.HasSuffix(got, " MB/s") {
			t.Fatalf("%v: %q is not MB/s", bps, got)
		}
		if width < 0 {
			width = len([]rune(got))
		} else if len([]rune(got)) != width {
			t.Fatalf("menuRate(%v) = %q: the width changed from %d", bps, got, width)
		}
	}
}

func TestMenuTitleChangesNoMoreThanEveryTwoSeconds(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	running := func(downs ...int64) engine.State {
		st := engine.State{Status: engine.Running}
		for _, d := range downs {
			st.History = append(st.History, clashapi.Traffic{Down: d})
		}
		st.Rate.Down = downs[len(downs)-1]
		return st
	}
	if got := menuTitle(engine.State{Status: engine.Stopped}, "1.00 MB/s", t0, t0); got != "" {
		t.Errorf("a core that is not running: %q", got)
	}
	first := menuTitle(running(1<<20, 3<<20), "", time.Time{}, t0) // the mean of the last two seconds
	if first != "2.00 MB/s" {
		t.Fatalf("first %q", first)
	}
	// A second later the speed is very different; the title keeps what it said.
	if got := menuTitle(running(50<<20), first, t0, t0.Add(time.Second)); got != first {
		t.Errorf("changed after a second: %q", got)
	}
	// Two seconds on, it follows.
	if got := menuTitle(running(4<<20, 4<<20), first, t0, t0.Add(2*time.Second)); got != "4.00 MB/s" {
		t.Errorf("after two seconds: %q", got)
	}
}

func TestRulesTextIsColoredByWhatEachWordIs(t *testing.T) {
	kinds := func(line string) map[string]editor.TokenKind {
		toks, _ := tokenizeRules(line, 0, nil)
		got, from := map[string]editor.TokenKind{}, 0
		for _, k := range toks {
			got[strings.TrimSpace(line[from:k.End])] = k.Kind
			from = k.End
		}
		return got
	}
	g := kinds(`route uk region GB gate ipv4 # note`)
	for word, want := range map[string]editor.TokenKind{
		"route": editor.TokenKeyword, "uk": editor.TokenVariable, "region": editor.TokenKeyword,
		"gate": editor.TokenConstant, "ipv4": editor.TokenConstant, "# note": editor.TokenComment,
	} {
		if g[word] != want {
			t.Errorf("%q is kind %v, want %v (%v)", word, g[word], want, g)
		}
	}
	g = kinds(`uk domain:a.com full:"b c"`)
	if g["uk"] != editor.TokenFunction || g["domain"] != editor.TokenProperty || g["a.com"] != editor.TokenString {
		t.Errorf("a rule line: %v", g)
	}
	if g := kinds(`regex#x # c`); g["regex#x"] != editor.TokenFunction || g["# c"] != editor.TokenComment {
		t.Errorf("a # inside a word is not a comment: %v", g)
	}
}

func TestRulesEditorKeepsTheTextAndMarksMistakes(t *testing.T) {
	a := newTestApp(t)
	a.rulesTab, a.rulesOpen = tabText, true
	a.rulesEdit = "route x region ZZ\nnope domain:a.com\n"
	view(a, "规则").Click("应用")
	view(a, "规则")
	if got := a.rulesCode.Buffer.String(); got != a.rulesEdit {
		t.Errorf("editor holds %q, text is %q", got, a.rulesEdit)
	}
	if n := len(a.rulesCode.Diagnostics()); n != 2 {
		t.Errorf("%d mistakes marked, want 2", n)
	}
	a.rulesCode.Buffer.Set("direct domain:b.com\n") // typed in the editor
	view(a, "规则")
	if a.rulesEdit != "direct domain:b.com\n" {
		t.Errorf("typing did not reach the text: %q", a.rulesEdit)
	}
}

func TestRuleSetFoundByNameAndAddedToAnApplication(t *testing.T) {
	a := newTestApp(t)
	if err := a.eng.NewGroup("AI", "direct", "example.com"); err != nil {
		t.Fatal(err)
	}
	a.rulesTab, a.exitOpen["direct"], a.appOpen["AI"] = tabExits, true, true
	tt := view(a, "规则")
	if tt.HasText("没有匹配的规则集") || tt.HasText("已在应用里") {
		t.Error("nothing is searched for, and nothing is shown")
	}

	q := "gemini"
	a.setQuery["AI"] = &q
	tt = view(a, "规则")
	need(t, tt, "google-gemini", "添加")
	for _, txt := range tt.Texts() {
		// The search's results, not the rules text's own lines under the exit.
		if strings.HasPrefix(txt, "geosite-") {
			t.Errorf("the shared prefix is shown: %q", txt)
		}
	}
	tt.Click("添加")
	wait(t, "the set in the group", func() bool { return len(a.eng.State().Groups[0].Sets) == 1 })
	if a.eng.State().Groups[0].Sets[0] != "geosite-google-gemini" || q != "" {
		t.Errorf("sets %v, search %q", a.eng.State().Groups[0].Sets, q)
	}
	tt = view(a, "规则")
	need(t, tt, "google-gemini") // held: a chip now, with its size
	found := false
	for _, txt := range tt.Texts() {
		found = found || strings.HasSuffix(txt, " KB")
	}
	if !found {
		t.Error("the set's size is not shown")
	}

	q = "zzzz-nothing"
	need(t, view(a, "规则"), "没有匹配的规则集")
	a.eng.RemoveSet("AI", "geosite-google-gemini")
	if tt := view(a, "规则"); tt.HasText("google-gemini") {
		t.Error("a removed set is still shown")
	}
}

func TestOverlapsAreShownOnBothApplicationsAndASiteSaysWhichSetsHoldIt(t *testing.T) {
	a := newTestApp(t)
	a.eng.NewGroup("先", "direct", "")
	a.eng.NewGroup("后", "proxy", "ai.google.dev")
	if err := a.eng.AddSet("先", "geosite-google-gemini"); err != nil {
		t.Fatal(err)
	}
	wait(t, "the overlap", func() bool { return len(a.eng.State().Overlaps) == 1 })
	tt := apps(t, a, []string{"direct", "proxy"}, "先", "后")
	need(t, tt, "与「后」#2 重叠 1 处，如 ai.google.dev；本应用排在前面，由它决定",
		"与「先」#1 重叠 1 处，如 ai.google.dev；「先」排在前面，由它决定")
	// Only the one behind is offered to go in front, and it goes there in one move.
	if tt.HasText("让 先 排到 后 前面") {
		t.Error("the one in front was offered to go in front")
	}
	if err := tt.Click("让 后 排到 先 前面"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	wait(t, "the overlap again", func() bool { o := a.eng.State().Overlaps; return len(o) == 1 && o[0].Winner == "后" })
	if g := a.eng.State().Groups; g[0].Name != "后" {
		t.Errorf("not put in front: %+v", g)
	}
	need(t, apps(t, a, nil), "与「先」#2 重叠 1 处，如 ai.google.dev；本应用排在前面，由它决定")

	var held string
	wait(t, "the sets read", func() bool {
		held = a.heldBy(a.eng.State(), "ai.google.dev")
		return held != ""
	})
	if !strings.Contains(held, "google-gemini（应用 先）") || strings.Contains(held, "geosite-") {
		t.Errorf("held by %q", held)
	}
	if a.heldBy(a.eng.State(), "192.0.2.7") != "" {
		t.Error("an address was looked up as a name")
	}
}

func TestLanesShowWhereTheirExitIs(t *testing.T) {
	a := newTestApp(t)
	withNodes(t, a, "🇬🇧 London 01", "🇩🇪 Frankfurt 01", "🇭🇰 Hong Kong 01", "Mystery Node")
	usePreset(t, a, "ai-accounts")
	st := a.eng.State()
	var claude, google *rules.Route
	for i := range st.Plan.Routes {
		switch st.Plan.Routes[i].ID {
		case "claude":
			claude = &st.Plan.Routes[i]
		case "google":
			google = &st.Plan.Routes[i]
		}
	}
	uk, _ := geo.Place("UK")
	de, _ := geo.Place("DE")
	if p, id := laneExitPlace(st, *claude); p == nil || *p != uk || id != "UK" {
		t.Errorf("claude's exit is at %v (%s), want London %v", p, id, uk)
	}
	if p, id := laneExitPlace(st, *google); p == nil || *p != de || id != "DE" {
		t.Errorf("google's exit is at %v (%s), want Germany %v", p, id, de)
	}
	// A gate that is shut, or no node, is no place to go.
	shut := *claude
	shut.Closed = true
	if p, _ := laneExitPlace(st, shut); p != nil {
		t.Error("a shut gate has an exit on the map")
	}
	if p, _ := laneExitPlace(st, rules.Route{ID: "none"}); p != nil {
		t.Error("a route with no nodes has an exit on the map")
	}
	// A node of no country, alone, has no place; a delay picks the fastest of several.
	if p, ok := exitPlace(st, "Mystery Node", nil); ok {
		t.Errorf("a node of no country has a place: %v", p)
	}
	hk, _ := geo.Place("HK")
	st.Delays = map[string]int{"🇬🇧 London 01": 300, "🇭🇰 Hong Kong 01": 40}
	if p, ok := exitPlace(st, "", []string{"🇬🇧 London 01", "🇭🇰 Hong Kong 01"}); !ok || p != hk {
		t.Errorf("fastest is Hong Kong: %v", p)
	}
	if main, _ := mainExitPlace(st); main == nil {
		t.Error("the main lane has no exit on the map")
	}

	// The page draws, with the maps, in either state.
	tt := view(a, "现在")
	need(t, tt, "claude", "google")
	a.eng.SetGate("claude", true)
	view(a, "现在")
	if tt.Image() == nil {
		t.Error("nothing drawn")
	}
}

func TestTheLightRunsEveryFewSecondsAndTheMapRestsBetween(t *testing.T) {
	a := newTestApp(t)
	withNodes(t, a, "🇬🇧 London 01", "🇩🇪 Frankfurt 01")
	usePreset(t, a, "ai-accounts")
	tt := ui.NewTester(a.view, 1040, 700)
	tt.SetTitleBar(ui.TitleBar{Height: 28, Left: 76})
	tt.Frame()
	if !tt.FramePending() {
		t.Error("the light is not running as the map first shows")
	}
	// Between runs the window asks for nothing but the next run.
	tt.Advance(mapRunTime + time.Second)
	tt.Frame()
	if tt.FramePending() {
		t.Error("the map asks for frames between runs")
	}
	tt.Frame()
	if tt.FramePending() {
		t.Error("a redraw at rest started a run")
	}
	// And at the next run it flows again.
	tt.Advance(mapEvery - (mapRunTime + time.Second) + 200*time.Millisecond) // just past the next run's start
	tt.Frame()
	if !tt.FramePending() {
		t.Error("the light did not run again")
	}
}

func TestEveryArcIsOneLineAndTheMapClipsWhatRunsOffIt(t *testing.T) {
	const left, width, height = float32(100), float32(250), float32(100)
	here, _ := geo.Place("HK")
	w := geo.WorldFrom(here, mapOrigin, mapSpan)
	ends := func(to geo.Point) (pts [][2]float32, ex float32) {
		fx, fy := w.XY(here)
		_, ty := w.XY(to)
		ax := left + float32(fx)*width
		ex = ax + float32(w.Reach(here, to))*width
		return mapArc(ax, float32(fy)*height, ex, float32(ty)*height, 40), ex
	}
	// To the UK, west: one line, from here at the middle to the UK to the left of it.
	uk, _ := geo.Place("UK")
	pts, ex := ends(uk)
	runs := visibleRuns(pts, left, width)
	if len(runs) != 1 || len(runs[0]) != len(pts) {
		t.Fatalf("an arc within the map is %d lines of %d points, want 1 of %d", len(runs), len(runs[0]), len(pts))
	}
	if start := pts[0][0]; math.Abs(float64(start-(left+width*mapOrigin))) > 0.01 {
		t.Errorf("starts at %v, not where this Mac is", start)
	}
	if ex >= pts[0][0] || ex < left || lastVisible(pts, left, width) != len(pts)-1 {
		t.Errorf("the UK is at %v, from %v", ex, pts[0][0])
	}
	// Every exit is on the map, and its arc is one line, whichever way round the
	// Earth it is the short way: the UK and the US are on either side.
	for _, id := range []string{"DE", "NL", "FR", "JP", "SG", "HK", "KR", "AU", "IN", "US", "BR", "CA", "ZA"} {
		p, _ := geo.Place(id)
		pts, x := ends(p)
		if x < left || x > left+width {
			t.Errorf("%s is at %v: off the map", id, x)
		}
		if runs := visibleRuns(pts, left, width); len(runs) != 1 || len(runs[0]) != len(pts) {
			t.Errorf("%s: the arc is %d lines", id, len(runs))
		}
	}
	us, _ := geo.Place("US")
	if _, x := ends(us); x <= left+width/2 {
		t.Errorf("the US is at %v: it is east of here, to the right", x)
	}
	// Were the map narrower, an arc runs off its edge and the line stops there.
	pts = [][2]float32{{50, 10}, {90, 5}, {130, 5}, {170, 10}, {210, 20}}
	runs = visibleRuns(pts, 100, 100)
	if len(runs) != 1 || len(runs[0]) != 2 || lastVisible(pts, 100, 100) != 3 {
		t.Errorf("clipped to the map: %v, last visible %d", runs, lastVisible(pts, 100, 100))
	}
	// An arc to the same place is a point.
	same, _ := ends(here)
	if len(visibleRuns(same, left, width)) > 1 {
		t.Error("a point split")
	}
}

func TestDelayAndWorkingAreShownApart(t *testing.T) {
	a := newTestApp(t)
	withNodes(t, a, "🇬🇧 London 01")
	usePreset(t, a, "ai-accounts")
	st := a.eng.State()
	tag := st.Plan.Routes[0].Nodes[0]
	draw := func(d int, works *bool) *ui.Tester {
		s := st
		s.Delays = map[string]int{tag: d}
		s.Works = map[string]bool{}
		if works != nil {
			s.Works[tag] = *works
		}
		return ui.NewTester(func(c *ui.Context) { a.delayPill(c, s, tag) }, 400, 60)
	}
	yes, no := true, false
	view(a) // sets the look

	tt := draw(214, &yes)
	need(t, tt, "214 ms", "通")
	if tt.HasText("不通") {
		t.Error("a node that works is shown as not")
	}
	// The server answers, and nothing goes through it: the delay stands, and "不通" is apart from it.
	tt = draw(214, &no)
	need(t, tt, "214 ms", "不通")
	// No answer at all: only the one word, no number.
	tt = draw(-1, &no)
	need(t, tt, "不通")
	for _, txt := range tt.Texts() {
		if strings.HasSuffix(txt, " ms") {
			t.Errorf("a number for a node that did not answer: %q", txt)
		}
	}
	// Not asked about yet whether it works: the delay alone.
	tt = draw(95, nil)
	need(t, tt, "95 ms")
	if tt.HasText("通") || tt.HasText("不通") {
		t.Error("a verdict before the test of it")
	}
}

func TestACheckedRouteSaysWhereItLeavesFrom(t *testing.T) {
	a := newTestApp(t)
	view(a)
	r := rules.Route{ID: "work", Exit: []string{"198.51.100.0/24"}}
	draw := func(x *engine.ExitCheck) *ui.Tester {
		st := a.eng.State()
		st.Exits = map[string]engine.ExitCheck{}
		if x != nil {
			st.Exits["work"] = *x
		}
		return ui.NewTester(func(c *ui.Context) { a.exitLine(c, st, r) }, 500, 40)
	}
	need(t, draw(nil), "核对中，未放行")
	need(t, draw(&engine.ExitCheck{IP: "198.51.100.222", OK: true, At: time.Now()}), "198.51.100.222")
	need(t, draw(&engine.ExitCheck{IP: "1.2.3.4", At: time.Now()}), "1.2.3.4 不在范围，已切断")
	need(t, draw(&engine.ExitCheck{Err: "timeout", At: time.Now()}), "无法确认，已切断")
}

// The order tab lists the rules as the core checks them, and says which values
// a rule above takes first.
func TestOrderShowsTheRulesAndWhatIsOutOfReach(t *testing.T) {
	a := newTestApp(t)
	withNodes(t, a, "🇬🇧英国-UK-1-流量倍率:0.9", "🇩🇪德国-DE-2-流量倍率:1")
	usePreset(t, a, "ai-accounts")
	if err := a.eng.NewGroup("视频", "proxy", "youtube.com ytimg.com"); err != nil {
		t.Fatal(err)
	}
	a.setPage(pageRules)
	a.rulesTab = tabOrder
	tt := ui.NewTester(a.view, 1040, 4000)
	need(t, tt, "最终顺序", "局域网与私有地址", "应用 · 视频", "其余所有流量",
		"ytimg.com 到不了这里：第 1 条先匹配，去了 proxy")
	// The headings of the rules text name the steps under them.
	if !tt.HasText("Google, Gemini, Antigravity") {
		t.Errorf("no heading from the rules text: %q", tt.Texts())
	}
	// A route's own rules are not said to be out of reach by its own guards.
	for _, s := range a.eng.Order() {
		if s.Target == "claude" && len(s.Shadowed) > 0 {
			t.Errorf("claude's step reported out of reach: %+v", s.Shadowed)
		}
	}
}

// The exits tab: a card for each exit, folded to a line, opened by a click; its
// settings saved to its line in the rules text.
func TestExitCardsFoldAndTheirSettingsAreWrittenToTheRoute(t *testing.T) {
	a := newTestApp(t)
	withNodes(t, a, "🇬🇧英国-UK-1-流量倍率:0.9", "🇩🇪德国-DE-2-流量倍率:1")
	if errs := a.eng.ApplyRules("route work region UK gate exit:198.51.100.0/24 # the office\nroute de region DE\nwork domain:work.example\n"); errs != nil {
		t.Fatal(errs)
	}
	a.setPage(pageRules)
	a.rulesTab = tabExits
	tt := view(a)
	need(t, tt, "work", "de", "proxy", "直连", "出口须在 198.51.100.0/24", "没有流量")
	if tt.HasText("work.example") {
		t.Error("a folded card shows what goes through it")
	}
	tt = view(a, "work")
	need(t, tt, "work.example", "出口设置")
	tt = view(a, "出口设置")
	need(t, tt, "核对间隔", "核对时段", "保存")
	d := a.exitEdit["work"]
	d.every, d.span, d.from, d.to = "每 5 分钟", someDay, "09:00", "23:30"
	d.rl.IPv4 = true
	view(a, "保存")
	want := "route work region UK gate ipv4 exit:198.51.100.0/24 check:5m hours:09:00-23:30 # the office\n"
	if got := a.eng.State().RulesText; !strings.HasPrefix(got, want) {
		t.Errorf("rules text:\n%s\nwant it to start:\n%s", got, want)
	}
	if a.exitEdit["work"] != nil {
		t.Error("the settings stay open after saving")
	}
	// What does not compile is refused, and said; the text is left as it was.
	before := a.eng.State().RulesText
	view(a, "出口设置")
	a.exitEdit["work"].rl.Exit = []string{"not-a-range"}
	tt = view(a, "保存")
	if a.eng.State().RulesText != before || !tt.HasText("not-a-range") {
		t.Errorf("a bad range was saved, or not said: %q", tt.Texts())
	}
}

// On an exit's card: the library is searched, an application added, one written
// by hand, and one removed after asking.
func TestApplicationsAreAddedFromTheExitCard(t *testing.T) {
	a := newTestApp(t)
	withNodes(t, a, "🇬🇧英国-UK-1-流量倍率:0.9")
	if errs := a.eng.ApplyRules("route work region UK noudp\n"); errs != nil {
		t.Fatal(errs)
	}
	a.setPage(pageRules)
	a.exitOpen["work"] = true
	tt := view(a, "添加应用")
	need(t, tt, "在规则库中搜索", "自己写")
	a.appQuery = "hugging"
	tt = view(a)
	need(t, tt, "Hugging Face", "把 Hugging Face 加到 work")
	tt = view(a, "把 Hugging Face 加到 work")
	if gs := a.eng.State().Groups; len(gs) != 1 || gs[0].App != "huggingface" || gs[0].Target != "work" {
		t.Fatalf("groups %+v", gs)
	}
	need(t, view(a), "已在 work", "规则库", "阻断 UDP")
	a.customName, a.customText = "办公", "work.example process:Office"
	view(a, "自己写")
	if gs := a.eng.State().Groups; len(gs) != 2 || gs[1].Name != "办公" || gs[1].App != "" {
		t.Fatalf("groups %+v", gs)
	}
	need(t, view(a), "办公", "自定义")
	// Removing, from its ⋯, asks first.
	tt = view(a, "办公的更多操作")
	if err := tt.ChooseMenuItem("删除"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	need(t, view(a), "移除 办公？")
	view(a, "移除")
	if gs := a.eng.State().Groups; len(gs) != 1 {
		t.Errorf("not removed: %+v", gs)
	}
}

// A group that holds a program shows it, and it can be added to and taken out
// like a site; kind:value that the rules would not take is refused.
func TestAGroupShowsAndTakesProgramsToo(t *testing.T) {
	a := newTestApp(t)
	a.rulesTab, a.exitOpen["direct"], a.appOpen["Orca"] = tabExits, true, true
	if err := a.eng.AddCustomApp("direct", "Orca", "process:Orca"); err != nil {
		t.Fatal(err)
	}
	tt := view(a, "规则")
	need(t, tt, "Orca", "process:Orca")
	if err := a.eng.SetGroupItems("Orca", []string{"process:Orca", "orca.example", "process:Orca Helper (GPU)"}); err != nil {
		t.Fatal(err)
	}
	g := a.eng.State().Groups[0]
	if strings.Join(g.Domains, ",") != "orca.example" || strings.Join(g.Rules, ",") != "process:Orca,process:Orca Helper (GPU)" {
		t.Errorf("group %+v", g)
	}
	for _, bad := range [][]string{{"port:nope"}, {"not a site"}, {"set:geosite-cn"}} {
		if err := a.eng.SetGroupItems("Orca", bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if len(a.eng.State().Groups[0].Rules) != 2 {
		t.Error("a refused change was kept")
	}
}

// The engine's notifier never waits: it posts at most one refresh at a time,
// however often it is called, and posts again once that one has run.
func TestNotifyingNeverWaitsAndPostsOnce(t *testing.T) {
	var posted []func()
	runs := 0
	notify := coalesce(func(f func()) { posted = append(posted, f) }, func() { runs++ })
	for i := 0; i < 100; i++ {
		notify() // the main thread is busy: nothing runs, and nothing blocks
	}
	if len(posted) != 1 || runs != 0 {
		t.Fatalf("posted %d, ran %d", len(posted), runs)
	}
	posted[0]()
	notify()
	if len(posted) != 2 || runs != 1 {
		t.Errorf("after it ran: posted %d, ran %d", len(posted), runs)
	}
}

// An exit is made on the page, without the rules text; one that still has
// applications cannot be deleted, an empty one can.
func TestAnExitIsMadeAndDeletedOnThePage(t *testing.T) {
	a := newTestApp(t)
	withNodes(t, a, "🇬🇧英国-UK-2-流量倍率:1", "🇩🇪德国-DE-2-流量倍率:1")
	a.setPage(pageRules)
	view(a, "新建出口")
	f := &a.newExit
	f.id, f.arg, f.noUDP = "work", "英国-UK-2-", true
	tt := view(a)
	need(t, tt, "符合的 1 个", "建好")
	view(a, "建好")
	if got := a.eng.State().RulesText; !strings.Contains(got, "route work regex \"英国-UK-2-\" gate ipv4 noudp") {
		t.Fatalf("rules text:\n%s", got)
	}
	if a.newExit.open || !a.exitOpen["work"] {
		t.Error("the form stays open, or the new exit is not shown open")
	}
	// A bad name is said as it is typed, and the exit cannot be made with it.
	view(a, "新建出口")
	for name, said := range map[string]string{
		"my work": "出口名不能有空格",
		"a#b":     "出口名不能有 #：# 在规则文本里表示注释",
		"a:b":     "出口名不能有冒号 :",
		`a"b`:     "出口名不能有引号",
		"Work":    "英文请用小写",
		"work":    "已经有叫 work 的出口",
		"notice":  "「notice」是 Faro 自己用的词",
	} {
		a.newExit.id, a.newExit.arg = name, "x"
		tt = view(a)
		if !tt.HasText(said) {
			t.Errorf("%q: not said %q: %q", name, said, tt.Texts())
		}
		before := a.eng.State().RulesText
		view(a, "建好")
		if !a.newExit.open || a.eng.State().RulesText != before {
			t.Errorf("%q was taken", name)
		}
	}
	// Nothing is said of an empty name, but it is not made either.
	a.newExit.id = ""
	if tt = view(a, "建好"); tt.HasText("请给出口起个名字") || !a.newExit.open {
		t.Error("an empty name: said at once, or made")
	}
	// A Chinese name is taken.
	a.newExit.id, a.newExit.arg = "英国-办公", "英国-UK-2-"
	view(a, "建好")
	if got := a.eng.State().RulesText; !strings.Contains(got, "route 英国-办公 regex") || a.newExit.open {
		t.Fatalf("the Chinese name was not taken:\n%s", got)
	}
	if errs := a.eng.DeleteRoute("英国-办公"); errs != nil {
		t.Fatal(errs)
	}
	// With an application on it, it is not deleted.
	if err := a.eng.AddCustomApp("work", "办公", "work.example"); err != nil {
		t.Fatal(err)
	}
	if errs := a.eng.DeleteRoute("work"); errs == nil || !strings.Contains(errs[0].Msg, "办公") {
		t.Errorf("deleted with an application on it: %v", errs)
	}
	a.eng.DeleteGroup("办公")
	a.startExitEdit(a.eng.State(), "work")
	view(a, "删除出口 work")
	view(a, "删除")
	if strings.Contains(a.eng.State().RulesText, "route work") {
		t.Error("not deleted")
	}
}

// The library shows a few at first and says how many more a search finds;
// a search finds one far down the list.
func TestTheLibrarySaysThereIsMoreAndASearchFindsIt(t *testing.T) {
	a := newTestApp(t)
	withNodes(t, a, "🇬🇧 London 01")
	if errs := a.eng.ApplyRules("route uk region UK\n"); errs != nil {
		t.Fatal(errs)
	}
	a.setPage(pageRules)
	a.exitOpen["uk"] = true
	tt := view(a, "添加应用")
	if !tt.HasText("还有 ") || tt.HasText("WhatsApp") {
		t.Errorf("the rest is not said, or all are shown: %q", tt.Texts())
	}
	a.appQuery = "whats"
	need(t, view(a), "WhatsApp", "把 WhatsApp 加到 uk")
}

// tall renders the app in a window tall enough for the whole settings page,
// after clicking the given texts, each of which must be there.
func tall(t *testing.T, a *app, clicks ...string) *ui.Tester {
	t.Helper()
	tt := ui.NewTester(a.view, 1040, 1400)
	tt.SetTitleBar(ui.TitleBar{Height: 28, Left: 76})
	for _, c := range clicks {
		if err := tt.Click(c); err != nil {
			t.Fatalf("click %q: %v", c, err)
		}
	}
	return tt
}

// testConfigIO answers the file dialogs with these paths at once. What comes
// back to the interface waits in a queue that the test runs, as the main
// thread would: the interface's state is only ever touched by the test.
type testConfigQueue struct {
	mu sync.Mutex
	q  []func()
}

func (q *testConfigQueue) post(f func()) { q.mu.Lock(); q.q = append(q.q, f); q.mu.Unlock() }

// settle waits for an answer to come back, and runs it.
func (q *testConfigQueue) settle(t *testing.T) {
	t.Helper()
	var fs []func()
	wait(t, "the answer", func() bool {
		q.mu.Lock()
		fs, q.q = q.q, nil
		q.mu.Unlock()
		return len(fs) > 0
	})
	for _, f := range fs {
		f()
	}
}

func testConfigIO(save, open string) (configIO, *testConfigQueue) {
	q := &testConfigQueue{}
	return configIO{
		save: func(string) (string, error) { return save, nil },
		open: func() (string, error) { return open, nil },
		post: q.post,
	}, q
}

// The whole configuration goes to a file, asked about first as it holds the
// passwords, and comes back from it, asked about first as it replaces all.
func TestTheConfigurationIsExportedAndImported(t *testing.T) {
	a := newTestApp(t)
	withNodes(t, a, "🇬🇧 London 01", "🇩🇪 Frankfurt 01")
	usePreset(t, a, "ai-accounts")
	file := filepath.Join(t.TempDir(), "faro.json")
	io, q := testConfigIO(file, file)
	a.cfgIO = io
	view(a, "设置")
	defer a.setPage(pageNow)

	tt := tall(t, a, "导出…")
	need(t, tt, "导出配置？", "节点密码")
	tall(t, a, "导出")
	q.settle(t)
	if a.cfg.err != "" {
		t.Fatal(a.cfg.err)
	}
	if fi, err := os.Stat(file); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file: %v %v", err, fi)
	}
	need(t, tall(t, a), "已导出到 "+file)

	// Changed since, then the file brought back.
	want := a.eng.State().RulesText
	if errs := a.eng.ApplyRules("route uk region UK\n"); errs != nil {
		t.Fatal(errs)
	}
	a.rulesEdit = a.eng.State().RulesText
	tall(t, a, "导入…")
	q.settle(t)
	tt = tall(t, a)
	need(t, tt, "导入并替换当前配置？", "1 个订阅、2 个节点", "先备份")
	if a.eng.State().RulesText == want {
		t.Fatal("replaced before it was answered")
	}
	tall(t, a, "导入")
	if st := a.eng.State(); st.RulesText != want || len(st.Nodes) != 2 {
		t.Errorf("not imported: %q", st.RulesText)
	}
	if a.rulesEdit != want {
		t.Error("the rules page still shows the old text")
	}
	need(t, tall(t, a), "已导入：1 个订阅、2 个节点", "导入前-")

	// A file that is not one is said, and nothing is asked.
	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte("{}"), 0o600)
	io, q = testConfigIO(file, bad)
	a.cfgIO = io
	tall(t, a, "导入…")
	q.settle(t)
	tt = tall(t, a)
	need(t, tt, "不能导入：不是 Faro 导出的配置文件")
	if a.cfg.importAsk {
		t.Error("asked about a file that is not one")
	}
}

// A route's guards are one row, and a group's rules one row, however many the
// core needs; a row opens to show each, with the group a guard stands for.
func TestOrderFoldsGuardsAndGroups(t *testing.T) {
	a := newTestApp(t)
	withNodes(t, a, "🇬🇧 London 01")
	if errs := a.eng.ApplyRules("route claude region UK noudp noipv6\ndirect set:geosite-cn\n"); errs != nil {
		t.Fatal(errs)
	}
	if err := a.eng.AddCustomApp("claude", "AI", "claude.ai anthropic.com process:Claude keyword:anthropic"); err != nil {
		t.Fatal(err)
	}
	// The engine: each guard knows the group it guards; each group step its group.
	order := a.eng.Order()
	guards := 0
	for _, s := range order {
		if s.Guard != "" {
			guards++
			if s.GuardOf != "AI" {
				t.Errorf("guard of %q: %+v", s.GuardOf, s)
			}
		}
	}
	if guards != 6 { // three kinds, UDP and IPv6 each
		t.Fatalf("%d guards: %+v", guards, order)
	}

	a.setPage(pageRules)
	a.rulesTab = tabOrder
	defer a.setPage(pageNow)
	tt := ui.NewTester(a.view, 1180, 1400)
	need(t, tt, "1–6", "claude 的守卫", "拒绝 UDP 和 IPv6 · 守住 AI",
		"7–9", "应用 · AI", "claude.ai、anthropic.com · 进程 Claude · 关键词 anthropic",
		"10", "规则集 geosite-cn")
	if tt.HasText("UDP · 进程 Claude") {
		t.Error("a guard's step is shown before the row is opened")
	}
	if err := tt.Click("claude 的守卫"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	need(t, tt, "UDP · 进程 Claude", "IPv6 · 关键词 anthropic")
	if err := tt.Click("claude 的守卫"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("UDP · 进程 Claude") {
		t.Error("the row did not fold again")
	}
}

// auto and block have a card only when an application is sent there; a route's
// guards, which block too, are pills on the route's card, not an application.
func TestBlockAndAutoHaveACardOnlyForTheirApplications(t *testing.T) {
	a := newTestApp(t)
	withNodes(t, a, "🇬🇧 London 01")
	if errs := a.eng.ApplyRules("route uk region UK noudp noipv6\ndirect set:geosite-cn\n"); errs != nil {
		t.Fatal(errs)
	}
	a.eng.NewGroup("办公", "uk", "work.example")
	tt := apps(t, a, []string{"uk"})
	need(t, tt, "阻断 UDP", "阻断 IPv6", "办公")
	if tt.HasText("不允许连接") || tt.HasText("所有节点中最快的") {
		t.Error("a card for block or auto with no application on it")
	}
	a.eng.NewGroup("广告", "block", "ads.example")
	a.eng.NewGroup("测速", "auto", "speed.example")
	need(t, apps(t, a, []string{"block", "auto"}), "阻断", "不允许连接", "广告", "所有节点中最快的", "测速")
}
