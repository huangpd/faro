// Package singcfg builds the sing-box configuration. Keep it to fields
// sing-box 1.14 accepts: the 1.11–1.13 releases removed the legacy inbound
// `sniff` options and the special block/dns outbounds.
package singcfg

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strconv"

	"faro/internal/rules"
)

// Settings are what one run of the core needs.
type Settings struct {
	MixedPort int    // local HTTP+SOCKS inbound on 127.0.0.1
	APIPort   int    // Clash API controller on 127.0.0.1
	Secret    string // bearer token for the controller
	// CheckPorts are the ports of the check entrances, by route group: a proxy
	// that goes out through the route's node whatever its gate, so that where it
	// leaves from can be asked while it is cut (AddCheck).
	CheckPorts map[string]int
}

// AddCheck gives a route group a check entrance on a free port.
func (s *Settings) AddCheck(group string) error {
	p, err := freePort()
	if err != nil {
		return err
	}
	if s.CheckPorts == nil {
		s.CheckPorts = map[string]int{}
	}
	s.CheckPorts[group] = p
	return nil
}

// CheckTag is the tag of a route group's check entrance, and of the group it
// goes out by: the route's nodes, without the gate.
func CheckTag(group string) string { return "check-" + group }

// NewSettings makes the settings of a run. mixed is the port of the proxy
// the user's tools are pointed at, so it must not change from run to run:
// 0 picks a free one, and a port that is taken is an error, not a reason to
// move. The controller's port and secret are new every time.
func NewSettings(mixed int) (Settings, error) {
	if mixed == 0 {
		var err error
		if mixed, err = freePort(); err != nil {
			return Settings{}, err
		}
	} else if err := portFree(mixed); err != nil {
		return Settings{}, err
	}
	api, err := freePort()
	if err != nil {
		return Settings{}, err
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return Settings{}, err
	}
	return Settings{MixedPort: mixed, APIPort: api, Secret: hex.EncodeToString(b)}, nil
}

// CheckPort says whether a port can be taken on this machine now.
func CheckPort(port int) error { return portFree(port) }

func portFree(port int) error {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("port %d is in use (another proxy client?): %w", port, err)
	}
	return l.Close()
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// ProbeURL is what nodes are tested against. It is the host of no service
// whose account is tied to a location: a test through one country's node must
// not be a request that such a service sees from there.
const ProbeURL = "https://cp.cloudflare.com/generate_204"

// TraceURL answers, among other things, the address a request came from: what
// the world sees of a route's exit. The same host as ProbeURL.
const TraceURL = "https://cp.cloudflare.com/cdn-cgi/trace"

// cnRuleSet is the rule set that tells names in China: what the DNS asks the
// domestic server for. It is the one the base rules send direct.
const cnRuleSet = "geosite-cn"

// Tags of the groups every config has. Node tags never use them.
const (
	TagProxy  = "proxy" // the selector the user picks a node in
	TagAuto   = "auto"  // the url-test group: fastest, with failover
	TagDirect = "direct"
)

// Outbound is a node ready for the config: its tag and sing-box outbound.
type Outbound struct {
	Tag string
	Out map[string]any
}

// Input is what the config is made of.
type Input struct {
	Nodes []Outbound
	// Plan is the routing text compiled; nil goes straight to the main group.
	Plan *rules.Plan
	// Choices is the node of each route, "proxy" for the main group.
	Choices map[string]string
	// Sets maps a rule set's name to its file; one not here is left out.
	Sets map[string]string
	// DNS is which servers resolve names; empty is the first of each side.
	DNS DNSChoice
}

// Build returns the config JSON, and notes on what it had to leave out.
// With no nodes it only goes direct; with nodes, traffic goes by the plan's
// rules, and what they do not match goes to the "proxy" selector, whose
// first choice is the automatic group, except for private addresses.
func Build(s Settings, in Input) (cfg []byte, notes []string, err error) {
	nodes := in.Nodes
	outbounds := []any{map[string]any{"type": "direct", "tag": TagDirect}}
	final := TagDirect
	var routeRules []any
	sets := map[string]any{}
	findProcess := false
	// The groups of ipv4 routes: what is sent to them leaves the node over IPv4,
	// looked up by a DNS server reached through the same group.
	var vias []dnsVia
	isIPv4Group := map[string]bool{}
	var checkRules []any
	var checkIns []any

	if len(nodes) > 0 {
		// Nodes of an ipv4 route resolve the names they dial as IPv4 only.
		ipv4 := map[string]bool{}
		gated := false
		if in.Plan != nil {
			for _, r := range in.Plan.Routes {
				for _, n := range r.Nodes {
					ipv4[n] = ipv4[n] || r.IPv4
				}
				gated = gated || (r.Gate && len(r.Nodes) > 0)
			}
			findProcess = in.Plan.UsesProcess()
		}
		tags := make([]string, 0, len(nodes))
		selected := in.Choices[TagProxy]
		def := TagAuto
		for _, n := range nodes {
			o := map[string]any{"tag": n.Tag}
			for k, v := range n.Out {
				o[k] = v
			}
			if ipv4[n.Tag] && o["domain_resolver"] == nil {
				o["domain_resolver"] = map[string]any{"server": "domestic", "strategy": "ipv4_only"}
			}
			outbounds = append(outbounds, o)
			tags = append(tags, n.Tag)
			if n.Tag == selected {
				def = selected
			}
		}
		// The fastest-of group leaves out the notices providers list as nodes
		// ("剩余 100 GB"): they answer, but are not servers to send traffic through.
		autoTags := make([]string, 0, len(tags))
		for _, t := range tags {
			if !in.Plan.IsNotice(t) { // a nil plan uses the built-in patterns
				autoTags = append(autoTags, t)
			}
		}
		if len(autoTags) == 0 {
			autoTags = tags
		}
		outbounds = append(outbounds,
			map[string]any{"type": "urltest", "tag": TagAuto, "outbounds": autoTags,
				"url": ProbeURL, "interval": "10m", "idle_timeout": "30m"},
			map[string]any{"type": "selector", "tag": TagProxy, "outbounds": append([]string{TagAuto}, tags...), "default": def},
		)
		final = TagProxy
		if gated {
			// What a closed gate points at: connections to it end at once.
			outbounds = append(outbounds, map[string]any{"type": "block", "tag": rules.GateTag})
		}

		if in.Plan != nil {
			for _, r := range in.Plan.Routes {
				if len(r.Nodes) == 0 {
					continue // its rules were turned into blocks
				}
				if r.IPv4 {
					vias = append(vias, dnsVia{viaDNS(r.Group()), r.Group()})
					isIPv4Group[r.Group()] = true
				}
				choices := append([]string{}, r.Nodes...)
				def := r.Chosen
				if r.Fast {
					fast := r.Group() + "-fast"
					outbounds = append(outbounds, map[string]any{"type": "urltest", "tag": fast, "outbounds": r.Nodes,
						"url": ProbeURL, "interval": "10m", "idle_timeout": "30m"})
					choices = append([]string{fast}, choices...)
					if def == "" {
						def = fast
					}
				}
				if r.Gate {
					choices = append(choices, rules.GateTag)
					// A route whose exit is checked starts cut, and is opened by the check
					// once it has seen where the route leaves from: nothing goes out before.
					if r.Closed || (len(r.Exit) > 0 && s.CheckPorts[r.Group()] != 0) {
						def = rules.GateTag
					}
				}
				outbounds = append(outbounds, map[string]any{"type": "selector", "tag": r.Group(), "outbounds": choices, "default": def})

				// A route whose exit is checked has an entrance of its own for the check,
				// which goes out by the route's nodes but never by its gate: a route that
				// is cut can still be asked where it would leave from.
				if port := s.CheckPorts[r.Group()]; port != 0 && len(r.Exit) > 0 {
					ct := CheckTag(r.Group())
					var nodes []string
					for _, c := range choices {
						if c != rules.GateTag {
							nodes = append(nodes, c)
						}
					}
					cdef := r.Chosen
					if cdef == "" {
						cdef = nodes[0]
					}
					outbounds = append(outbounds, map[string]any{"type": "selector", "tag": ct, "outbounds": nodes, "default": cdef})
					checkIns = append(checkIns, map[string]any{"type": "mixed", "tag": ct, "listen": "127.0.0.1", "listen_port": port})
					if r.IPv4 {
						vias = append(vias, dnsVia{viaDNS(ct), ct})
						checkRules = append(checkRules, map[string]any{"inbound": []string{ct}, "action": "resolve", "server": viaDNS(ct), "strategy": "ipv4_only"})
					}
					checkRules = append(checkRules, map[string]any{"inbound": []string{ct}, "action": "route", "outbound": ct})
				}
			}
		}
	}

	// sniff first, so rules on names see the name of a connection to an
	// address; private addresses next; then the user's rules in order.
	routeRules = append(routeRules, map[string]any{"action": "sniff"})
	routeRules = append(routeRules, checkRules...) // the checks go their own way, before all else
	routeRules = append(routeRules, map[string]any{"ip_is_private": true, "action": "route", "outbound": TagDirect})
	if len(nodes) > 0 && in.Plan != nil {
		resolved := false
	rules:
		for _, r := range in.Plan.Rules {
			rule := map[string]any{}
			needsResolve := false
			for _, m := range r.Matchers {
				switch m.Kind {
				case "rule_set", "rule_set_ip":
					var have []string
					for _, name := range m.Values {
						if path, ok := in.Sets[name]; ok {
							sets[name] = map[string]any{"type": "local", "tag": name, "format": "binary", "path": path}
							have = append(have, name)
						} else {
							notes = append(notes, fmt.Sprintf("line %d: rule set %q is not installed, rule skipped", r.Line, name))
						}
					}
					// With other conditions, a set left out would widen the rule: skip it whole.
					if len(have) == 0 || (len(have) < len(m.Values) && len(r.Matchers) > 1) {
						continue rules
					}
					rule["rule_set"] = have
					needsResolve = needsResolve || m.Kind == "rule_set_ip"
				case "port":
					var ports []int
					for _, v := range m.Values {
						n, _ := strconv.Atoi(v)
						ports = append(ports, n)
					}
					rule["port"] = ports
				default:
					rule[m.Kind] = m.Values
				}
			}
			if r.Network != "" {
				rule["network"] = r.Network
			}
			if r.IPVersion != 0 {
				rule["ip_version"] = r.IPVersion
			}
			if needsResolve && !resolved {
				// Rules on addresses need the name resolved; do it only from here on, so
				// what the rules above decide never goes through the local DNS.
				routeRules = append(routeRules, map[string]any{"action": "resolve"})
				resolved = true
			}
			switch r.Target {
			case rules.TargetBlock:
				rule["action"] = "reject"
			default:
				rule["action"], rule["outbound"] = "route", outboundTag(r.Target)
			}
			if g := outboundTag(r.Target); isIPv4Group[g] {
				// A name sent to a node is looked up by the node, which goes out over
				// IPv6 where it can, whatever the route says. So the name is looked up
				// here first, IPv4 only, by a server reached through the same route, and
				// the node is given the address: it can only go out over IPv4. Without
				// an IPv4 answer the connection fails; it never falls back to IPv6.
				res := map[string]any{}
				for k, v := range rule {
					res[k] = v
				}
				delete(res, "outbound")
				res["action"], res["server"], res["strategy"] = "resolve", viaDNS(g), "ipv4_only"
				routeRules = append(routeRules, res)
			}
			routeRules = append(routeRules, rule)
		}
	}

	// DNS: names in China asked directly of a domestic server, the rest through the
	// proxy. The rule set that tells names in China is the one the rules use for it.
	cnSet := ""
	if path, ok := in.Sets[cnRuleSet]; ok {
		sets[cnRuleSet] = map[string]any{"type": "local", "tag": cnRuleSet, "format": "binary", "path": path}
		cnSet = cnRuleSet
	}
	dnsSection, resolver, dnsNotes := dnsConfig(in.DNS, len(nodes) > 0, cnSet, vias)
	notes = append(notes, dnsNotes...)

	var ruleSets []any
	for _, v := range sets {
		ruleSets = append(ruleSets, v)
	}
	route := map[string]any{"default_domain_resolver": resolver, "rules": routeRules, "final": final}
	if len(ruleSets) > 0 {
		route["rule_set"] = ruleSets
	}
	if findProcess {
		route["find_process"] = true
	}
	c := map[string]any{
		"log": map[string]any{"level": "info", "timestamp": true},
		"dns": dnsSection,
		"inbounds": append([]any{map[string]any{
			"type": "mixed", "tag": "mixed-in",
			"listen": "127.0.0.1", "listen_port": s.MixedPort,
		}}, checkIns...),
		"outbounds": outbounds,
		"route":     route,
		"experimental": map[string]any{
			"clash_api": map[string]any{
				"external_controller": fmt.Sprintf("127.0.0.1:%d", s.APIPort),
				"secret":              s.Secret,
			},
		},
	}
	cfg, err = json.MarshalIndent(c, "", "  ")
	return cfg, notes, err
}

func outboundTag(target string) string {
	switch target {
	case rules.TargetProxy:
		return TagProxy
	case rules.TargetAuto:
		return TagAuto
	case rules.TargetDirect:
		return TagDirect
	}
	return target // a route's group
}
