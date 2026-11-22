package rulesdoc

import (
	"regexp"
	"strings"
	"testing"

	"faro/internal/fixtures"
	"faro/internal/rules"
	"faro/internal/ruleset"
)

// examples returns the indented code blocks of the spec's Examples section,
// each a run of lines indented by four spaces, with the blank lines between.
func examples(t *testing.T) []string {
	t.Helper()
	_, rest, ok := strings.Cut(Spec(), "## Examples")
	if !ok {
		t.Fatal("no Examples section in the spec")
	}
	var blocks []string
	var cur []string
	for _, line := range strings.Split(rest, "\n") {
		if strings.HasPrefix(line, "    ") {
			cur = append(cur, strings.TrimPrefix(line, "    "))
			continue
		}
		if len(cur) > 0 {
			blocks = append(blocks, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	if len(cur) > 0 {
		blocks = append(blocks, strings.Join(cur, "\n"))
	}
	return blocks
}

// The spec is what an AI learns the language from: every example in it must be
// accepted by the real compiler, or the spec has drifted from the language.
func TestEveryExampleInTheSpecCompiles(t *testing.T) {
	blocks := examples(t)
	if len(blocks) < 6 {
		t.Fatalf("only %d examples found", len(blocks))
	}
	// The examples share route names (mirror follows work, and so on): compile the
	// whole of them together, as a person would paste them one after another,
	// then each alone where it stands alone.
	nodes := []string{"🇬🇧 London 01", "🇩🇪 Frankfurt 01", "🇵🇱 Warsaw 01", "lab-1"}
	all := strings.Join(blocks, "\n\n")
	if _, errs := rules.Compile(all, nodes, nil); errs != nil {
		t.Errorf("the examples together: %v", errs)
	}
	for i, b := range blocks {
		if strings.Contains(b, "route mirror like work") || strings.Contains(b, "work process:") {
			continue // these refer to a route another example defines
		}
		if _, errs := rules.Compile(b, nodes, nil); errs != nil {
			t.Errorf("example %d:\n%s\n%v", i, b, errs)
		}
	}
}

// Every kind and flag the compiler knows must be in the spec, and the other way:
// a kind that is only in the code is one an AI will never write.
func TestSpecNamesEveryKindFlagAndOption(t *testing.T) {
	for _, kind := range []string{"domain:", "full:", "keyword:", "regex:", "cidr:", "set:", "process:", "path:", "port:"} {
		if !strings.Contains(Spec(), kind) {
			t.Errorf("the spec does not mention %s", kind)
		}
		// and the compiler accepts it
		val := "x"
		switch kind {
		case "port:":
			val = "443"
		case "cidr:":
			val = "10.0.0.0/8"
		}
		if _, errs := rules.Compile("direct "+kind+val+"\n", nil, nil); errs != nil {
			t.Errorf("%s%s: %v", kind, val, errs)
		}
	}
	for _, w := range []string{"udp", "tcp", "ipv4", "ipv6", "fast", "gate", "like", "regex", "region", "proxy", "auto", "direct", "block"} {
		if !regexp.MustCompile(`\b` + w + `\b`).MatchString(Spec()) {
			t.Errorf("the spec does not mention %q", w)
		}
	}
	// What the spec tells an AI not to do, it must not do itself.
	if strings.Contains(Spec(), `"outbounds"`) || strings.Contains(Spec(), "DOMAIN-SUFFIX") {
		t.Error("the spec shows another format")
	}
}

func TestPromptCarriesTheLanguageTheSetupAndAPlaceForTheRequest(t *testing.T) {
	text := fixtures.AIAccounts()
	nodes := []string{"🇬🇧 London 01", "🇬🇧 London 02", "🇩🇪 Frankfurt 01", "🇯🇵 Tokyo 01", "🇺🇸 LA 01"}
	p := Prompt(Context{Rules: text, Regions: rules.RegionIDs(), NodeNames: nodes, RuleSets: []string{"geosite-cn", "geoip-cn"}})

	for _, want := range []string{
		"Faro 规则语法", "route <id> region <XX>", "FIRST that matches decides", // the language
		"可用的地区:", "UK", "DE", // what it knows
		"Rule sets（set: 只能用这些，不要自己编）: geosite-cn geoip-cn",
		"route claude region UK gate ipv4", "route openai like claude", "route google region DE", // their routes
		"🇬🇧 London 01", "🇯🇵 Tokyo 01", // node names
		"我现在的完整规则", "我的需求", // where the person writes
		"只输出规则文本",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
	// Comments in the rules do not leak into the list of routes.
	for _, line := range routeLines("route a region UK # a comment\n# route hidden region DE\nroute \"x\"\n") {
		if strings.Contains(line, "comment") || strings.Contains(line, "hidden") {
			t.Errorf("route line %q", line)
		}
	}
	// Nothing but names: the prompt is built from a Context that has nothing else,
	// and it says so.
	if strings.Contains(p, "server") && strings.Contains(p, "password:") {
		t.Error("the prompt looks to hold node credentials")
	}
	// No routes yet: it says so, instead of an empty list.
	if q := Prompt(Context{Rules: "direct set:geosite-cn\n"}); !strings.Contains(q, "我还没有定义路由") {
		t.Error("a setup with no routes is not said")
	}
	// Many nodes: a spread of names, not all of them.
	var many []string
	for i := 0; i < 100; i++ {
		many = append(many, "Node "+string(rune('A'+i%26))+string(rune('a'+i/26)))
	}
	q := Prompt(Context{NodeNames: many})
	if n := strings.Count(q, "\n    Node "); n != 12 {
		t.Errorf("%d node names, want 12", n)
	}
	if !strings.Contains(q, "一共 100 个") {
		t.Error("the total is not said")
	}
}

// A rule set the app does not have is silently skipped when the config is made:
// the spec must not show one, and the prompt must name the ones that exist.
func TestSpecNamesOnlyRuleSetsThatExist(t *testing.T) {
	known := map[string]bool{}
	for name := range ruleset.Files {
		known[name] = true
	}
	for _, m := range regexp.MustCompile(`set:([A-Za-z0-9_!-]+)`).FindAllStringSubmatch(Spec(), -1) {
		if strings.HasPrefix(m[1], "<") {
			continue
		}
		if !known[m[1]] {
			t.Errorf("the spec uses set:%s, which Faro does not have", m[1])
		}
	}
	if q := Prompt(Context{}); !strings.Contains(q, "没有安装任何规则集，不要使用 set:") {
		t.Error("no rule sets installed is not said")
	}
}
