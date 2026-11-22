package singcfg

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed dns.list
var dnsList string

// DNSServer is one DNS server that can be used over HTTPS.
type DNSServer struct {
	Side    string // "domestic" or "foreign"
	ID      string
	Address string // an IP
	TLSName string
	Label   string
}

// DNSServers lists the servers in dns.list, in order.
func DNSServers() []DNSServer {
	var out []DNSServer
	for _, line := range strings.Split(dnsList, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		out = append(out, DNSServer{Side: f[0], ID: f[1], Address: f[2], TLSName: f[3], Label: strings.Join(f[4:], " ")})
	}
	return out
}

// DNSChoice is which servers to use. An empty or unknown id is the first of
// that side.
type DNSChoice struct {
	Domestic, Foreign string
}

// pick finds the server of a side by id, or the first of the side.
func pick(side, id string) (DNSServer, bool) {
	var first DNSServer
	found := false
	for _, s := range DNSServers() {
		if s.Side != side {
			continue
		}
		if !found {
			first, found = s, true
		}
		if s.ID == id {
			return s, true
		}
	}
	return first, found
}

// DNSOptions lists the servers of a side, for choosing among.
func DNSOptions(side string) []DNSServer {
	var out []DNSServer
	for _, s := range DNSServers() {
		if s.Side == side {
			out = append(out, s)
		}
	}
	return out
}

// dnsConfig makes the "dns" section, and says which server resolves the names
// the core itself must look up (the proxy servers' own addresses).
//
// Names in China are asked of the domestic server, directly. Every other name
// is asked of the foreign server through the proxy: the question leaves this
// Mac inside the tunnel, so neither the network nor a resolver in China sees
// what was looked up, and the answer is the real one, not a poisoned one.
// The system's resolver is never used: there is no "local" server in it, so
// nothing can fall back to it.
//
// hasProxy says whether there is a proxy group to go through; without nodes
// everything goes direct and the domestic server answers for all. cnSet is the
// rule set that tells names in China, "" when it is not installed: then all
// names go through the proxy, which is slower for names in China but leaks
// nothing.
func dnsConfig(choice DNSChoice, hasProxy bool, cnSet string) (dns map[string]any, resolver string, notes []string) {
	dom, _ := pick("domestic", choice.Domestic)
	servers := []any{map[string]any{
		"type": "https", "tag": "domestic", "server": dom.Address, "tls": map[string]any{"server_name": dom.TLSName},
	}}
	dns = map[string]any{"servers": servers, "strategy": "prefer_ipv4"}
	if !hasProxy {
		dns["final"] = "domestic"
		return dns, "domestic", nil
	}
	foreign, _ := pick("foreign", choice.Foreign)
	dns["servers"] = append(servers, map[string]any{
		"type": "https", "tag": "foreign", "server": foreign.Address, "tls": map[string]any{"server_name": foreign.TLSName},
		"detour": TagProxy,
	})
	dns["final"] = "foreign"
	if cnSet != "" {
		dns["rules"] = []any{map[string]any{"rule_set": []string{cnSet}, "action": "route", "server": "domestic"}}
	} else {
		notes = append(notes, "DNS: the rule set that tells names in China is not installed, so every name is looked up through the proxy")
	}
	return dns, "domestic", notes
}

func (s DNSServer) String() string { return fmt.Sprintf("%s (%s)", s.Label, s.Address) }
