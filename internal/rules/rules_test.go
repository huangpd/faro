package rules

import (
	"faro/internal/fixtures"
	"strings"
	"testing"
	"time"
)

func TestRegionStrict(t *testing.T) {
	cases := []struct {
		name, region string
		want         bool
	}{
		// the right ones
		{"🇬🇧 London 01", "UK", true},
		{"UK-03", "UK", true},
		{"GB 02", "UK", true},
		{"英国 IEPL 01", "UK", true},
		{"United Kingdom | BGP", "UK", true},
		{"[UK] premium", "UK", true},
		{"🇩🇪 Frankfurt", "DE", true},
		{"德国 02", "DE", true},
		{"DE-01", "DE", true},
		// the traps
		{"Ukraine 01", "UK", false},
		{"🇺🇦 Kyiv UKR", "UK", false},
		{"Sweden 02", "DE", false},
		{"Dedicated 01", "DE", false},
		{"Denmark", "DE", false},
		{"剩余流量：100 GB", "UK", false},
		{"距离下次重置剩余：3 天 UK", "UK", false},
		{"套餐到期：2027-01-01 DE", "DE", false},
		{"Traffic: 100GB UK", "UK", false},
		{"uk-lowercase-code", "UK", false}, // short codes only as written in capitals
		// Real names from a provider: "流量倍率" is a rate multiplier in the name of a server.
		{"英国-UK-1-流量倍率:0.9", "UK", true},
		{"德国-DE-2-流量倍率:1", "DE", true},
		{"流量: 100GB UK", "UK", false},
		{"已用流量 3GB 英国", "UK", false},
		{"访问受限 REF: abc UK", "UK", false},
		{"JP 01", "UK", false},
		{"🇺🇸 US 01", "UK", false},
	}
	for _, c := range cases {
		if got := InRegion(c.name, c.region); got != c.want {
			t.Errorf("InRegion(%q, %s) = %v, want %v", c.name, c.region, got, c.want)
		}
	}
}

var nodes = []string{
	"🇬🇧 London 02", "🇬🇧 London 01", "Ukraine 01", "🇩🇪 Frankfurt 01", "Sweden 01",
	"剩余流量：100 GB", "🇺🇸 LA 01", "HK 01",
}

// The node names the user actually has: these must be found by the routes.
func TestUsersRealNodeNames(t *testing.T) {
	tags := []string{"英国-UK-1-流量倍率:0.9", "德国-DE-2-流量倍率:1", "美国-US-1-流量倍率:1", "剩余流量：88 GB", "距离下次重置剩余：5 天"}
	p, errs := Compile(fixtures.AIAccounts(), tags, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	for _, r := range p.Routes {
		switch r.ID {
		case "claude":
			if len(r.Nodes) != 1 || r.Nodes[0] != "英国-UK-1-流量倍率:0.9" {
				t.Errorf("claude nodes %v", r.Nodes)
			}
		case "google":
			if len(r.Nodes) != 1 || r.Nodes[0] != "德国-DE-2-流量倍率:1" {
				t.Errorf("google nodes %v", r.Nodes)
			}
		}
	}
	if len(p.Warnings) != 0 {
		t.Errorf("warnings %v", p.Warnings)
	}
}

func TestCompileDefault(t *testing.T) {
	p, errs := Compile(fixtures.AIAccounts(), nodes, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	routes := map[string]Route{}
	for _, r := range p.Routes {
		routes[r.ID] = r
	}
	uk := routes["claude"]
	if strings.Join(uk.Nodes, "|") != "🇬🇧 London 01|🇬🇧 London 02" {
		t.Errorf("claude nodes %v", uk.Nodes)
	}
	if uk.Chosen != "🇬🇧 London 01" { // stable: first by name, not by subscription order
		t.Errorf("claude chosen %q", uk.Chosen)
	}
	if !uk.Gate || !uk.IPv4 || uk.Closed {
		t.Errorf("claude should have an open gate and ipv4: %+v", uk)
	}
	// OpenAI is a UK route of its own, with its own gate.
	if o := routes["openai"]; o.Kind != "region" || !o.Gate || strings.Join(o.Nodes, "|") != strings.Join(uk.Nodes, "|") {
		t.Errorf("openai %+v", o)
	}
	// All of Google from Germany.
	if g := routes["google"]; len(g.Nodes) != 1 || g.Nodes[0] != "🇩🇪 Frankfurt 01" {
		t.Errorf("google nodes %v", g.Nodes)
	}
	if len(p.Warnings) != 0 {
		t.Errorf("warnings %v", p.Warnings)
	}
	if !p.UsesProcess() {
		t.Error("the default rules match processes")
	}

	// The shape of the first rules: UDP and IPv6 are blocked before anything is routed.
	first := p.Rules[0]
	if first.Target != TargetBlock || first.Network != "udp" || first.Matchers[0].Kind != "domain_suffix" {
		t.Errorf("first rule %+v", first)
	}
	var sawV6Block, sawClaudeProc, sawGoogle, sawCN bool
	claudeAt, googleAt, cnAt := -1, -1, -1
	for i, r := range p.Rules {
		for _, m := range r.Matchers {
			switch {
			case r.Target == TargetBlock && r.IPVersion == 6:
				sawV6Block = true
			case r.Target == "route-claude" && m.Kind == "process_name":
				sawClaudeProc = true
				if claudeAt < 0 {
					claudeAt = i
				}
			case r.Target == "route-google" && m.Kind == "domain_suffix":
				sawGoogle = true
				googleAt = i
			case r.Target == TargetDirect && m.Kind == "rule_set":
				sawCN = true
				cnAt = i
			}
		}
	}
	if !sawV6Block || !sawClaudeProc || !sawGoogle || !sawCN {
		t.Errorf("rules missing: v6 %v process %v google %v cn %v", sawV6Block, sawClaudeProc, sawGoogle, sawCN)
	}
	// Pinned services are decided before the general direct rule.
	if !(claudeAt < googleAt && googleAt < cnAt) {
		t.Errorf("order: claude %d, google %d, cn %d", claudeAt, googleAt, cnAt)
	}
	// The block rules come before the routes, so a route never wins over them.
	for i, r := range p.Rules {
		if r.Target == "route-claude" && i < 6 {
			t.Errorf("a claude route at %d, before the blocks", i)
		}
	}
}

func TestStrictNoNodes(t *testing.T) {
	p, errs := Compile("route gemini region DE\ngemini domain:gemini.google.com\nproxy domain:x.com\n", []string{"🇬🇧 London 01"}, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	if p.Rules[0].Target != TargetBlock {
		t.Errorf("no German node, yet target %q", p.Rules[0].Target)
	}
	if len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "blocked") {
		t.Errorf("warnings %v", p.Warnings)
	}
}

func TestChoiceKeptOrWarned(t *testing.T) {
	text := "route claude region UK\nclaude domain:claude.ai\n"
	tags := []string{"UK 01", "UK 02"}
	p, _ := Compile(text, tags, map[string]string{"claude": "UK 02"})
	if r := p.Routes[0]; r.Chosen != "UK 02" || r.Warn != "" {
		t.Errorf("kept choice: %+v", r)
	}
	// The provider dropped UK 02: do not slide to another exit unseen.
	p, _ = Compile(text, []string{"UK 01", "UK 03"}, map[string]string{"claude": "UK 02"})
	if r := p.Routes[0]; r.Chosen != "UK 01" || !strings.Contains(r.Warn, "UK 02") || len(p.Warnings) != 1 {
		t.Errorf("gone choice: %+v %v", r, p.Warnings)
	}
}

func TestCompileErrors(t *testing.T) {
	text := strings.Join([]string{
		"route Bad region UK",          // 1: upper-case id
		"route a region XX",            // 2: unknown region
		"route direct region UK",       // 3: built in
		"route ok region UK",           // 4: fine
		"route ok region DE",           // 5: twice
		"nope domain:x.com",            // 6: unknown route
		"ok domain",                    // 7: no colon
		"ok color:red",                 // 8: unknown kind
		"ok regex:(",                   // 9: bad regex
		"ok domain:a.com extra",        // 10: too many words
		"route r regex (",              // 11: bad regex
		"route r2 city London",         // 12: unknown route kind
		"ok domain:fine.com # comment", // fine
	}, "\n")
	_, errs := Compile(text, nil, nil)
	got := map[int]bool{}
	for _, e := range errs {
		got[e.Line] = true
	}
	for line := 1; line <= 12; line++ {
		if line == 4 {
			continue
		}
		if !got[line] {
			t.Errorf("line %d: no error", line)
		}
	}
	if got[4] || got[13] {
		t.Errorf("valid lines reported: %v", errs)
	}
}

func TestRegexRouteAndFast(t *testing.T) {
	p, errs := Compile("route corp regex (?i)^corp-\ncorp full:intra.example.com\nroute g region DE fast\n", []string{"corp-1", "other", "DE 1"}, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	for _, r := range p.Routes {
		switch r.ID {
		case "corp":
			if len(r.Nodes) != 1 || r.Nodes[0] != "corp-1" {
				t.Errorf("corp %v", r.Nodes)
			}
		case "g":
			if !r.Fast || r.Chosen != "" {
				t.Errorf("fast route: %+v", r)
			}
		}
	}
}

func TestFieldsAndComments(t *testing.T) {
	got := fields(stripComment(`claude process:"Claude Helper (GPU)" process:claude # a comment`))
	if len(got) != 3 || got[1] != "process:Claude Helper (GPU)" {
		t.Errorf("fields %q", got)
	}
	if c := stripComment(`claude regex:^a#b$ # trailing`); strings.TrimSpace(c) != "claude regex:^a#b$" {
		t.Errorf("comment: %q", c) // a '#' inside a word stays
	}
	if c := stripComment(`# whole line`); c != "" {
		t.Errorf("comment: %q", c)
	}
	if c := stripComment(`x process:"a # b"`); c != `x process:"a # b"` {
		t.Errorf("a '#' inside quotes stays: %q", c)
	}
}

func TestRuleFlagsMatchersAndMerge(t *testing.T) {
	text := `route c region UK
block udp domain:a.com domain:b.com
block udp domain:c.com
block domain:d.com
c process:"Claude Helper" process:claude path:/\.claude/ port:443
c ipv6 cidr:2607:6bc0::/32
`
	p, errs := Compile(text, []string{"UK 1"}, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	if len(p.Rules) != 4 {
		t.Fatalf("%d rules: %+v", len(p.Rules), p.Rules)
	}
	// Neighbours with the same flags and kind share one rule; a different flag does not.
	if r := p.Rules[0]; r.Network != "udp" || len(r.Matchers) != 1 || strings.Join(r.Matchers[0].Values, ",") != "a.com,b.com,c.com" {
		t.Errorf("merged rule %+v", r)
	}
	if r := p.Rules[1]; r.Network != "" || r.Matchers[0].Values[0] != "d.com" {
		t.Errorf("unflagged rule %+v", r)
	}
	// Different kinds are all required; the same kind is "or".
	r := p.Rules[2]
	if len(r.Matchers) != 3 || r.Matchers[0].Kind != "process_name" || strings.Join(r.Matchers[0].Values, "|") != "Claude Helper|claude" {
		t.Errorf("multi-matcher rule %+v", r)
	}
	if r.Matchers[2].Kind != "port" || r.Matchers[2].Values[0] != "443" {
		t.Errorf("port %+v", r.Matchers)
	}
	if r := p.Rules[3]; r.IPVersion != 6 || r.Target != "route-c" {
		t.Errorf("ipv6 rule %+v", r)
	}
}

func TestGate(t *testing.T) {
	text := "route claude region UK gate ipv4\nclaude domain:claude.ai\n"
	tags := []string{"UK 02", "UK 01", "DE 01"}

	p, errs := Compile(text, tags, map[string]string{"claude": "UK 02"})
	if errs != nil {
		t.Fatal(errs)
	}
	by := map[string]Route{}
	for _, r := range p.Routes {
		by[r.ID] = r
	}
	if r := by["claude"]; !r.Gate || r.Closed || r.Chosen != "UK 02" {
		t.Errorf("open gate: %+v", r)
	}

	// Shut the gate: the route is closed, the node choice is kept.
	p, _ = Compile(text, tags, map[string]string{"claude": "UK 02", "claude" + GateSuffix: "closed"})
	by = map[string]Route{}
	for _, r := range p.Routes {
		by[r.ID] = r
	}
	if r := by["claude"]; !r.Closed || r.Chosen != "UK 02" {
		t.Errorf("closed gate: %+v", r)
	}
	if len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "gate is closed") {
		t.Errorf("warnings %v", p.Warnings)
	}
	// A gate that the route does not have cannot be closed.
	p, _ = Compile("route a region UK\na domain:x.com\n", tags, map[string]string{"a" + GateSuffix: "closed"})
	if p.Routes[0].Closed {
		t.Error("closed a route that has no gate")
	}
}

func TestMoreErrors(t *testing.T) {
	cases := map[string]string{
		"route a like b\n":                             "'like' is gone",
		"route a region UK fast gate bogus\n":          "unknown option \"bogus\"",
		"block port:99999 domain:x.com\n":              "bad port",
		"block set:geoip-cn set:geosite-cn domain:x\n": "both a geoip- set and another set",
		"block process:x udp tcp\n":                    "", // flags alone are not a rule below
		"block udp\n":                                  "expected: <route> [flags] <kind>:<value>",
		"block color:red\n":                            "expected a flag",
		"block path:(\n":                               "bad regular expression",
		"block cidr:999.999.999.999\n":                 "bad address range",
		"block cidr:10.0.0.0/33\n":                     "bad address range",
	}
	for text, want := range cases {
		_, errs := Compile(text, []string{"UK 1"}, nil)
		if want == "" {
			continue
		}
		var all []string
		for _, e := range errs {
			all = append(all, e.Error())
		}
		if !strings.Contains(strings.Join(all, "\n"), want) {
			t.Errorf("%q: errors %v, want one with %q", text, all, want)
		}
	}
}

// Regions and notices are data in the text, not code: a user adds, replaces
// and extends them.
func TestUserDefinedRegionsAndNotices(t *testing.T) {
	text := `region PL flag:🇵🇱 name:poland name:warsaw code:PL
region UK name:"great britain" code:UK
notice "(?i)maintenance"
route pl region PL
route uk region UK
pl domain:example.pl
uk domain:example.uk
`
	tags := []string{"🇵🇱 Warsaw 1", "Poland-2", "🇬🇧 London 1", "Great Britain 2", "UK-3", "Warsaw maintenance PL"}
	p, errs := Compile(text, tags, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	by := map[string]Route{}
	for _, r := range p.Routes {
		by[r.ID] = r
	}
	if got := strings.Join(by["pl"].Nodes, "|"); got != "Poland-2|🇵🇱 Warsaw 1" {
		t.Errorf("a region of the user's own: %q", got)
	}
	// UK was replaced: London's flag is no longer enough, the new names are.
	if got := strings.Join(by["uk"].Nodes, "|"); got != "Great Britain 2|UK-3" {
		t.Errorf("a replaced region: %q", got)
	}
	if !p.IsNotice("Warsaw maintenance PL") || !p.IsNotice("剩余流量：1 GB") || p.IsNotice("Poland-2") {
		t.Error("notices: the user's pattern and the built-in ones must both apply")
	}
	if got := p.Regions(); !strings.Contains(strings.Join(got, " "), "PL") {
		t.Errorf("regions %v", got)
	}
	// What one text defines does not reach another.
	p2, _ := Compile("route pl region UK\n", []string{"Poland-2"}, nil)
	if p2 == nil || p2.IsNotice("Warsaw maintenance PL") || len(p2.Routes[0].Nodes) != 0 {
		t.Error("a text's own definitions leaked into another")
	}
}

func TestDefinitionErrors(t *testing.T) {
	cases := map[string]string{
		"region pl name:x\n":    "region id",
		"region PL\n":           "expected: region",
		"region PL color:red\n": "expected flag:",
		"region PL name:\n":     "expected flag:",
		"notice\n":              "expected: notice",
		"notice \"(\"\n":        "bad regular expression",
		"route a region ZZ\n":   "define one with: region",
	}
	for text, want := range cases {
		_, errs := Compile(text, nil, nil)
		var all []string
		for _, e := range errs {
			all = append(all, e.Error())
		}
		if !strings.Contains(strings.Join(all, "\n"), want) {
			t.Errorf("%q: %v, want %q", text, all, want)
		}
	}
}

// The names a real subscription gives: each must land in its country.
func TestRegionOfRealNames(t *testing.T) {
	for name, want := range map[string]string{
		"中国香港-HK-1-流量倍率:0.7": "HK", "中国香港-IPLC-HK-3-流量倍率:1": "HK", "日本-TY-3-2-流量倍率:0.6": "JP", "日本-FW-JP1-流量倍率:0.6": "JP",
		"美国-FW-US1-流量倍率:1": "US", "美国-V6-US3-流量倍率:1": "US", "德国-DE-2-流量倍率:1": "DE", "英国-UK-1-流量倍率:0.9": "UK",
		"新加坡-SG-3-1-流量倍率:1": "SG", "澳大利亚-AU-1-流量倍率:1.6": "AU", "中国台湾-TW-1-流量倍率:1.1": "TW", "荷兰-V6-NL1-流量倍率:0.5": "NL",
		"爱沙尼亚-EE-1-流量倍率:0.2": "EE", "🇯🇵 Tokyo 01": "JP",
		"有效期2099-01-01, 剩余 100.00 GB": "", // a notice is in no country
		"Node-17":                     "", // says none
		"Ukraine 01":                  "", // not the UK
	} {
		if got := RegionOf(name); got != want {
			t.Errorf("RegionOf(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestRegionsAreListedInTheOrderDefined(t *testing.T) {
	p, errs := Compile("region PL name:poland code:PL\nregion JP name:nippon code:JP\n", nil, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	order := p.RegionOrder()
	if len(order) < 10 || order[0] != "HK" || order[1] != "JP" {
		t.Fatalf("order %v", order)
	}
	// A new one goes last; a replaced one keeps its place.
	if order[len(order)-1] != "PL" {
		t.Errorf("a region of the user's own is not last: %v", order)
	}
	if order[1] != "JP" {
		t.Errorf("a replaced region moved: %v", order)
	}
	// The replacement is in force (Tokyo's flag no longer counts, its new name does).
	if p.RegionOf("🇯🇵 Tokyo") != "" || p.RegionOf("Nippon 1") != "JP" {
		t.Error("the replaced region's definition is not the one in force")
	}
	if got := p.RegionOf("Poland 1"); got != "PL" {
		t.Errorf("region of the user's own: %q", got)
	}
	// What one text defines does not reach another or the built-in set.
	if builtin.order[len(builtin.order)-1] == "PL" || RegionOf("Poland 1") != "" {
		t.Error("a text's regions leaked into the built-in ones")
	}
}

func TestRegionLabelsAreReadable(t *testing.T) {
	for id, want := range map[string]string{"JP": "日本 · JP", "HK": "香港 · HK", "UK": "英国 · UK", "US": "美国 · US", "ZZ": "ZZ"} {
		if got := RegionLabel(id); got != want {
			t.Errorf("RegionLabel(%s) = %q, want %q", id, got, want)
		}
	}
	// A region with no Chinese name is shown by its first name, capitalised, and its code.
	p, _ := Compile("region PL name:poland code:PL\nregion XX code:XX\n", nil, nil)
	if got := p.RegionLabel("PL"); got != "Poland · PL" {
		t.Errorf("%q", got)
	}
	if got := p.RegionLabel("XX"); got != "XX" {
		t.Errorf("%q", got)
	}
}

// Rules put before the text come first, and the text keeps its own line numbers.
func TestCompileWithPutsTheHeadFirstAndKeepsLines(t *testing.T) {
	head := "direct domain:a.com\nr domain:b.com\n"
	// An error in the text is at the text's own line.
	if _, errs := CompileWith(head, "route r region UK\n\nproxy domain:x.com\nnope domain:y.com\n", []string{"UK 01"}, nil); len(errs) != 1 || errs[0].Line != 4 || !strings.Contains(errs[0].Msg, "nope") {
		t.Fatalf("an error in the text is at its own line: %v", errs)
	}
	p, errs := CompileWith(head, "route r region UK\n\nproxy domain:x.com\n", []string{"UK 01"}, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	if p.Rules[0].Target != "direct" || p.Rules[0].Line > 0 || p.Rules[2].Line != 3 {
		t.Errorf("the head is first, at lines 0 or less; the text keeps its lines: %+v", p.Rules)
	}
	if p.Routes[0].Line != 1 {
		t.Errorf("route line %d, want 1", p.Routes[0].Line)
	}
	// A head naming a route that does not exist is an error at 0 or less.
	if _, errs = CompileWith("ghost domain:z.com\n", "direct set:geosite-cn\n", nil, nil); len(errs) != 1 || errs[0].Line > 0 {
		t.Errorf("the head's error is at a line before the text: %v", errs)
	}
	// With no head it is Compile.
	if p2, e2 := CompileWith("", "direct domain:a.com\n", nil, nil); e2 != nil || p2.Rules[0].Line != 1 {
		t.Error("no head is no change")
	}
}

func TestExitRangesOfARoute(t *testing.T) {
	plan, errs := Compile("route work region UK exit:198.51.100.0/24 exit:203.0.113.7 ipv4\nwork domain:a.example\n", []string{"UK 1"}, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	r := plan.Routes[0]
	if len(r.Exit) != 2 || r.Exit[0] != "198.51.100.0/24" || r.Exit[1] != "203.0.113.7/32" || !r.Gate || !r.IPv4 {
		t.Fatalf("route %+v", r)
	}
	for ip, want := range map[string]bool{"198.51.100.222": true, "198.51.100.0": true, "198.51.101.1": false, "203.0.113.7": true, "203.0.113.8": false, "2a01::1": false, "": false, "nonsense": false} {
		if r.ExitAllows(ip) != want {
			t.Errorf("ExitAllows(%q) = %v", ip, !want)
		}
	}
	if !(Route{}).ExitAllows("1.2.3.4") {
		t.Error("a route with no exit to keep to allows any")
	}
	if p, _ := Compile("route w region UK exit:198.51.100.9/24\nw domain:a.example\n", []string{"UK 1"}, nil); p.Routes[0].Exit[0] != "198.51.100.0/24" {
		t.Errorf("a range is kept as its network: %v", p.Routes[0].Exit)
	}
	for _, bad := range []string{"exit:", "exit:198.51.100", "exit:example.com", "exit:1.2.3.4/40"} {
		if _, errs := Compile("route w region UK "+bad+"\n", []string{"UK 1"}, nil); errs == nil {
			t.Errorf("%q was taken", bad)
		}
	}
}

func TestWhenAnExitIsChecked(t *testing.T) {
	p, errs := Compile("route a region UK exit:198.51.100.0/24 check:5m hours:22:00-06:00\nroute b region UK exit:1.2.3.4\n", []string{"UK 1"}, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	a, b := p.Routes[0], p.Routes[1]
	if a.Every() != 5*time.Minute || b.Every() != DefaultCheckEvery {
		t.Errorf("every %v %v", a.Every(), b.Every())
	}
	at := func(h, m int) time.Time { return time.Date(2026, 1, 1, h, m, 0, 0, time.Local) }
	if !a.InHours(at(23, 0)) || !a.InHours(at(5, 59)) || a.InHours(at(6, 0)) || a.InHours(at(12, 0)) {
		t.Error("hours across midnight read wrong")
	}
	if !b.InHours(at(3, 0)) {
		t.Error("no hours is all day")
	}
	for _, bad := range []string{
		"route a region UK check:5m",                       // no exit to check
		"route a region UK exit:1.2.3.4 check:1s",          // too often
		"route a region UK exit:1.2.3.4 check:often",       // not a time
		"route a region UK exit:1.2.3.4 hours:morning",     // not hours
		"route a region UK exit:1.2.3.4 hours:09:00-09:00", // no time at all
	} {
		if _, errs := Compile(bad+"\n", []string{"UK 1"}, nil); errs == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
