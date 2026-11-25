package engine

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"faro/internal/fixtures"
)

// exported is a configuration set up on one engine, and the file it gives.
func exported(t *testing.T) (*Engine, []byte) {
	t.Helper()
	e := newEngine(t, (&fakeNetworksetup{}).run)
	e.addAndWait(t, subFile(t, "🇬🇧 London 01", "🇩🇪 Frankfurt 01"), 2)
	if errs := e.ApplyRules(fixtures.AIAccounts()); errs != nil {
		t.Fatal(errs)
	}
	if err := e.NewGroup("AI", "claude", "claude.ai"); err != nil {
		t.Fatal(err)
	}
	e.Choose("proxy", "🇩🇪 Frankfurt 01")
	if err := e.SetDNS("dnspod", "cloudflare"); err != nil {
		t.Fatal(err)
	}
	e.SetLightMode(true)
	b, err := e.ExportConfig("9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	return e, b
}

// What is exported from one Mac is, imported on another, the same set-up.
func TestAConfigurationGoesFromOneMacToAnother(t *testing.T) {
	from, b := exported(t)
	var f ConfigFile
	if err := json.Unmarshal(b, &f); err != nil || f.Faro != configFormat || f.App != "9.9.9" {
		t.Fatalf("file: %v %+v", err, f)
	}

	to := newEngine(t, (&fakeNetworksetup{}).run) // another data directory: another Mac
	res, err := to.ImportConfig(b)
	if err != nil {
		t.Fatal(err)
	}
	if res.Subscriptions != 1 || res.Nodes != 2 || res.Groups != 1 {
		t.Errorf("result %+v", res)
	}
	a, z := from.State(), to.State()
	if z.RulesText != a.RulesText || !reflect.DeepEqual(z.Groups, a.Groups) || len(z.Nodes) != 2 ||
		z.Choices["proxy"] != "🇩🇪 Frankfurt 01" || z.DNS != a.DNS || !z.LightMode || z.MixedPort != a.MixedPort {
		t.Errorf("imported:\n%+v\nfrom:\n%+v", z, a)
	}
	if z.Plan == nil || len(z.GroupErrors) != 0 {
		t.Errorf("not in force: %v %v", z.PlanErrors, z.GroupErrors)
	}
	// It is kept: a new start of the app has it.
	again, err := to.ExportConfig("9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	var g ConfigFile
	json.Unmarshal(again, &g)
	if g.Rules != f.Rules || len(g.Subscriptions) != 1 || g.Settings != f.Settings {
		t.Errorf("not kept: %+v", g.Settings)
	}
}

// What was in force before an import is kept aside, and importing that puts it back.
func TestAnImportKeepsWhatWasThereAndCanBeUndone(t *testing.T) {
	_, b := exported(t)
	to := newEngine(t, (&fakeNetworksetup{}).run)
	to.addAndWait(t, subFile(t, "🇺🇸 Seattle 01"), 1)
	before := to.State()
	res, err := to.ImportConfig(b)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filepath.Base(res.Backup), "导入前-") {
		t.Fatalf("backup %q", res.Backup)
	}
	old, err := os.ReadFile(res.Backup)
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(res.Backup); fi.Mode().Perm() != 0o600 {
		t.Errorf("backup mode %v: it holds passwords", fi.Mode().Perm())
	}
	if _, err := to.ImportConfig(old); err != nil {
		t.Fatal(err)
	}
	after := to.State()
	if after.RulesText != before.RulesText || len(after.Nodes) != 1 || after.Nodes[0].Tag != "🇺🇸 Seattle 01" || len(after.Groups) != 0 {
		t.Errorf("not put back: %+v", after.Nodes)
	}
}

// A file that is not right is refused whole, and nothing changes.
func TestABadConfigurationIsRefusedAndNothingChanges(t *testing.T) {
	_, good := exported(t)
	edit := func(f func(m map[string]any)) []byte {
		var m map[string]any
		json.Unmarshal(good, &m)
		f(m)
		b, _ := json.Marshal(m)
		return b
	}
	cases := map[string][]byte{
		"不是 Faro 的配置文件":   []byte("not json"),
		"不是 Faro 导出的配置文件": []byte(`{"rules":"x"}`),
		"更新版本的 Faro":      edit(func(m map[string]any) { m["faro"] = 99 }),
		"规则有错误":           edit(func(m map[string]any) { m["rules"] = "route x nonsense\n" }),
		"出现了两次": edit(func(m map[string]any) {
			g := m["groups"].([]any)
			m["groups"] = append(g, g[0])
		}),
		"端口":  edit(func(m map[string]any) { m["settings"].(map[string]any)["mixedPort"] = 80 }),
		"DNS": edit(func(m map[string]any) { m["settings"].(map[string]any)["dnsForeign"] = "nowhere" }),
		"节点": edit(func(m map[string]any) {
			m["subscriptions"].([]any)[0].(map[string]any)["nodes"].([]any)[0].(map[string]any)["out"] = map[string]any{}
		}),
		"没有规则文本": edit(func(m map[string]any) { m["rules"] = "" }),
	}
	to := newEngine(t, (&fakeNetworksetup{}).run)
	to.addAndWait(t, subFile(t, "🇺🇸 Seattle 01"), 1)
	before := to.State()
	for want, b := range cases {
		if _, err := to.ImportConfig(b); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q, got %v", want, err)
		}
	}
	after := to.State()
	if after.RulesText != before.RulesText || len(after.Nodes) != 1 {
		t.Error("a refused file changed something")
	}
	if entries, _ := os.ReadDir(filepath.Join(after.DataDir, "backups")); len(entries) != 0 {
		t.Errorf("a refused file left %d backups", len(entries))
	}
}

// Rule sets the file uses that this Mac has not are fetched after it.
func TestAnImportFetchesTheRuleSetsItUses(t *testing.T) {
	src, _ := filepath.Abs("../../resources/rulesets/geosite-xai.srs")
	var mu sync.Mutex
	var fetched []string
	fetch := func(_ context.Context, dir, name string, _ int) error {
		mu.Lock()
		fetched = append(fetched, name)
		mu.Unlock()
		if name == "geosite-nowhere" {
			return errors.New("no route to host")
		}
		b, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, name+".srs"), b, 0o600)
	}
	from := newEngineWith(t, (&fakeNetworksetup{}).run, func(o *Options) { o.FetchSet = fetch })
	from.addAndWait(t, subFile(t, "🇬🇧 London 01"), 1)
	if errs := from.ApplyRules("route uk region UK\n"); errs != nil {
		t.Fatal(errs)
	}
	from.NewGroup("X", "uk", "x.ai")
	b, _ := from.ExportConfig("")
	var m map[string]any
	json.Unmarshal(b, &m)
	m["groups"].([]any)[0].(map[string]any)["sets"] = []string{"geosite-xai-copy", "geosite-nowhere"}
	b, _ = json.Marshal(m)

	to := newEngineWith(t, (&fakeNetworksetup{}).run, func(o *Options) { o.FetchSet = fetch })
	res, err := to.ImportConfig(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Fetching, []string{"geosite-nowhere", "geosite-xai-copy"}) {
		t.Errorf("fetching %v", res.Fetching)
	}
	waitFor(t, "the sets", func() bool { return to.State().Busy == "" && strings.Contains(to.State().Err, "nowhere") })
	if _, err := os.Stat(filepath.Join(to.State().DataDir, "rulesets", "geosite-xai-copy.srs")); err != nil {
		t.Error("the set that could be fetched is not there")
	}
}

// A port the file names that something here holds is not taken: the old one stays.
func TestAnImportKeepsThePortWhenItsTaken(t *testing.T) {
	_, b := exported(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	held := l.Addr().(*net.TCPAddr).Port
	var m map[string]any
	json.Unmarshal(b, &m)
	m["settings"].(map[string]any)["mixedPort"] = held
	b, _ = json.Marshal(m)

	to := newEngine(t, (&fakeNetworksetup{}).run)
	old := to.State().MixedPort
	res, err := to.ImportConfig(b)
	if err != nil {
		t.Fatal(err)
	}
	if res.PortKept != old || to.State().MixedPort != old {
		t.Errorf("port: kept %d, now %d, was %d", res.PortKept, to.State().MixedPort, old)
	}
}

// A running core restarts on what was imported.
func TestAnImportReachesTheRunningCore(t *testing.T) {
	_, b := exported(t)
	to := newEngine(t, (&fakeNetworksetup{}).run)
	to.Start()
	waitFor(t, "running", func() bool { return to.State().Status == Running })
	if _, err := to.ImportConfig(b); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "running again", func() bool { return to.State().Status == Running })
	cfg, err := os.ReadFile(filepath.Join(to.State().DataDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), "Frankfurt 01") || !strings.Contains(string(cfg), "claude.ai") {
		t.Error("the core's config does not have the imported nodes and groups")
	}
}
