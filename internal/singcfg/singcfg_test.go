package singcfg

import (
	"encoding/json"
	"faro/internal/fixtures"
	"fmt"
	"net"
	"strings"

	"faro/internal/rules"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The generated config must pass the real sing-box's own check.
func TestBuildPassesCheck(t *testing.T) {
	bin, _ := filepath.Abs("../../bin/sing-box")
	if _, err := os.Stat(bin); err != nil {
		t.Skip("no bin/sing-box")
	}
	set, err := NewSettings(0)
	if err != nil {
		t.Fatal(err)
	}
	nodes := []Outbound{
		{"vm", map[string]any{"type": "vmess", "server": "a.example.com", "server_port": 443, "uuid": "b831381d-6324-4d53-ad4f-8cda48b30811", "security": "auto", "alter_id": 0,
			"tls": map[string]any{"enabled": true, "server_name": "a.example.com"}, "transport": map[string]any{"type": "ws", "path": "/x", "headers": map[string]any{"Host": "h.com"}}}},
		{"rl", map[string]any{"type": "vless", "server": "b.example.com", "server_port": 443, "uuid": "b831381d-6324-4d53-ad4f-8cda48b30811", "flow": "xtls-rprx-vision",
			"tls": map[string]any{"enabled": true, "server_name": "apple.com", "utls": map[string]any{"enabled": true, "fingerprint": "chrome"},
				"reality": map[string]any{"enabled": true, "public_key": "Ro_C3LfQ6ouz6d5L3F8bQ6D8XpJbG8vNlXjK0y3Vd0w", "short_id": "ab12"}}}},
		{"hy", map[string]any{"type": "hysteria2", "server": "c.example.com", "server_port": 443, "password": "p", "tls": map[string]any{"enabled": true, "server_name": "c.example.com"}}},
		{"ss", map[string]any{"type": "shadowsocks", "server": "d.example.com", "server_port": 1, "method": "aes-256-gcm", "password": "p"}},
		{"tj", map[string]any{"type": "trojan", "server": "e.example.com", "server_port": 443, "password": "p", "tls": map[string]any{"enabled": true}, "transport": map[string]any{"type": "grpc", "service_name": "s"}}},
		{"tu", map[string]any{"type": "tuic", "server": "f.example.com", "server_port": 443, "uuid": "b831381d-6324-4d53-ad4f-8cda48b30811", "password": "p", "tls": map[string]any{"enabled": true}}},
	}
	for name, ns := range map[string][]Outbound{"direct only": nil, "with nodes": nodes} {
		cfg, _, err := Build(set, Input{Nodes: ns, Choices: map[string]string{"proxy": "hy"}})
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(t.TempDir(), "c.json")
		os.WriteFile(p, cfg, 0o600)
		if out, err := exec.Command(bin, "check", "-c", p).CombinedOutput(); err != nil {
			t.Errorf("%s: %v\n%s\n%s", name, err, out, cfg)
		}
	}
}

func TestBuildWithPlanPassesCheck(t *testing.T) {
	bin, _ := filepath.Abs("../../bin/sing-box")
	if _, err := os.Stat(bin); err != nil {
		t.Skip("no bin/sing-box")
	}
	sets := map[string]string{}
	files, _ := filepath.Glob("../../resources/rulesets/*.srs")
	for _, f := range files {
		abs, _ := filepath.Abs(f)
		sets[strings.TrimSuffix(filepath.Base(f), ".srs")] = abs
	}
	if len(sets) == 0 {
		t.Skip("no resources/rulesets")
	}
	text := `route claude region UK
route gemini region DE fast
route nowhere region JP
route corp regex (?i)^corp
claude domain:claude.ai
claude set:geosite-anthropic
gemini full:gemini.google.com
nowhere domain:jp.example.com
corp keyword:intra
block set:geosite-category-ads-all
direct set:geosite-cn
direct set:geoip-cn
direct cidr:203.0.113.0/24
proxy regex:^cdn[0-9]+\.example\.com$
direct set:geosite-not-installed
`
	tags := []string{"UK 01", "UK 02", "DE 01", "DE 02", "corp-1", "US 01"}
	plan, errs := rules.Compile(text, tags, map[string]string{"claude": "UK 02"})
	if errs != nil {
		t.Fatal(errs)
	}
	var nodes []Outbound
	for _, tag := range tags {
		nodes = append(nodes, Outbound{tag, map[string]any{"type": "shadowsocks", "server": "a.example.com", "server_port": 1, "method": "aes-256-gcm", "password": "p"}})
	}
	set, _ := NewSettings(0)
	cfg, notes, err := Build(set, Input{Nodes: nodes, Plan: plan, Sets: sets})
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "geosite-not-installed") {
		t.Errorf("notes %v", notes)
	}
	p := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(p, cfg, 0o600)
	if out, err := exec.Command(bin, "check", "-c", p).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s\n%s", err, out, cfg)
	}
	// Order: sniff, private, then the rules; the address resolve only before geoip.
	var c struct {
		Route struct {
			Rules []map[string]any `json:"rules"`
		} `json:"route"`
	}
	json.Unmarshal(cfg, &c)
	var acts []string
	for _, r := range c.Route.Rules {
		a, _ := r["action"].(string)
		acts = append(acts, a)
	}
	if acts[0] != "sniff" || strings.Join(acts[:2], ",") != "sniff,route" {
		t.Errorf("rule order starts %v", acts)
	}
	resolveAt, geoipAt := -1, -1
	for i, r := range c.Route.Rules {
		if r["action"] == "resolve" {
			resolveAt = i
		}
		if sets, ok := r["rule_set"].([]any); ok && len(sets) == 1 && sets[0] == "geoip-cn" {
			geoipAt = i
		}
	}
	if resolveAt < 0 || geoipAt != resolveAt+1 {
		t.Errorf("resolve at %d, geoip at %d, in %v", resolveAt, geoipAt, acts)
	}
	for i := range c.Route.Rules[:resolveAt] {
		if c.Route.Rules[i]["rule_set"] != nil {
			if s, _ := c.Route.Rules[i]["rule_set"].([]any); len(s) > 0 && s[0] == "geoip-cn" {
				t.Error("geoip before resolve")
			}
		}
	}
}

func TestStablePort(t *testing.T) {
	first, err := NewSettings(0)
	if err != nil {
		t.Fatal(err)
	}
	// Asking for the same port again gives the same port, when it is free.
	again, err := NewSettings(first.MixedPort)
	if err != nil || again.MixedPort != first.MixedPort {
		t.Fatalf("%v %v", again, err)
	}
	if again.APIPort == first.APIPort && again.Secret == first.Secret {
		t.Error("the controller's secret must be new every run")
	}
	// A port that is taken is an error: no quiet move to another one.
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", first.MixedPort))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, err := NewSettings(first.MixedPort); err == nil || !strings.Contains(err.Error(), "in use") {
		t.Errorf("taken port: %v", err)
	}
}

// The whole default rule text, from the user's own setup, must make a
// config the real core accepts, with every rule set it names installed.
func TestDefaultRulesPassCheck(t *testing.T) {
	bin, _ := filepath.Abs("../../bin/sing-box")
	if _, err := os.Stat(bin); err != nil {
		t.Skip("no bin/sing-box")
	}
	sets := map[string]string{}
	files, _ := filepath.Glob("../../resources/rulesets/*.srs")
	for _, f := range files {
		abs, _ := filepath.Abs(f)
		sets[strings.TrimSuffix(filepath.Base(f), ".srs")] = abs
	}
	if len(sets) == 0 {
		t.Skip("no resources/rulesets")
	}
	tags := []string{"🇬🇧 London 01", "🇬🇧 London 02", "🇩🇪 Frankfurt 01", "🇺🇸 LA 01"}
	var nodes []Outbound
	for _, tag := range tags {
		nodes = append(nodes, Outbound{tag, map[string]any{"type": "shadowsocks", "server": "a.example.com", "server_port": 1, "method": "aes-256-gcm", "password": "p"}})
	}
	for name, choices := range map[string]map[string]string{
		"gate open":   {"claude": "🇬🇧 London 02"},
		"gate closed": {"claude": "🇬🇧 London 02", "claude" + rules.GateSuffix: "closed"},
	} {
		plan, errs := rules.Compile(fixtures.AIAccounts(), tags, choices)
		if errs != nil {
			t.Fatal(errs)
		}
		set, _ := NewSettings(0)
		cfg, notes, err := Build(set, Input{Nodes: nodes, Plan: plan, Choices: choices, Sets: sets})
		if err != nil {
			t.Fatal(err)
		}
		if len(notes) != 0 {
			t.Errorf("%s: rules skipped: %v", name, notes)
		}
		p := filepath.Join(t.TempDir(), "c.json")
		os.WriteFile(p, cfg, 0o600)
		if out, err := exec.Command(bin, "check", "-c", p).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
		var c struct {
			Outbounds []map[string]any `json:"outbounds"`
			Route     struct {
				FindProcess bool `json:"find_process"`
			} `json:"route"`
		}
		json.Unmarshal(cfg, &c)
		if !c.Route.FindProcess {
			t.Errorf("%s: process rules without find_process", name)
		}
		var claude, openai, google map[string]any
		var blockOut, ipv4Node bool
		for _, o := range c.Outbounds {
			switch o["tag"] {
			case "route-claude":
				claude = o
			case "route-openai":
				openai = o
			case "route-google":
				google = o
			case "block":
				blockOut = o["type"] == "block"
			case "🇬🇧 London 01":
				ipv4Node = o["domain_resolver"] != nil
			}
		}
		if claude == nil || openai == nil || google == nil || !blockOut || !ipv4Node {
			t.Fatalf("%s: groups claude %v openai %v google %v block %v ipv4 %v", name, claude != nil, openai != nil, google != nil, blockOut, ipv4Node)
		}
		wantDef := "🇬🇧 London 02"
		if name == "gate closed" {
			wantDef = "block"
		}
		if claude["default"] != wantDef {
			t.Errorf("%s: claude default %v, want %v", name, claude["default"], wantDef)
		}
		// The follower is on the same node whatever the gate does, and has no gate choice.
		if openai["default"] != "🇬🇧 London 02" {
			t.Errorf("%s: openai default %v", name, openai["default"])
		}
		for _, o := range openai["outbounds"].([]any) {
			if o == "block" {
				t.Errorf("%s: openai has a gate", name)
			}
		}
		if g := google["outbounds"].([]any); len(g) != 1 {
			t.Errorf("%s: google outbounds %v", name, g)
		}
	}
}

func TestProbeIsNotGoogle(t *testing.T) {
	for _, bad := range []string{"google", "gstatic", "gvt", "youtube"} {
		if strings.Contains(ProbeURL, bad) {
			t.Errorf("the probe URL %q is a Google host: every node would reach Google", ProbeURL)
		}
	}
}

func TestAutoGroupSkipsNotices(t *testing.T) {
	nodes := []Outbound{
		{"有效期2099-01-01, 剩余 100.00 GB", map[string]any{"type": "shadowsocks", "server": "a.example.com", "server_port": 1, "method": "aes-256-gcm", "password": "p"}},
		{"日本-TY-1-流量倍率:1.2", map[string]any{"type": "shadowsocks", "server": "b.example.com", "server_port": 1, "method": "aes-256-gcm", "password": "p"}},
	}
	set, _ := NewSettings(0)
	cfg, _, err := Build(set, Input{Nodes: nodes})
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Outbounds []struct {
			Tag       string   `json:"tag"`
			Outbounds []string `json:"outbounds"`
		} `json:"outbounds"`
	}
	json.Unmarshal(cfg, &c)
	for _, o := range c.Outbounds {
		switch o.Tag {
		case "auto":
			if len(o.Outbounds) != 1 || strings.Contains(o.Outbounds[0], "剩余") {
				t.Errorf("auto group %v", o.Outbounds)
			}
		case "proxy": // still listed, so it can be picked by hand
			if len(o.Outbounds) != 3 {
				t.Errorf("proxy group %v", o.Outbounds)
			}
		}
	}
}

func TestAnIPv4RouteHasItsNamesLookedUpAsIPv4ThroughItself(t *testing.T) {
	text := `route work region UK gate ipv4
route other region DE
work domain:work.example
work process:WorkApp
other domain:other.example
direct domain:local.example
`
	tags := []string{"UK 01", "DE 01"}
	plan, errs := rules.Compile(text, tags, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	var nodes []Outbound
	for _, tag := range tags {
		nodes = append(nodes, Outbound{tag, map[string]any{"type": "shadowsocks", "server": "a.example.com", "server_port": 1, "method": "aes-256-gcm", "password": "p"}})
	}
	set, _ := NewSettings(0)
	cfg, _, err := Build(set, Input{Nodes: nodes, Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	if bin, _ := filepath.Abs("../../bin/sing-box"); fileExists(bin) {
		p := filepath.Join(t.TempDir(), "c.json")
		os.WriteFile(p, cfg, 0o600)
		if out, err := exec.Command(bin, "check", "-c", p).CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	var c struct {
		DNS struct {
			Servers []map[string]any `json:"servers"`
		} `json:"dns"`
		Route struct {
			Rules []map[string]any `json:"rules"`
		} `json:"route"`
	}
	json.Unmarshal(cfg, &c)
	work := "route-work"

	// A server for the route, reached through it; none for the route without ipv4.
	var via map[string]any
	for _, s := range c.DNS.Servers {
		if s["tag"] == "dns-"+work {
			via = s
		}
		if s["tag"] == "dns-route-other" {
			t.Error("a route without ipv4 has a server of its own")
		}
	}
	if via == nil || via["detour"] != work {
		t.Fatalf("the route's DNS server: %v", via)
	}

	// Each rule to the route is preceded by the same rule, resolving IPv4 only
	// through that server; other rules are not.
	var toWork, resolves int
	for i, r := range c.Route.Rules {
		if r["action"] == "resolve" && r["strategy"] == "ipv4_only" {
			resolves++
			next := c.Route.Rules[i+1]
			if r["server"] != "dns-"+work || next["outbound"] != work {
				t.Errorf("resolve %v is not before a rule to the route: %v", r, next)
			}
			for k, v := range r {
				if k == "action" || k == "server" || k == "strategy" {
					continue
				}
				if fmt.Sprint(next[k]) != fmt.Sprint(v) {
					t.Errorf("the resolve matches %s=%v, the rule %v", k, v, next[k])
				}
			}
			if _, ok := r["outbound"]; ok {
				t.Error("a resolve with an outbound")
			}
		}
		if r["outbound"] == work {
			toWork++
		}
	}
	if toWork != 2 || resolves != 2 {
		t.Errorf("%d rules to the route, %d resolves: want 2 and 2", toWork, resolves)
	}
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestARouteWithAnExitHasACheckEntranceThatIgnoresItsGate(t *testing.T) {
	text := "route work region UK ipv4 exit:198.51.100.0/24\nroute plain region DE\nwork domain:work.example\n"
	tags := []string{"UK 01", "UK 02", "DE 01"}
	plan, errs := rules.Compile(text, tags, map[string]string{"work": "UK 02", "work" + rules.GateSuffix: "closed"})
	if errs != nil {
		t.Fatal(errs)
	}
	var nodes []Outbound
	for _, tag := range tags {
		nodes = append(nodes, Outbound{tag, map[string]any{"type": "shadowsocks", "server": "a.example.com", "server_port": 1, "method": "aes-256-gcm", "password": "p"}})
	}
	set, _ := NewSettings(0)
	if err := set.AddCheck("route-work"); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Build(set, Input{Nodes: nodes, Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	if bin, _ := filepath.Abs("../../bin/sing-box"); fileExists(bin) {
		p := filepath.Join(t.TempDir(), "c.json")
		os.WriteFile(p, cfg, 0o600)
		if out, err := exec.Command(bin, "check", "-c", p).CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	var c struct {
		Inbounds  []map[string]any `json:"inbounds"`
		Outbounds []map[string]any `json:"outbounds"`
		DNS       struct {
			Servers []map[string]any `json:"servers"`
		} `json:"dns"`
		Route struct {
			Rules []map[string]any `json:"rules"`
		} `json:"route"`
	}
	json.Unmarshal(cfg, &c)
	ct := CheckTag("route-work")
	var in, sel map[string]any
	for _, i := range c.Inbounds {
		if i["tag"] == ct {
			in = i
		}
	}
	for _, o := range c.Outbounds {
		if o["tag"] == ct {
			sel = o
		}
		if o["tag"] == CheckTag("route-plain") {
			t.Error("a route with no exit has a check")
		}
	}
	if in == nil || int(in["listen_port"].(float64)) != set.CheckPorts["route-work"] || in["listen"] != "127.0.0.1" {
		t.Fatalf("check entrance %v", in)
	}
	if sel == nil || fmt.Sprint(sel["outbounds"]) != "[UK 01 UK 02]" || sel["default"] != "UK 02" {
		t.Errorf("the check goes out by %v: the route's nodes, on its node, never its gate", sel)
	}
	// Right after sniff, before anything else: the lookup, IPv4 only, through the
	// check's own server, then out through the check's group.
	r := c.Route.Rules
	if r[1]["action"] != "resolve" || r[1]["server"] != "dns-"+ct || fmt.Sprint(r[1]["inbound"]) != "["+ct+"]" ||
		r[2]["action"] != "route" || r[2]["outbound"] != ct || fmt.Sprint(r[2]["inbound"]) != "["+ct+"]" {
		t.Errorf("check rules %v / %v", r[1], r[2])
	}
	found := false
	for _, s := range c.DNS.Servers {
		found = found || (s["tag"] == "dns-"+ct && s["detour"] == ct)
	}
	if !found {
		t.Error("no DNS server through the check's group: a cut route would have none to look up with")
	}
	// Without a port, no entrance: the check is for a running core that asked for one.
	set2, _ := NewSettings(0)
	cfg2, _, _ := Build(set2, Input{Nodes: nodes, Plan: plan})
	if strings.Contains(string(cfg2), ct) {
		t.Error("an entrance with no port")
	}
}
