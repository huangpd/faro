package singcfg

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func buildDNS(t *testing.T, withNodes bool, choice DNSChoice, withCN bool) (map[string]any, map[string]any, []string) {
	t.Helper()
	set, _ := NewSettings(0)
	in := Input{DNS: choice, Sets: map[string]string{}}
	if withNodes {
		in.Nodes = []Outbound{{"n1", map[string]any{"type": "shadowsocks", "server": "a.example.com", "server_port": 1, "method": "aes-256-gcm", "password": "p"}}}
	}
	if withCN {
		files, _ := filepath.Glob("../../resources/rulesets/geosite-cn.srs")
		if len(files) == 0 {
			t.Skip("no resources/rulesets")
		}
		abs, _ := filepath.Abs(files[0])
		in.Sets["geosite-cn"] = abs
	}
	cfg, notes, err := Build(set, in)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(cfg, &m); err != nil {
		t.Fatal(err)
	}
	return m["dns"].(map[string]any), m["route"].(map[string]any), notes
}

func serversByTag(dns map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, s := range dns["servers"].([]any) {
		m := s.(map[string]any)
		out[m["tag"].(string)] = m
	}
	return out
}

func TestDNSSplitsDomesticAndForeign(t *testing.T) {
	dns, route, notes := buildDNS(t, true, DNSChoice{}, true)
	sv := serversByTag(dns)
	// Domestic: direct, encrypted, by IP (no name to look up to look names up).
	d := sv["domestic"]
	if d == nil || d["type"] != "https" || d["server"] != "223.5.5.5" || d["detour"] != nil {
		t.Errorf("domestic server %v", d)
	}
	if d["tls"].(map[string]any)["server_name"] != "dns.alidns.com" {
		t.Errorf("domestic tls %v", d["tls"])
	}
	// Foreign: encrypted, and THROUGH the proxy.
	f := sv["foreign"]
	if f == nil || f["type"] != "https" || f["server"] != "8.8.8.8" || f["detour"] != "proxy" {
		t.Errorf("foreign server %v", f)
	}
	// Names in China go to the domestic one; everything else to the foreign one.
	rules := dns["rules"].([]any)
	if len(rules) != 1 {
		t.Fatalf("dns rules %v", rules)
	}
	r := rules[0].(map[string]any)
	if r["server"] != "domestic" || r["rule_set"].([]any)[0] != "geosite-cn" {
		t.Errorf("rule %v", r)
	}
	if dns["final"] != "foreign" {
		t.Errorf("final %v", dns["final"])
	}
	// The proxy servers' own names are looked up by the domestic server, directly.
	if route["default_domain_resolver"] != "domestic" {
		t.Errorf("resolver %v", route["default_domain_resolver"])
	}
	if len(notes) != 0 {
		t.Errorf("notes %v", notes)
	}
}

// The point of it: the system's resolver is not in the config at all, so there
// is nothing for a name to fall back to.
func TestTheSystemResolverIsNeverUsed(t *testing.T) {
	for _, tc := range []struct {
		nodes, cn bool
	}{{true, true}, {true, false}, {false, false}, {false, true}} {
		dns, route, _ := buildDNS(t, tc.nodes, DNSChoice{}, tc.cn)
		for tag, s := range serversByTag(dns) {
			if s["type"] == "local" || s["type"] == "dhcp" || s["type"] == "resolved" {
				t.Errorf("nodes=%v cn=%v: server %q is the system's (%v)", tc.nodes, tc.cn, tag, s["type"])
			}
			if s["type"] == "udp" || s["type"] == "tcp" {
				t.Errorf("nodes=%v cn=%v: server %q is not encrypted (%v)", tc.nodes, tc.cn, tag, s["type"])
			}
		}
		if route["default_domain_resolver"] == "local" || route["default_domain_resolver"] == nil {
			t.Errorf("resolver %v", route["default_domain_resolver"])
		}
		if f := dns["final"]; f != "foreign" && f != "domestic" {
			t.Errorf("final %v", f)
		}
	}
}

func TestDNSWithoutNodesIsDomesticOnly(t *testing.T) {
	dns, _, _ := buildDNS(t, false, DNSChoice{}, false)
	if sv := serversByTag(dns); len(sv) != 1 || sv["domestic"] == nil || sv["foreign"] != nil {
		t.Errorf("servers %v: with no proxy there is nothing to go through", sv)
	}
	if dns["final"] != "domestic" {
		t.Errorf("final %v", dns["final"])
	}
}

// Without the rule set that tells names in China, nothing is sent to the domestic
// server by name: all of it goes through the proxy, which leaks nothing, and the
// person is told.
func TestDNSWithoutTheChinaRuleSetSendsEverythingThroughTheProxy(t *testing.T) {
	dns, _, notes := buildDNS(t, true, DNSChoice{}, false)
	if _, ok := dns["rules"]; ok {
		t.Errorf("a dns rule with no rule set to match: %v", dns["rules"])
	}
	if dns["final"] != "foreign" {
		t.Errorf("final %v", dns["final"])
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "DNS") {
		t.Errorf("notes %v", notes)
	}
}

func TestChoosingDNSServers(t *testing.T) {
	dns, _, _ := buildDNS(t, true, DNSChoice{Domestic: "dnspod", Foreign: "cloudflare"}, true)
	sv := serversByTag(dns)
	if sv["domestic"]["server"] != "120.53.53.53" || sv["domestic"]["tls"].(map[string]any)["server_name"] != "doh.pub" {
		t.Errorf("domestic %v", sv["domestic"])
	}
	if sv["foreign"]["server"] != "1.1.1.1" || sv["foreign"]["tls"].(map[string]any)["server_name"] != "cloudflare-dns.com" {
		t.Errorf("foreign %v", sv["foreign"])
	}
	// An unknown id is the first of its side, not an error and not nothing.
	dns, _, _ = buildDNS(t, true, DNSChoice{Domestic: "no-such", Foreign: ""}, true)
	sv = serversByTag(dns)
	if sv["domestic"]["server"] != "223.5.5.5" || sv["foreign"]["server"] != "8.8.8.8" {
		t.Errorf("defaults: %v %v", sv["domestic"], sv["foreign"])
	}
	// A foreign id on the domestic side does not give a foreign server there.
	dns, _, _ = buildDNS(t, true, DNSChoice{Domestic: "google"}, true)
	if serversByTag(dns)["domestic"]["server"] != "223.5.5.5" {
		t.Error("a foreign server was used as the domestic one")
	}
}

func TestDNSListIsWellFormed(t *testing.T) {
	servers := DNSServers()
	if len(DNSOptions("domestic")) < 2 || len(DNSOptions("foreign")) < 2 {
		t.Fatalf("too few: %v", servers)
	}
	seen := map[string]bool{}
	for _, s := range servers {
		if s.Side != "domestic" && s.Side != "foreign" {
			t.Errorf("side %q", s.Side)
		}
		if seen[s.ID] {
			t.Errorf("id %q twice", s.ID)
		}
		seen[s.ID] = true
		// An IP, so that no name is needed to look names up.
		parts := strings.Split(s.Address, ".")
		if len(parts) != 4 {
			t.Errorf("%s: %q is not an IPv4 address", s.ID, s.Address)
		}
		if s.TLSName == "" || s.Label == "" {
			t.Errorf("%s: %+v", s.ID, s)
		}
	}
}

// Every combination must be a config the real core accepts.
func TestEveryDNSChoiceIsAcceptedByTheCore(t *testing.T) {
	bin, _ := filepath.Abs("../../bin/sing-box")
	if _, err := os.Stat(bin); err != nil {
		t.Skip("no bin/sing-box")
	}
	for _, d := range DNSOptions("domestic") {
		for _, f := range DNSOptions("foreign") {
			for _, nodes := range []bool{true, false} {
				for _, cn := range []bool{true, false} {
					set, _ := NewSettings(0)
					in := Input{DNS: DNSChoice{Domestic: d.ID, Foreign: f.ID}, Sets: map[string]string{}}
					if nodes {
						in.Nodes = []Outbound{{"n1", map[string]any{"type": "shadowsocks", "server": "a.example.com", "server_port": 1, "method": "aes-256-gcm", "password": "p"}}}
						in.Nodes[0].Out["domain_resolver"] = nil
					}
					if cn {
						abs, _ := filepath.Abs("../../resources/rulesets/geosite-cn.srs")
						if _, err := os.Stat(abs); err != nil {
							continue
						}
						in.Sets["geosite-cn"] = abs
					}
					cfg, _, err := Build(set, in)
					if err != nil {
						t.Fatal(err)
					}
					p := filepath.Join(t.TempDir(), "c.json")
					os.WriteFile(p, cfg, 0o600)
					if out, err := exec.Command(bin, "check", "-c", p).CombinedOutput(); err != nil {
						t.Errorf("%s+%s nodes=%v cn=%v: %v\n%s", d.ID, f.ID, nodes, cn, err, out)
					}
				}
			}
		}
	}
}
