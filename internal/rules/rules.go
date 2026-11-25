// Package rules reads the user's routing text and turns it into a plan:
// which node groups the config needs, and which rules send what to them.
//
// The text is one statement a line, '#' starts a comment:
//
//	route work region UK gate ipv4     a group of the UK nodes, held on one node,
//	                                   with a gate that can cut it, dialling IPv4
//	route eu region DE fast            the same, on the fastest of them
//	route work   regex "(?i)^corp"     nodes whose name matches
//	work domain:example.com            send this to the route
//	block udp domain:example.com       a flag and a matcher; UDP only
//	work process:"Some Helper"         what a program on this Mac sends
//	region EE name:estonia code:EE     a region of your own, or one replaced
//	notice "(?i)expires"               names that are notices, not servers
//	direct set:geosite-cn              direct, proxy, block and auto are built in
//
// A rule has a target, then flags (udp, tcp, ipv4, ipv6) and matchers
// (kind:value) in any number; a rule with several matchers needs all of
// them. A region route is strict: with no node in the region, what is sent
// to it is rejected, never sent out another country's node.
package rules

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The built-in targets, and the prefix of the groups made for routes.
const (
	TargetProxy  = "proxy"
	TargetAuto   = "auto"
	TargetDirect = "direct"
	TargetBlock  = "block"

	GroupPrefix = "route-"

	// GateTag is the choice that cuts a gated route: its group may be put on
	// it, and nothing then goes out.
	GateTag = "block"
	// GateSuffix is added to a route's id to keep whether its gate is closed
	// among the user's choices.
	GateSuffix = "#gate"
)

// Route is a named group of nodes.
type Route struct {
	ID   string
	Kind string // "region" or "regex"
	Arg  string // the region id, or the regular expression
	Fast bool   // follow the fastest node, rather than hold one
	Gate bool   // the group has a way to cut it: GateTag
	IPv4 bool   // its traffic leaves its nodes over IPv4 only
	// NoUDP and NoIPv6 block what is sent to the route over UDP, or to an IPv6
	// address: every rule that sends to it is repeated, as a block, above all
	// the others, wherever it is written.
	NoUDP, NoIPv6 bool
	// Exit is the addresses the route may leave from, as address ranges: the
	// address the world sees is checked, and a route that leaves from another
	// is cut. Empty is no check.
	Exit []string
	// CheckEvery is how often the exit is checked, 0 for DefaultCheckEvery;
	// Hours the part of the day it is checked on that timer ("09:00-23:30",
	// which may cross midnight), "" for all day. Outside those hours the route
	// keeps what its last check said. Both need Exit.
	CheckEvery time.Duration
	Hours      string
	Line       int
	Nodes      []string
	// Chosen is the node the group is on, "" for the fastest of a Fast
	// route. Warn says why not the one the user picked, when that is gone.
	Chosen string
	Warn   string
	// Closed is set when the gate is shut: the group is on GateTag.
	Closed bool
}

// Group is the tag of the selector the route becomes.
func (r Route) Group() string { return GroupPrefix + r.ID }

// Matcher is one condition of a rule.
type Matcher struct {
	Kind   string // domain_suffix, domain, domain_keyword, domain_regex, ip_cidr, rule_set, rule_set_ip, process_name, process_path_regex or port
	Values []string
}

// Rule sends what it matches to Target: a built-in, or a route's group. All
// its matchers, and its flags, must hold.
type Rule struct {
	Target    string // an outbound tag, or TargetBlock
	Network   string // "udp", "tcp", or "" for either
	IPVersion int    // 4, 6, or 0
	Matchers  []Matcher
	Line      int
	Route     string // the route it was written for, when it blocks because that route has no node
	Guard     string // the route whose noudp or noipv6 made it
}

// Plan is what the text says, with the nodes taken into account.
type Plan struct {
	Routes   []Route
	Rules    []Rule
	Warnings []string

	defs *defs // regions and notices, as the text and the built-in data define them
}

// UsesProcess reports whether a rule looks at the program a connection
// comes from, which the core must then find out.
func (p *Plan) UsesProcess() bool {
	for _, r := range p.Rules {
		for _, m := range r.Matchers {
			if m.Kind == "process_name" || m.Kind == "process_path_regex" {
				return true
			}
		}
	}
	return false
}

// Error is a mistake in the text, with its line.
type Error struct {
	Line int
	Msg  string
}

func (e Error) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Msg) }

var idRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,23}$`)

var matcherKinds = map[string]string{
	"domain": "domain_suffix", "full": "domain", "keyword": "domain_keyword",
	"regex": "domain_regex", "cidr": "ip_cidr", "set": "rule_set",
	"process": "process_name", "path": "process_path_regex", "port": "port",
}

const kindList = "domain, full, keyword, regex, cidr, set, process, path, port"

// fields splits a line at spaces, except inside double quotes, which are
// dropped: process:"Some Helper (GPU)" is one field.
func fields(line string) []string {
	var out []string
	var cur strings.Builder
	in, started := false, false
	for _, r := range line {
		switch {
		case r == '"':
			in, started = !in, true
		case (r == ' ' || r == '\t' || r == '\r') && !in:
			if started {
				out = append(out, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	if started {
		out = append(out, cur.String())
	}
	return out
}

// stripComment drops a '#' comment: one at the start of the line or after a
// space. A '#' inside a word (a regular expression's, a URL's) stays.
func stripComment(line string) string {
	in := false
	for i, r := range line {
		switch {
		case r == '"':
			in = !in
		case r == '#' && !in && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
			return line[:i]
		}
	}
	return line
}

// CompileWith is Compile for a text that has other rules before it (head), as
// the person's groups of sites. Lines are counted in the text, not in head: a
// line of head is at 0 or less, so that the person's own lines keep their numbers.
func CompileWith(head, text string, tags []string, choices map[string]string) (*Plan, []Error) {
	if head == "" {
		return Compile(text, tags, choices)
	}
	if !strings.HasSuffix(head, "\n") {
		head += "\n"
	}
	off := strings.Count(head, "\n")
	p, errs := Compile(head+text, tags, choices)
	for i := range errs {
		errs[i].Line -= off
	}
	if p != nil {
		for i := range p.Rules {
			p.Rules[i].Line -= off
		}
		for i := range p.Routes {
			p.Routes[i].Line -= off
		}
	}
	return p, errs
}

// Compile reads the text against the node tags. choices holds the node the
// user picked for each route, by route id, and which gates are shut.
func Compile(text string, tags []string, choices map[string]string) (*Plan, []Error) {
	d := builtin.clone()
	p := &Plan{defs: d}
	var errs []Error
	routes := map[string]*Route{}
	var order []string
	// A paragraph is lines with no blank line between them. Rules of different
	// paragraphs are never merged into one, so that each keeps whose it is:
	// a group, or a section of the text.
	type pending struct {
		line, para int
		rule       Rule
	}
	var lines []pending
	para := 0

	for i, raw := range strings.Split(text, "\n") {
		n := i + 1
		f := fields(stripComment(raw))
		if len(f) == 0 {
			if strings.TrimSpace(raw) == "" {
				para++
			}
			continue
		}
		if handled, err := d.apply(f); handled {
			if err != "" {
				errs = append(errs, Error{n, err})
			}
			continue
		}
		if f[0] == "route" {
			r, err := parseRoute(f[1:], routes, d)
			if err != "" {
				errs = append(errs, Error{n, err})
				continue
			}
			r.Line = n
			routes[r.ID] = r
			order = append(order, r.ID)
			continue
		}
		rule, err := parseRule(f, n)
		if err != "" {
			errs = append(errs, Error{n, err})
			continue
		}
		lines = append(lines, pending{n, para, rule})
	}

	// The routes, with their nodes.
	sort.Strings(order)
	for _, id := range order {
		r := routes[id]
		r.Nodes = match(r, tags, d)
		r.Chosen, r.Warn = pick(r, choices[id])
	}
	for _, id := range order {
		r := routes[id]
		r.Closed = r.Gate && choices[id+GateSuffix] == "closed"
		switch {
		case len(r.Nodes) == 0:
			p.Warnings = append(p.Warnings, fmt.Sprintf("route %s: no node matches %s, so what is sent to it is blocked", id, r.describe()))
		case r.Warn != "":
			p.Warnings = append(p.Warnings, fmt.Sprintf("route %s: %s", id, r.Warn))
		}
		if r.Closed {
			p.Warnings = append(p.Warnings, fmt.Sprintf("route %s: gate is closed, so what is sent to it is blocked", id))
		}
		p.Routes = append(p.Routes, *r)
	}

	// The rules; neighbours that differ only in one matcher's values share a rule.
	lastPara := -1
	for _, l := range lines {
		rule := l.rule
		switch rule.Target {
		case TargetProxy, TargetAuto, TargetDirect, TargetBlock:
		default:
			r, ok := routes[rule.Target]
			if !ok {
				errs = append(errs, Error{l.line, fmt.Sprintf("unknown route %q: define it with a route line", rule.Target)})
				continue
			}
			if len(r.Nodes) == 0 {
				rule.Route = r.ID
				rule.Target = TargetBlock // strict: no node in the region, no way out
			} else {
				rule.Target = r.Group()
			}
		}
		samePara := l.para == lastPara
		lastPara = l.para
		if n := len(p.Rules); n > 0 && samePara && mergeable(p.Rules[n-1], rule) {
			p.Rules[n-1].Matchers[0].Values = append(p.Rules[n-1].Matchers[0].Values, rule.Matchers[0].Values...)
			continue
		}
		p.Rules = append(p.Rules, rule)
	}
	p.Rules = append(guards(p.Rules, routes), p.Rules...)
	if len(errs) > 0 {
		sort.SliceStable(errs, func(i, j int) bool { return errs[i].Line < errs[j].Line })
		return nil, errs
	}
	return p, nil
}

// guards are the blocks that a route's noudp and noipv6 make: each rule that
// sends to the route, again, as a block over UDP or to IPv6 addresses. They go
// above all the rules, so that no rule written above can let such traffic out.
func guards(rs []Rule, routes map[string]*Route) []Rule {
	var out []Rule
	add := func(g Rule) {
		if n := len(out); n > 0 && mergeable(out[n-1], g) {
			out[n-1].Matchers[0].Values = append(out[n-1].Matchers[0].Values, g.Matchers[0].Values...)
			return
		}
		out = append(out, g)
	}
	for _, r := range rs {
		id := r.Route
		if t, ok := strings.CutPrefix(r.Target, GroupPrefix); ok {
			id = t
		}
		rt := routes[id]
		if rt == nil {
			continue
		}
		copyOf := func() Rule {
			g := Rule{Target: TargetBlock, Network: r.Network, IPVersion: r.IPVersion, Line: r.Line, Guard: id}
			for _, m := range r.Matchers {
				g.Matchers = append(g.Matchers, Matcher{m.Kind, append([]string(nil), m.Values...)})
			}
			return g
		}
		if rt.NoUDP && r.Network != "tcp" {
			g := copyOf()
			g.Network = "udp"
			add(g)
		}
		if rt.NoIPv6 && r.IPVersion != 4 {
			g := copyOf()
			g.IPVersion = 6
			add(g)
		}
	}
	return out
}

func mergeable(a, b Rule) bool {
	return a.Target == b.Target && a.Route == b.Route && a.Guard == b.Guard && a.Network == b.Network && a.IPVersion == b.IPVersion &&
		len(a.Matchers) == 1 && len(b.Matchers) == 1 && a.Matchers[0].Kind == b.Matchers[0].Kind
}

func (r Route) describe() string { return r.Arg }

func parseRule(f []string, line int) (Rule, string) {
	r := Rule{Target: f[0], Line: line}
	for _, tok := range f[1:] {
		switch tok {
		case "udp", "tcp":
			r.Network = tok
			continue
		case "ipv4":
			r.IPVersion = 4
			continue
		case "ipv6":
			r.IPVersion = 6
			continue
		}
		kind, val, ok := strings.Cut(tok, ":")
		sk, known := matcherKinds[kind]
		if !ok || !known || val == "" {
			return r, fmt.Sprintf("%q: expected a flag (udp, tcp, ipv4, ipv6) or <kind>:<value>, kind being one of %s", tok, kindList)
		}
		switch kind {
		case "regex", "path":
			if _, err := regexp.Compile(val); err != nil {
				return r, "bad regular expression: " + err.Error()
			}
		case "port":
			if n, err := strconv.Atoi(val); err != nil || n < 1 || n > 65535 {
				return r, fmt.Sprintf("bad port %q", val)
			}
		case "cidr":
			if !strings.Contains(val, "/") && !strings.Contains(val, ".") && !strings.Contains(val, ":") {
				return r, fmt.Sprintf("bad address range %q", val)
			}
		case "set":
			if strings.HasPrefix(val, "geoip-") {
				sk = "rule_set_ip" // matches on addresses: the name is resolved first
			}
		}
		// The same kind again means "or": process:a process:b is either program.
		merged := false
		for i := range r.Matchers {
			if r.Matchers[i].Kind == sk {
				r.Matchers[i].Values = append(r.Matchers[i].Values, val)
				merged = true
			}
		}
		if !merged {
			r.Matchers = append(r.Matchers, Matcher{sk, []string{val}})
		}
	}
	if len(r.Matchers) == 0 {
		return r, "expected: <route> [flags] <kind>:<value>"
	}
	kinds := map[string]bool{}
	for _, m := range r.Matchers {
		kinds[m.Kind] = true
	}
	if kinds["rule_set"] && kinds["rule_set_ip"] {
		return r, "a rule cannot take both a geoip- set and another set: write two rules"
	}
	return r, ""
}

func parseRoute(f []string, have map[string]*Route, d *defs) (*Route, string) {
	const usage = "expected: route <id> region <XX> [fast] [gate] [ipv4] [noudp] [noipv6] [exit:<range>]  |  route <id> regex <expression> [...]"
	if len(f) < 3 {
		return nil, usage
	}
	id := f[0]
	switch {
	case !idRe.MatchString(id):
		return nil, fmt.Sprintf("route id %q: use lower-case letters, digits and '-', starting with a letter", id)
	case id == TargetProxy || id == TargetAuto || id == TargetDirect || id == TargetBlock:
		return nil, fmt.Sprintf("%q is built in", id)
	case have[id] != nil:
		return nil, fmt.Sprintf("route %q is defined twice", id)
	}
	r := &Route{ID: id, Kind: f[1]}
	switch f[1] {
	case "region":
		if d.regions[strings.ToUpper(f[2])] == nil {
			return nil, fmt.Sprintf("unknown region %q; known: %s (define one with: region <ID> name:<text> ...)", f[2], strings.Join(d.regionIDs(), " "))
		}
		r.Arg = strings.ToUpper(f[2])
	case "regex":
		r.Arg = f[2]
		if _, err := regexp.Compile(r.Arg); err != nil {
			return nil, "bad regular expression: " + err.Error()
		}
	case "like":
		return nil, "'like' is gone: give the route its own region or regex, the same as the other route's"
	default:
		return nil, fmt.Sprintf("route kind %q: use region or regex", f[1])
	}
	for _, mod := range f[3:] {
		switch mod {
		case "fast":
			r.Fast = true
		case "gate":
			r.Gate = true
		case "ipv4":
			r.IPv4 = true
		case "noudp":
			r.NoUDP = true
		case "noipv6":
			r.NoIPv6 = true
		default:
			if v, ok := strings.CutPrefix(mod, "exit:"); ok {
				p, err := exitRange(v)
				if err != nil {
					return nil, fmt.Sprintf("exit:%s: %v", v, err)
				}
				r.Exit = append(r.Exit, p)
				r.Gate = true // a route that is checked is cut by the check: it has a gate
				continue
			}
			if v, ok := strings.CutPrefix(mod, "check:"); ok {
				d, err := time.ParseDuration(v)
				if err != nil || d < MinCheckEvery {
					return nil, fmt.Sprintf("check:%s: expected a time of at least 10s, as 30s, 2m or 1h", v)
				}
				r.CheckEvery = d
				continue
			}
			if v, ok := strings.CutPrefix(mod, "hours:"); ok {
				if _, _, ok := parseHours(v); !ok {
					return nil, fmt.Sprintf("hours:%s: expected a part of the day, as 09:00-23:30", v)
				}
				r.Hours = v
				continue
			}
			return nil, fmt.Sprintf("unknown option %q; options: fast, gate, ipv4, noudp, noipv6, exit:<address range>, check:<time>, hours:<from>-<to>", mod)
		}
	}
	if (r.CheckEvery != 0 || r.Hours != "") && len(r.Exit) == 0 {
		return nil, "check: and hours: say when the exit is checked: they need exit:<range>"
	}
	return r, ""
}

// DefaultCheckEvery is how often an exit is checked when check: is not given,
// and MinCheckEvery the least that may be given.
const (
	DefaultCheckEvery = 2 * time.Minute
	MinCheckEvery     = 10 * time.Second
)

// Every is how often the route's exit is checked.
func (r Route) Every() time.Duration {
	if r.CheckEvery == 0 {
		return DefaultCheckEvery
	}
	return r.CheckEvery
}

// InHours says whether t is in the part of the day the exit is checked on its timer.
func (r Route) InHours(t time.Time) bool {
	from, to, ok := parseHours(r.Hours)
	if !ok {
		return true
	}
	m := t.Hour()*60 + t.Minute()
	if from <= to {
		return m >= from && m < to
	}
	return m >= from || m < to // across midnight
}

// parseHours reads "HH:MM-HH:MM" as minutes of the day; the two must differ.
func parseHours(s string) (from, to int, ok bool) {
	a, b, found := strings.Cut(s, "-")
	if !found {
		return 0, 0, false
	}
	clock := func(v string) (int, bool) {
		if v == "24:00" {
			return 24 * 60, true
		}
		t, err := time.Parse("15:04", v)
		if err != nil {
			return 0, false
		}
		return t.Hour()*60 + t.Minute(), true
	}
	from, ok1 := clock(a)
	to, ok2 := clock(b)
	return from, to, ok1 && ok2 && from != to
}

// exitRange reads an address range, "198.51.100.0/24", or an address alone.
func exitRange(s string) (string, error) {
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked().String(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return "", errors.New("expected an address range, as 198.51.100.0/24, or an address")
	}
	return netip.PrefixFrom(a, a.BitLen()).String(), nil
}

// ExitAllows says whether the route may leave from ip: yes when it has no exit to
// keep to, or ip is in one of its ranges.
func (r Route) ExitAllows(ip string) bool {
	if len(r.Exit) == 0 {
		return true
	}
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	for _, s := range r.Exit {
		if p, err := netip.ParsePrefix(s); err == nil && p.Contains(a.Unmap()) {
			return true
		}
	}
	return false
}

// match returns the route's nodes, in a stable order.
func match(r *Route, tags []string, d *defs) []string {
	var re *regexp.Regexp
	if r.Kind == "regex" {
		re = regexp.MustCompile(r.Arg)
	}
	var out []string
	for _, t := range tags {
		if d.isNotice(t) {
			continue
		}
		if (r.Kind == "region" && d.inRegion(t, r.Arg)) || (re != nil && re.MatchString(t)) {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// pick is the node the group is on: the one the user chose, if it is still
// a node of the route; if it is gone, the first, with a warning so that the
// change of exit is seen.
func pick(r *Route, want string) (chosen, warn string) {
	if len(r.Nodes) == 0 {
		return "", ""
	}
	if want == "" {
		if r.Fast {
			return "", "" // the fastest of them
		}
		return r.Nodes[0], ""
	}
	for _, n := range r.Nodes {
		if n == want {
			return want, ""
		}
	}
	return r.Nodes[0], fmt.Sprintf("the node you picked, %q, is gone; now on %q", want, r.Nodes[0])
}
