package engine

import (
	"sort"
	"strings"
	"testing"
)

func TestNaturalOrder(t *testing.T) {
	// Each list is already in the order a person expects.
	for _, want := range [][]string{
		{"US-1", "US-2", "US-3", "US-10", "US-11", "US-100"},
		{"日本-TY-1", "日本-TY-2", "日本-TY-3", "日本-TY-3-2", "日本-TY-4-HY2", "日本-TY-5-HY2", "日本-TY-10"},
		{"HK 1", "HK 02", "HK 3"},         // leading zeros are not a bigger number
		{"a", "A1", "b"},                  // case is not a difference of order
		{"节点2", "节点10", "节点A"},            // digits before letters, Chinese by code point
		{"x", "x1", "x1-1", "x1-2", "x2"}, // shorter first
	} {
		got := append([]string(nil), want...)
		// shuffle by reversing, then sort
		for i, j := 0, len(got)-1; i < j; i, j = i+1, j-1 {
			got[i], got[j] = got[j], got[i]
		}
		sort.SliceStable(got, func(i, j int) bool { return NaturalLess(got[i], got[j]) })
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("sorted:\n got  %v\n want %v", got, want)
		}
	}
	// A sort needs a strict order: nothing is less than itself, and one is less in only one direction.
	names := []string{"US-1", "US-01", "us-1", "US-10", "US-2", "", "a", "A", "日本-1"}
	for _, a := range names {
		if NaturalLess(a, a) {
			t.Errorf("%q < itself", a)
		}
		for _, b := range names {
			if a != b && NaturalLess(a, b) && NaturalLess(b, a) {
				t.Errorf("%q and %q are each less than the other", a, b)
			}
		}
	}
}

// The user's real subscription, in the order the provider lists it: countries
// mixed together, a notice first.
func TestGroupingARealSubscription(t *testing.T) {
	names := []string{
		"有效期2028-11-06, 剩余 45.01 GB", "荷兰-NL-4-流量倍率:0.1", "爱沙尼亚-EE-1-流量倍率:0.2", "中国香港-HK-2-流量倍率:0.7", "中国香港-HK-1-流量倍率:0.7",
		"荷兰-NL-1-流量倍率:0.5", "日本-TY-3-2-流量倍率:0.6", "日本-TY-3-流量倍率:0.6", "日本-TY-10-流量倍率:1", "日本-TY-2-流量倍率:1", "日本-TY-1-流量倍率:1.2",
		"美国-US-2-流量倍率:1", "美国-FW-US1-流量倍率:1", "美国-US-1-流量倍率:1", "德国-DE-2-流量倍率:1", "英国-UK-1-流量倍率:0.9",
		"新加坡-SG-2-流量倍率:1", "新加坡-SG-1-流量倍率:1", "澳大利亚-AU-1-流量倍率:1.6", "中国台湾-TW-1-流量倍率:1.1", "Node-17",
	}
	var nodes []Node
	for _, n := range names {
		nodes = append(nodes, Node{Tag: n, Kind: "trojan"})
	}
	groups, notices := GroupNodes(nodes, nil)

	if len(notices) != 1 || !strings.HasPrefix(notices[0], "有效期") {
		t.Errorf("notices %v", notices)
	}
	var order []string
	byRegion := map[string][]string{}
	total := 0
	for _, g := range groups {
		order = append(order, g.Region)
		for _, n := range g.Nodes {
			byRegion[g.Region] = append(byRegion[g.Region], n.Tag)
			total++
			if strings.HasPrefix(n.Tag, "有效期") {
				t.Error("a notice is in a country")
			}
		}
	}
	// Countries in the order defined, the ones without a country last.
	if want := "HK JP SG TW US UK DE NL AU EE "; strings.Join(order[:len(order)-1], " ")+" " != want || order[len(order)-1] != "" {
		t.Errorf("order %q", order)
	}
	if total != len(names)-1 {
		t.Errorf("%d nodes grouped, want %d (every one but the notice)", total, len(names)-1)
	}
	// In natural order inside a country: the numbers rise (1, 2, 3, 10), and TY-3 and
	// TY-3-2 stay together. Which of those two is first is not decided here: the name
	// after the number ("-流量倍率") is the provider's, and a digit sorts before a letter.
	jp := strings.Join(byRegion["JP"], "|")
	at := func(s string) int { return strings.Index(jp, s) }
	if at("TY-1-") != strings.Index(jp, "日本-TY-1-")+len("日本-") || !(at("TY-1-") < at("TY-2-") && at("TY-2-") < at("TY-3-") && at("TY-2-") < at("TY-3-2-") &&
		at("TY-3-") < at("TY-10-") && at("TY-3-2-") < at("TY-10-")) {
		t.Errorf("JP order: %v", byRegion["JP"])
	}
	if got := byRegion["US"]; len(got) != 3 || !strings.Contains(got[0], "FW-US1") {
		t.Errorf("US: %v", got) // "FW" sorts before "US" by letter
	}
	if got := byRegion[""]; len(got) != 1 || got[0] != "Node-17" {
		t.Errorf("no country: %v", got)
	}
	// Labels read as a person says them.
	for _, g := range groups {
		switch g.Region {
		case "JP":
			if g.Label != "日本 · JP" {
				t.Errorf("label %q", g.Label)
			}
		case "":
			if g.Label != "其他" {
				t.Errorf("label %q", g.Label)
			}
		}
	}
}

// The engine keeps the grouping current as subscriptions change, and its
// snapshot is a copy.
func TestEngineGroupsFollowTheSubscription(t *testing.T) {
	e := newEngine(t, (&fakeNetworksetup{}).run)
	if g := e.State().NodeGroups; len(g) != 0 {
		t.Fatalf("groups before any subscription: %v", g)
	}
	e.addAndWait(t, subFile(t, "日本-TY-2", "日本-TY-10", "日本-TY-1", "美国-US-1", "剩余流量：1 GB", "无名节点"), 6)
	st := e.State()
	if len(st.NodeGroups) != 3 || st.NodeGroups[0].Region != "JP" || st.NodeGroups[1].Region != "US" || st.NodeGroups[2].Region != "" {
		t.Fatalf("groups %+v", st.NodeGroups)
	}
	var jp []string
	for _, n := range st.NodeGroups[0].Nodes {
		jp = append(jp, n.Tag)
	}
	if strings.Join(jp, "|") != "日本-TY-1|日本-TY-2|日本-TY-10" {
		t.Errorf("JP %v", jp)
	}
	if len(st.Notices) != 1 || len(st.Nodes) != 6 {
		t.Errorf("notices %v, nodes %d", st.Notices, len(st.Nodes))
	}
	st.NodeGroups[0].Nodes[0].Tag = "changed"
	if e.State().NodeGroups[0].Nodes[0].Tag == "changed" {
		t.Error("the snapshot shares its groups")
	}
	// A region of the user's own is a group of its own, after the built-in ones, and
	// the node it names leaves "other": now it has a country.
	if errs := e.ApplyRules("region ZZ name:无名 code:ZZ\n"); errs != nil {
		t.Fatal(errs)
	}
	g := e.State().NodeGroups
	if len(g) != 3 || g[2].Region != "ZZ" || len(g[2].Nodes) != 1 {
		t.Errorf("groups with a region of the user's own: %+v", g)
	}
	for _, x := range g {
		if x.Region == "" {
			t.Error("a node with a country is still listed under other")
		}
	}
}
