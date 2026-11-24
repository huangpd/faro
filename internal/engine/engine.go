// Package engine is everything Faro does that is not drawing: subscriptions,
// nodes, rules, the core process, the system proxy. It has no interface of its
// own; the window, the menu-bar menu and the control API are all users of it.
//
// An Engine is safe for use from any goroutine. State returns a snapshot to
// draw from; the methods that act return at once and do their slow work in the
// background, calling Options.Notify when something changed.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"faro/internal/clashapi"
	"faro/internal/core"
	"faro/internal/domains"
	"faro/internal/prefs"
	"faro/internal/presets"
	"faro/internal/rules"
	"faro/internal/ruleset"
	"faro/internal/singcfg"
	"faro/internal/sub"
	"faro/internal/sysproxy"
)

// The states the core can be in.
const (
	Stopped  = "stopped"
	Starting = "starting"
	Running  = "running"
)

// historyLen is how many one-second samples of traffic are kept.
const historyLen = 60

// Options configure New.
type Options struct {
	// ResourcesDir is where the app's bundled files are (the core, the rule sets).
	ResourcesDir string
	// Notify is called, from any goroutine and never with a lock held, after
	// the state changed.
	Notify func()
	// ProxyRunner runs networksetup; nil runs the real one.
	ProxyRunner sysproxy.Runner

	// The rule sets are kept current by themselves. These are for tests; zero
	// values give the real ones.
	FetchRuleSets    func(ctx context.Context, dir string, proxyPort int) (ruleset.Result, error)
	RuleSetsMaxAge   time.Duration // older than this is stale: a day
	RuleSetsFirst    time.Duration // the first look after the app starts: a little while
	RuleSetsInterval time.Duration // then a look this often: an hour
	RuleSetsRetry    time.Duration // after a failure, not again within this: an hour
	Now              func() time.Time
	// ProbeURL is what a node is tested against; empty is singcfg.ProbeURL. For tests.
	ProbeURL string
	// Probe times one node through the running core: ms, or an error. Nil asks the
	// core (clashapi Delay). A hook, so that the engine's bookkeeping of tests can be
	// tested apart from the core's.
	Probe func(ctx context.Context, tag string) (int, error)
}

// Node is a node as lists show it.
type Node struct {
	Tag, Kind string
}

// NodeGroup is the nodes of one country, in the order to show them: the
// regions in the order they are defined, the nodes in each in natural order
// (US-2 before US-10). Region is "" for the nodes whose names say no country.
type NodeGroup struct {
	Region string
	Label  string // "日本 · JP"; "其他" for none
	Nodes  []Node
}

// State is a snapshot of everything a view needs. Its maps and slices are
// copies; Plan is shared and must be treated as read-only.
type State struct {
	Status  string // Stopped, Starting or Running
	Version string // the core's, while running
	Err     string // the last thing that went wrong, until the next action
	Busy    string // what is being done in the background, "" if nothing
	Rate    clashapi.Traffic
	History []clashapi.Traffic // the last minute, oldest first, a sample a second
	Conns   []clashapi.Conn    // the open connections, while they are being watched
	Subs    []sub.Subscription
	Nodes   []Node
	// NodeGroups is Nodes by country, for listing. Notices (usage, expiry) are
	// not in it: they are not servers to pick.
	NodeGroups []NodeGroup
	// Notices are the entries a provider lists as nodes that are only messages.
	Notices    []string
	Choices    map[string]string // the node picked per route; "proxy" is the main group
	Plan       *rules.Plan       // the rules compiled against the nodes; nil if the text has errors
	PlanErrors []rules.Error     // why Plan is nil
	RulesText  string
	// Groups are the person's own sites by group, checked before RulesText. A
	// group in GroupErrors (by name) is not in force, and the value says why.
	Groups      []domains.Group
	GroupErrors map[string]string
	Delays      map[string]int    // ms by node tag; -1 failed; absent untested
	Testing     map[string]bool   // nodes being tested now
	SystemProxy bool              // whether the user wants the system proxy on
	LightMode   bool              // the window is not opened unless asked for
	MixedPort   int               // the local proxy's port, which does not change
	Notes       []string          // what the last config left out
	DNS         singcfg.DNSChoice // the DNS servers in use, by id
	// RuleSetsChecked is when the rule sets were last known to be current: the
	// last time they were fetched, or the date of the copy that shipped.
	RuleSetsChecked time.Time
	DataDir         string
}

// Engine runs Faro.
type Engine struct {
	opts  Options
	core  *core.Core
	store *sub.Store
	prefs *prefs.Prefs
	proxy *sysproxy.Manager

	setsDir string

	proxyMu       sync.Mutex // serializes applying the latest system-proxy preference
	mu            sync.Mutex
	status        string
	version       string
	err           string
	busy          string
	rate          clashapi.Traffic
	history       []clashapi.Traffic
	conns         []clashapi.Conn
	watching      int // how many views want the connections
	pollGen       int // which poller is current
	rulesText     string
	groupsList    []domains.Group
	groupErrs     map[string]string
	delays        map[string]int
	testing       map[string]bool
	subs          []sub.Subscription
	nodes         []Node
	groups        []NodeGroup
	notices       []string
	choices       map[string]string
	plan          *rules.Plan
	planErrs      []rules.Error
	autoTried     time.Time // the last automatic attempt at the rule sets
	autoTriedPort int       // the proxy it went through, 0 for none
	quit          chan struct{}
	gen           int                // which run of the core is current
	cancel        context.CancelFunc // ends the current run's traffic stream
}

// New opens Faro's data, puts back a system proxy that a crashed run left
// set, and returns the engine, stopped.
func New(opts Options) (*Engine, error) {
	if opts.FetchRuleSets == nil {
		opts.FetchRuleSets = ruleset.Update
	}
	if opts.RuleSetsMaxAge == 0 {
		opts.RuleSetsMaxAge = 24 * time.Hour
	}
	if opts.RuleSetsFirst == 0 {
		opts.RuleSetsFirst = 20 * time.Second
	}
	if opts.RuleSetsInterval == 0 {
		opts.RuleSetsInterval = time.Hour
	}
	if opts.RuleSetsRetry == 0 {
		opts.RuleSetsRetry = time.Hour
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	e := &Engine{opts: opts, status: Stopped, delays: map[string]int{}, testing: map[string]bool{}, choices: map[string]string{}, quit: make(chan struct{})}
	var err error
	if e.core, err = core.New(opts.ResourcesDir, e.changed); err != nil {
		return nil, err
	}
	dir := e.core.Dir()
	if e.store, err = sub.OpenStore(filepath.Join(dir, "subscriptions.json")); err != nil {
		return nil, err
	}
	if e.prefs, err = prefs.Load(filepath.Join(dir, "prefs.json")); err != nil {
		e.err = "settings: " + err.Error()
	}
	// The proxy's port is picked once and kept: tools are pointed at it.
	if e.prefs.MixedPort == 0 {
		if st, err := singcfg.NewSettings(0); err == nil {
			e.prefs.MixedPort = st.MixedPort
			_ = e.prefs.Save()
		}
	}
	run := opts.ProxyRunner
	if run == nil {
		run = sysproxy.ExecRunner
		if only := os.Getenv("FARO_PROXY_ONLY"); only != "" { // debugging: one service only
			run = sysproxy.OnlyRunner(only)
		}
	}
	e.proxy = sysproxy.New(run, filepath.Join(dir, "sysproxy-backup.json"))
	// A run that ended without putting the system proxy back leaves its backup.
	if err := e.proxy.Recover(); err != nil {
		e.err = "system proxy: " + err.Error()
	}

	e.setsDir = filepath.Join(dir, "rulesets")
	if err := ruleset.Install(opts.ResourcesDir, e.setsDir); err != nil {
		e.err = "rule sets: " + err.Error()
	}
	e.rulesText = presets.Base()
	if b, err := os.ReadFile(filepath.Join(dir, "rules.txt")); err == nil {
		e.rulesText = string(b)
	}
	if g, err := domains.Load(filepath.Join(dir, "domains.json")); err != nil {
		e.err = err.Error()
	} else {
		e.groupsList = g
	}
	e.reloadLocked()
	go e.keepRuleSetsCurrent()
	return e, nil
}

// changed tells the owner that the state changed.
func (e *Engine) changed() {
	if e.opts.Notify != nil {
		e.opts.Notify()
	}
}

// State returns a snapshot.
func (e *Engine) State() State {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := State{
		Status: e.status, Version: e.version, Err: e.err, Busy: e.busy, Rate: e.rate,
		History:    append([]clashapi.Traffic(nil), e.history...),
		Conns:      append([]clashapi.Conn(nil), e.conns...),
		Subs:       append([]sub.Subscription(nil), e.subs...),
		Nodes:      append([]Node(nil), e.nodes...),
		NodeGroups: copyGroups(e.groups),
		Notices:    append([]string(nil), e.notices...),
		Choices:    copyMap(e.choices),
		Plan:       e.plan,
		PlanErrors: append([]rules.Error(nil), e.planErrs...),
		RulesText:  e.rulesText, Groups: domains.Copy(e.groupsList), GroupErrors: copyMap(e.groupErrs), Delays: copyInts(e.delays), Testing: copyBools(e.testing),
		SystemProxy: e.prefs.SystemProxy, LightMode: e.prefs.LightMode, MixedPort: e.prefs.MixedPort,
		Notes: e.core.Notes(), DataDir: e.core.Dir(),
		DNS:             singcfg.DNSChoice{Domestic: e.prefs.DNSDomestic, Foreign: e.prefs.DNSForeign},
		RuleSetsChecked: e.ruleSetsCheckedLocked(),
	}
	return st
}

func copyGroups(g []NodeGroup) []NodeGroup {
	out := make([]NodeGroup, len(g))
	for i, x := range g {
		out[i] = NodeGroup{Region: x.Region, Label: x.Label, Nodes: append([]Node(nil), x.Nodes...)}
	}
	return out
}

func copyMap(m map[string]string) map[string]string {
	c := make(map[string]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func copyBools(m map[string]bool) map[string]bool {
	c := make(map[string]bool, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func copyInts(m map[string]int) map[string]int {
	c := make(map[string]int, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// Logs returns the core's recent output.
func (e *Engine) Logs() []string { return e.core.Logs() }

// ---- the state, from the store

// reloadLocked takes the store's contents in and compiles the rules against
// the nodes. The caller holds e.mu.
func (e *Engine) reloadLocked() {
	subs, choices := e.store.Snapshot()
	e.subs, e.choices = subs, choices
	outs := e.store.Outbounds()
	e.nodes = e.nodes[:0]
	tags := make([]string, 0, len(outs))
	for _, o := range outs {
		kind, _ := o.Out["type"].(string)
		e.nodes = append(e.nodes, Node{o.Tag, kind})
		tags = append(tags, o.Tag)
	}
	if sel := e.choices[singcfg.TagProxy]; sel != "" {
		found := false
		for _, n := range e.nodes {
			found = found || n.Tag == sel
		}
		if !found { // the node left the subscription: back to automatic, in view only
			delete(e.choices, singcfg.TagProxy)
		}
	}
	e.recompileLocked(tags)
}

func (e *Engine) recompileLocked(tags []string) {
	if tags == nil {
		tags = make([]string, len(e.nodes))
		for i, n := range e.nodes {
			tags[i] = n.Tag
		}
	}
	e.plan, e.planErrs, e.groupErrs = compileWithGroups(e.groupsList, e.rulesText, tags, e.choices)
	e.groupNodesLocked()
}

// groupNodesLocked sorts the nodes into countries, for listing. The plan knows
// the regions (the built-in ones and the text's own); with none, the built-in
// ones are used.
func (e *Engine) groupNodesLocked() {
	e.groups, e.notices = GroupNodes(e.nodes, e.plan)
}

// GroupNodes sorts nodes by country, in the order the regions are defined, and
// in each country in natural order. A node of no known country goes last, in a
// group of its own, and a notice is set apart.
func GroupNodes(nodes []Node, plan *rules.Plan) (groups []NodeGroup, notices []string) {
	by := map[string][]Node{}
	for _, n := range nodes {
		if plan.IsNotice(n.Tag) {
			notices = append(notices, n.Tag)
			continue
		}
		r := plan.RegionOf(n.Tag)
		by[r] = append(by[r], n)
	}
	add := func(id string) {
		ns := by[id]
		if len(ns) == 0 {
			return
		}
		sort.SliceStable(ns, func(i, j int) bool { return NaturalLess(ns[i].Tag, ns[j].Tag) })
		label := "其他"
		if id != "" {
			label = plan.RegionLabel(id)
		}
		groups = append(groups, NodeGroup{Region: id, Label: label, Nodes: ns})
	}
	for _, id := range plan.RegionOrder() {
		add(id)
	}
	add("")
	return groups, notices
}

// NaturalLess orders names as a person would: runs of digits by their number,
// so that US-2 comes before US-10, the rest by letter, ignoring case.
func NaturalLess(a, b string) bool {
	ra, rb := []rune(a), []rune(b)
	i, j := 0, 0
	for i < len(ra) && j < len(rb) {
		if isDigit(ra[i]) && isDigit(rb[j]) {
			si := i
			for i < len(ra) && isDigit(ra[i]) {
				i++
			}
			sj := j
			for j < len(rb) && isDigit(rb[j]) {
				j++
			}
			na, nb := strings.TrimLeft(string(ra[si:i]), "0"), strings.TrimLeft(string(rb[sj:j]), "0")
			if len(na) != len(nb) { // fewer digits, a smaller number
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			continue
		}
		ca, cb := unicode.ToLower(ra[i]), unicode.ToLower(rb[j])
		if ca != cb {
			return ca < cb
		}
		i++
		j++
	}
	if len(ra)-i != len(rb)-j {
		return len(ra)-i < len(rb)-j // the shorter first: TY-3 before TY-3-2
	}
	return a < b // equal but for case or leading zeros: stable
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

// work runs fn in the background, shows what it is doing, and loads what it
// changed: a running core restarts.
func (e *Engine) work(what string, fn func(ctx context.Context) error) {
	e.mu.Lock()
	if e.busy != "" {
		e.mu.Unlock()
		return
	}
	e.busy, e.err = what, ""
	e.mu.Unlock()
	e.changed()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		err := fn(ctx)
		e.mu.Lock()
		e.busy = ""
		if err != nil {
			e.err = err.Error()
		}
		e.reloadLocked()
		restart := err == nil && e.status == Running
		e.mu.Unlock()
		e.changed()
		if restart {
			e.Restart()
		}
	}()
}

// AddSubscription adds a subscription from a URL, or from a file another
// client downloaded.
func (e *Engine) AddSubscription(ref string) {
	e.work("Fetching subscription…", func(ctx context.Context) error { return e.store.Add(ctx, ref) })
}

// RefreshSubscription downloads a subscription again.
func (e *Engine) RefreshSubscription(id string) {
	e.work("Refreshing…", func(ctx context.Context) error { return e.store.Refresh(ctx, id) })
}

// RemoveSubscription deletes a subscription.
func (e *Engine) RemoveSubscription(id string) {
	e.work("Removing…", func(context.Context) error { return e.store.Remove(id) })
}

// ---- choosing

// Choose picks the node a route's group is on, and remembers it, so that it
// is the same node after a restart or a refresh of the subscription. route is
// "proxy" for the main group; tag "" is its automatic choice, or a fast
// route's fastest. A running core switches at once, and so do the routes that
// follow this one. A route whose gate is shut stays shut: the node is only
// kept for when it opens.
func (e *Engine) Choose(route, tag string) {
	e.mu.Lock()
	if tag == e.choices[route] {
		e.mu.Unlock()
		return
	}
	if tag == "" {
		delete(e.choices, route)
	} else {
		e.choices[route] = tag
	}
	e.recompileLocked(nil)
	type sel struct{ group, name string }
	var todo []sel
	if route == singcfg.TagProxy {
		name := tag
		if name == "" {
			name = singcfg.TagAuto
		}
		todo = []sel{{singcfg.TagProxy, name}}
	} else if e.plan != nil {
		for _, r := range e.plan.Routes {
			if r.Closed {
				continue
			}
			if r.ID == route || (r.Kind == "like" && r.Like == route) {
				todo = append(todo, sel{r.Group(), nodeOf(r)})
			}
		}
	}
	// A few hundred bytes: saved here, so that what is shown is what is kept.
	saveErr := e.store.SetChoice(route, tag)
	if saveErr != nil {
		e.err = saveErr.Error()
	}
	running := e.status == Running
	e.mu.Unlock()
	e.changed()
	if saveErr != nil || !running {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, s := range todo {
			if err := e.core.API().Select(ctx, s.group, s.name); err != nil {
				e.fail(err)
				return
			}
		}
	}()
}

// nodeOf is the choice a route's group should be on: its node, or the
// fastest, or the gate when it is shut.
func nodeOf(r rules.Route) string {
	switch {
	case r.Closed:
		return rules.GateTag
	case r.Chosen == "":
		return r.Group() + "-fast"
	}
	return r.Chosen
}

// SetGate shuts or opens a route's gate, at once in a running core, and keeps
// it so across restarts. Opening puts the group back on its node.
func (e *Engine) SetGate(route string, closed bool) {
	key := route + rules.GateSuffix
	val := ""
	e.mu.Lock()
	if closed {
		e.choices[key], val = "closed", "closed"
	} else {
		delete(e.choices, key)
	}
	e.recompileLocked(nil)
	var group, name string
	if e.plan != nil {
		for _, r := range e.plan.Routes {
			if r.ID == route {
				group, name = r.Group(), nodeOf(r)
			}
		}
	}
	err := e.store.SetChoice(key, val)
	if err != nil {
		e.err = err.Error()
	}
	running := e.status == Running
	e.mu.Unlock()
	e.changed()
	if err != nil || !running || group == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := e.core.API().Select(ctx, group, name); err != nil {
			e.fail(err)
		}
	}()
}

// fail records an error to show.
func (e *Engine) fail(err error) {
	if err == nil {
		return
	}
	e.mu.Lock()
	e.err = err.Error()
	e.mu.Unlock()
	e.changed()
}

// TestAll times every node, eight at a time. The core must be running.
func (e *Engine) TestAll() {
	e.mu.Lock()
	if e.status != Running {
		e.mu.Unlock()
		return
	}
	tags := make([]string, len(e.nodes))
	for i, n := range e.nodes {
		tags[i] = n.Tag
	}
	e.delays = map[string]int{}
	e.mu.Unlock()
	e.changed()
	api := e.core.API()
	go func() {
		var wg sync.WaitGroup
		sem := make(chan struct{}, 8)
		for _, tag := range tags {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				d, err := e.probe(ctx, api, tag)
				if err != nil || d <= 0 {
					d = -1
				}
				e.mu.Lock()
				e.delays[tag] = d
				e.mu.Unlock()
				e.changed()
			}()
		}
		wg.Wait()
	}()
}

// probe times one node: through the hook if there is one, else the core.
func (e *Engine) probe(ctx context.Context, api *clashapi.Client, tag string) (int, error) {
	if e.opts.Probe != nil {
		return e.opts.Probe(ctx, tag)
	}
	url := e.opts.ProbeURL
	if url == "" {
		url = singcfg.ProbeURL
	}
	return api.Delay(ctx, tag, url)
}

// TestNode times one node, now: whether it can carry a request out and how long
// that takes. It is what choosing a node does, so that you see at once whether
// the exit you picked works; clicking the node again tests it again. Nothing
// happens if the core is stopped (the node is not loaded then) or the node is
// being tested already.
func (e *Engine) TestNode(tag string) {
	e.mu.Lock()
	if e.status != Running || e.testing[tag] || tag == "" {
		e.mu.Unlock()
		return
	}
	known := false
	for _, n := range e.nodes {
		known = known || n.Tag == tag
	}
	if !known {
		e.mu.Unlock()
		return
	}
	e.testing[tag] = true
	delete(e.delays, tag) // the old answer is not the answer: show it is being asked again
	api := e.core.API()
	g := e.gen
	e.mu.Unlock()
	e.changed()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		d, err := e.probe(ctx, api, tag)
		if err != nil || d <= 0 {
			d = -1
		}
		e.mu.Lock()
		delete(e.testing, tag)
		if e.gen == g { // not for a core that has since gone: its answer is about nothing
			e.delays[tag] = d
		}
		e.mu.Unlock()
		e.changed()
	}()
}

// ---- rules

// ApplyRules checks the text and, when it is right, keeps it and restarts a
// running core to load it. The errors, with their lines, come back; nothing is
// kept while there are any.
func (e *Engine) ApplyRules(text string) []rules.Error {
	e.mu.Lock()
	tags := make([]string, len(e.nodes))
	for i, n := range e.nodes {
		tags[i] = n.Tag
	}
	if _, errs, _ := compileWithGroups(e.groupsList, text, tags, e.choices); errs != nil {
		e.mu.Unlock()
		return errs
	}
	if err := os.WriteFile(filepath.Join(e.core.Dir(), "rules.txt"), []byte(text), 0o600); err != nil {
		e.err = err.Error()
		e.mu.Unlock()
		e.changed()
		return nil
	}
	e.rulesText = text
	e.reloadLocked()
	running := e.status == Running
	e.mu.Unlock()
	e.changed()
	if running {
		e.Restart()
	}
	return nil
}

// UpdateRuleSets fetches the rule sets now, when asked. They are also kept
// current without asking (keepRuleSetsCurrent); this is for wanting it at once,
// and it says what went wrong.
func (e *Engine) UpdateRuleSets() {
	e.mu.Lock()
	if e.busy != "" {
		e.mu.Unlock()
		return
	}
	port := 0
	if e.status == Running {
		port = e.core.MixedPort()
	}
	e.busy, e.err = "Updating rule sets…", ""
	e.mu.Unlock()
	e.changed()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		res, err := e.opts.FetchRuleSets(ctx, e.setsDir, port)
		e.mu.Lock()
		e.busy = ""
		if err != nil {
			e.err = "rule sets: updated " + strconv.Itoa(res.Fetched) + ", " + err.Error()
		} else {
			e.markRuleSetsCheckedLocked()
		}
		e.mu.Unlock()
		e.changed()
	}()
}

// ---- keeping the rule sets current

// ruleSetsCheckedLocked is when the rule sets were last known to be current:
// the later of the last fetch and the newest file (the copy that shipped).
func (e *Engine) ruleSetsCheckedLocked() time.Time {
	t := ruleset.Newest(e.setsDir)
	if c := time.Unix(e.prefs.RuleSetsChecked, 0); e.prefs.RuleSetsChecked != 0 && c.After(t) {
		t = c
	}
	return t
}

func (e *Engine) markRuleSetsCheckedLocked() {
	e.prefs.RuleSetsChecked = e.opts.Now().Unix()
	_ = e.prefs.Save()
}

// keepRuleSetsCurrent looks, a little after the app starts and then every
// hour, whether the rule sets are more than a day old, and fetches them if so.
// The communities that maintain them publish about daily; this follows.
func (e *Engine) keepRuleSetsCurrent() {
	first := time.NewTimer(e.opts.RuleSetsFirst)
	defer first.Stop()
	tick := time.NewTicker(e.opts.RuleSetsInterval)
	defer tick.Stop()
	for {
		select {
		case <-e.quit:
			return
		case <-first.C:
		case <-tick.C:
		}
		e.maybeUpdateRuleSets()
	}
}

// maybeUpdateRuleSets fetches the rule sets if they are stale, quietly: it
// shows no error and no busy state, since nobody asked. A failure is not tried
// again for an hour, except that if the core has since started, the attempt is
// made again at once through it, because the sites may not be reachable
// directly. What is replaced is loaded by the core by itself; nothing restarts.
func (e *Engine) maybeUpdateRuleSets() {
	e.mu.Lock()
	now := e.opts.Now()
	port := 0
	if e.status == Running {
		port = e.core.MixedPort()
	}
	stale := now.Sub(e.ruleSetsCheckedLocked()) > e.opts.RuleSetsMaxAge
	// Not again within the retry gap, unless a better way (the core's proxy) has appeared.
	recent := !e.autoTried.IsZero() && now.Sub(e.autoTried) < e.opts.RuleSetsRetry
	better := port != 0 && e.autoTriedPort == 0
	if !stale || e.busy != "" || (recent && !better) {
		e.mu.Unlock()
		return
	}
	e.autoTried, e.autoTriedPort = now, port
	e.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	res, err := e.opts.FetchRuleSets(ctx, e.setsDir, port)
	if err != nil { // some may have come: they are in place. The rest, next time.
		return
	}
	e.mu.Lock()
	e.markRuleSetsCheckedLocked()
	e.mu.Unlock()
	_ = res
	e.changed()
}

// ---- the system proxy

// SetSystemProxy remembers whether macOS should use the local proxy. If the
// core is stopped (for example after a failure), enabling also retries it.
func (e *Engine) SetSystemProxy(on bool) {
	e.mu.Lock()
	e.prefs.SystemProxy = on
	e.err = ""
	saveErr := e.prefs.Save()
	e.mu.Unlock()
	if on {
		e.Start()
	}
	e.fail(saveErr)
	e.changed()
	go func() { e.fail(e.syncSystemProxy()) }()
}

// The switch and readiness callbacks can overlap. Read the latest preference
// only once we have the lock, so a stale callback cannot undo a newer toggle.
func (e *Engine) syncSystemProxy() error {
	e.proxyMu.Lock()
	defer e.proxyMu.Unlock()
	e.mu.Lock()
	running, want := e.status == Running, e.prefs.SystemProxy
	e.mu.Unlock()
	select {
	case <-e.quit:
		return nil
	default:
	}
	if !want {
		return e.proxy.Disable()
	}
	if running {
		return e.proxy.Enable("127.0.0.1", e.core.MixedPort())
	}
	return nil
}

// SetLightMode remembers whether Faro keeps to the menu bar. Nothing else
// changes: the core, the proxy and the rules go on as they were.
func (e *Engine) SetLightMode(on bool) {
	e.mu.Lock()
	e.prefs.LightMode = on
	if err := e.prefs.Save(); err != nil {
		e.err = err.Error()
	}
	e.mu.Unlock()
	e.changed()
}

// ---- DNS

// SetDNS chooses the DNS servers: a domestic one, asked directly for names in
// China, and a foreign one, asked through the proxy for all the rest. An id that
// is not in the list for its side is refused. A running core restarts to use
// them.
func (e *Engine) SetDNS(domestic, foreign string) error {
	has := func(side, id string) bool {
		for _, s := range singcfg.DNSOptions(side) {
			if s.ID == id {
				return true
			}
		}
		return false
	}
	if !has("domestic", domestic) || !has("foreign", foreign) {
		return fmt.Errorf("unknown DNS server: %q or %q", domestic, foreign)
	}
	e.mu.Lock()
	if e.prefs.DNSDomestic == domestic && e.prefs.DNSForeign == foreign {
		e.mu.Unlock()
		return nil
	}
	e.prefs.DNSDomestic, e.prefs.DNSForeign = domestic, foreign
	err := e.prefs.Save()
	if err != nil {
		e.err = err.Error()
	}
	running := e.status != Stopped
	e.mu.Unlock()
	e.changed()
	if err == nil && running {
		e.Restart()
	}
	return err
}

// ---- the port

// SetMixedPort changes the port of the local proxy, which tools are pointed at.
// It is checked first and the answer is the error: a port below 1024 needs
// privileges this app does not have, and one that something else holds would
// leave the core unable to start. The old port is kept until the new one is
// known to be good. A running core restarts on the new port, and the system
// proxy, which follows the core, follows it there.
func (e *Engine) SetMixedPort(port int) error {
	if port < 1024 || port > 65535 {
		return fmt.Errorf("端口要在 1024 到 65535 之间（小于 1024 的端口需要管理员权限）")
	}
	e.mu.Lock()
	if port == e.prefs.MixedPort {
		e.mu.Unlock()
		return nil
	}
	running := e.status != Stopped
	e.mu.Unlock()
	// The core's own port is the old one: only a different one is asked of the system.
	if err := singcfg.CheckPort(port); err != nil {
		return err
	}
	e.mu.Lock()
	e.prefs.MixedPort = port
	err := e.prefs.Save()
	if err != nil {
		e.err = err.Error()
	} else {
		e.err = ""
	}
	e.mu.Unlock()
	e.changed()
	if err != nil {
		return err
	}
	if running {
		e.Restart()
	}
	return nil
}

// ---- the core

// Start runs the core, if it is stopped.
func (e *Engine) Start() {
	e.mu.Lock()
	if e.status != Stopped {
		e.mu.Unlock()
		return
	}
	e.gen++
	g := e.gen
	e.status, e.err = Starting, ""
	e.mu.Unlock()
	e.changed()
	go e.run(g)
}

// Restart loads what changed: it stops the core and runs it again.
func (e *Engine) Restart() {
	e.mu.Lock()
	e.gen++
	g := e.gen
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	e.status, e.err = Starting, ""
	e.mu.Unlock()
	e.changed()
	go func() {
		e.fail(e.proxy.Disable()) // the new core has another port
		e.core.Stop()
		e.run(g)
	}()
}

// Stop stops the core, after putting the system proxy back.
func (e *Engine) Stop() {
	e.mu.Lock()
	e.gen++
	g := e.gen
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	e.mu.Unlock()
	go func() {
		e.fail(e.proxy.Disable()) // before the core goes: no dead proxy
		e.core.Stop()
		e.mu.Lock()
		if e.gen == g {
			e.status, e.rate = Stopped, clashapi.Traffic{}
			e.history, e.conns = nil, nil
		}
		e.mu.Unlock()
		e.changed()
	}()
}

// Close is for quitting: the system proxy is put back, then the core stops.
func (e *Engine) Close() {
	e.mu.Lock()
	select {
	case <-e.quit:
	default:
		close(e.quit)
	}
	e.gen++
	if e.cancel != nil {
		e.cancel()
	}
	e.mu.Unlock()
	e.proxyMu.Lock()
	_ = e.proxy.Disable()
	e.proxyMu.Unlock()
	e.core.Stop()
}

// current reports whether run g is still the one the engine wants.
func (e *Engine) current(g int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.gen == g
}

// stopped ends run g with a message, unless a newer run has taken over.
func (e *Engine) stopped(g int, msg string) {
	e.mu.Lock()
	if e.gen == g {
		e.status, e.err, e.rate = Stopped, msg, clashapi.Traffic{}
		e.history, e.conns = nil, nil
	}
	e.mu.Unlock()
	e.changed()
}

// run starts the core and follows it until it ends. It belongs to one
// generation: once a newer one exists it leaves the core and the state alone.
func (e *Engine) run(g int) {
	e.mu.Lock()
	if e.gen != g {
		e.mu.Unlock()
		return
	}
	text, port, groups := e.rulesText, e.prefs.MixedPort, domains.Copy(e.groupsList)
	e.mu.Unlock()
	nodes := e.store.Outbounds()
	choices := e.store.ChoicesCopy()
	tags := make([]string, len(nodes))
	for i, n := range nodes {
		tags[i] = n.Tag
	}
	plan, errs, _ := compileWithGroups(groups, text, tags, choices)
	if errs != nil { // the saved text was checked when saved; this is a file edited by hand
		e.stopped(g, "rules: "+errs[0].Error())
		return
	}
	e.mu.Lock()
	dns := singcfg.DNSChoice{Domestic: e.prefs.DNSDomestic, Foreign: e.prefs.DNSForeign}
	e.mu.Unlock()
	in := singcfg.Input{Nodes: nodes, Plan: plan, Choices: choices, Sets: ruleset.Paths(e.setsDir), DNS: dns}
	if err := e.core.Start(in, port); err != nil {
		e.stopped(g, err.Error())
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.mu.Lock()
	if e.gen != g { // superseded while starting: not ours to follow
		e.mu.Unlock()
		cancel()
		return
	}
	e.cancel = cancel
	e.mu.Unlock()

	wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
	v, err := e.core.WaitReady(wctx)
	wcancel()
	if err != nil {
		cancel()
		if !e.current(g) {
			return
		}
		e.core.Stop()
		e.stopped(g, err.Error()+lastLogLine(e.core.Logs())) // the reason is in the core's own words
		return
	}
	e.mu.Lock()
	if e.gen != g {
		e.mu.Unlock()
		return
	}
	e.status, e.version = Running, v.Version
	e.mu.Unlock()
	e.changed()
	go e.maybeUpdateRuleSets() // now there is a proxy to fetch through, if the direct way failed
	e.fail(e.syncSystemProxy())
	_ = e.core.API().StreamTraffic(ctx, func(t clashapi.Traffic) {
		e.mu.Lock()
		if e.gen == g {
			e.rate = t
			e.history = append(e.history, t)
			if n := len(e.history); n > historyLen {
				e.history = append(e.history[:0], e.history[n-historyLen:]...)
			}
		}
		e.mu.Unlock()
		e.changed()
	})
	// The stream ends on stop, or because the core died: tell which.
	if ctx.Err() != nil || !e.current(g) {
		return
	}
	e.fail(e.proxy.Disable()) // the core is gone: do not leave a dead proxy
	e.core.Stop()             // reaps it if only the stream broke
	msg := "sing-box stopped unexpectedly"
	if ce := e.core.Err(); ce != nil {
		msg += ": " + ce.Error()
	}
	e.stopped(g, msg)
}

// lastLogLine is ": <the last ERROR or FATAL line>" of the core's output, or "".
func lastLogLine(logs []string) string {
	for i := len(logs) - 1; i >= 0; i-- {
		if strings.Contains(logs[i], "FATAL") || strings.Contains(logs[i], "ERROR") {
			l := logs[i]
			if j := strings.Index(l, "FATAL"); j >= 0 {
				l = l[j:]
			} else if j := strings.Index(l, "ERROR"); j >= 0 {
				l = l[j:]
			}
			return ": " + strings.TrimSpace(l)
		}
	}
	return ""
}

// ---- connections

// WatchConnections says whether a view wants the list of open connections.
// The core is asked once a second while any does, and not otherwise: nobody
// pays for a list nobody looks at.
func (e *Engine) WatchConnections(on bool) {
	e.mu.Lock()
	if on {
		e.watching++
	} else if e.watching > 0 {
		e.watching--
	}
	start := e.watching > 0
	if start {
		e.pollGen++
	} else {
		e.pollGen++ // ends the poller
		e.conns = nil
	}
	g := e.pollGen
	e.mu.Unlock()
	if start {
		go e.pollConnections(g)
	}
}

func (e *Engine) pollConnections(g int) {
	for {
		e.mu.Lock()
		stale := e.pollGen != g || e.watching == 0
		running := e.status == Running
		e.mu.Unlock()
		if stale {
			return
		}
		if running {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			conns, err := e.core.API().Connections(ctx)
			cancel()
			if err == nil {
				e.mu.Lock()
				if e.pollGen == g {
					sort.SliceStable(conns, func(i, j int) bool { return conns[i].Start.After(conns[j].Start) })
					if len(conns) > 500 {
						conns = conns[:500]
					}
					e.conns = conns
				}
				e.mu.Unlock()
				e.changed()
			}
		}
		time.Sleep(time.Second)
	}
}

// CloseConnection ends one open connection.
func (e *Engine) CloseConnection(id string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		e.fail(e.core.API().CloseConnection(ctx, id))
	}()
}

// ---- the generated config, to read

// secretKeys are the fields of a node that identify a server or open it.
var secretKeys = map[string]bool{
	"server": true, "server_port": true, "uuid": true, "password": true, "public_key": true,
	"short_id": true, "username": true, "auth_str": true, "obfs": true, "server_name": true,
	"host": true, "path": true, "service_name": true, "secret": true, "external_controller": true,
	"path_regex": true,
}

// Preview returns the configuration the current rules and nodes make, to
// read: the routing, the groups, the rule sets. What identifies a server or
// opens it (addresses, ports, ids, passwords, keys, the controller's secret)
// is never in it: a node is shown by its tag and type alone.
func (e *Engine) Preview() (string, error) {
	e.mu.Lock()
	plan := e.plan
	choices := copyMap(e.choices)
	e.mu.Unlock()
	if plan == nil {
		return "", errors.New("the rules have errors")
	}
	nodes := e.store.Outbounds()
	in := singcfg.Input{Nodes: nodes, Plan: plan, Choices: choices, Sets: ruleset.Paths(e.setsDir),
		DNS: singcfg.DNSChoice{Domestic: e.prefs.DNSDomestic, Foreign: e.prefs.DNSForeign}}
	cfg, _, err := singcfg.Build(singcfg.Settings{MixedPort: e.prefs.MixedPort, APIPort: 1, Secret: "x"}, in)
	if err != nil {
		return "", err
	}
	var doc map[string]any
	if err := json.Unmarshal(cfg, &doc); err != nil {
		return "", err
	}
	delete(doc, "experimental") // the controller: its address and secret
	delete(doc, "inbounds")
	delete(doc, "log")
	delete(doc, "dns")
	scrub(doc)
	// The routing first: it is what the rules make, and what is read for. The
	// groups and nodes follow. (A Go map would print them in alphabetical order.)
	var b strings.Builder
	b.WriteString("{\n")
	keys := []string{"route", "outbounds"}
	for i, k := range keys {
		v, ok := doc[k]
		if !ok {
			continue
		}
		body, err := json.MarshalIndent(v, "  ", "  ")
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "  %q: %s", k, body)
		if i < len(keys)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("}")
	return b.String(), nil
}

// scrub empties the secret fields of node outbounds in place, keeping the
// groups (selector, urltest), which hold only names.
func scrub(doc map[string]any) {
	outs, _ := doc["outbounds"].([]any)
	for i, o := range outs {
		m, ok := o.(map[string]any)
		if !ok {
			continue
		}
		switch m["type"] {
		case "selector", "urltest", "direct", "block":
			continue
		}
		outs[i] = map[string]any{"type": m["type"], "tag": m["tag"]} // a node: its name and kind
	}
	if route, ok := doc["route"].(map[string]any); ok {
		if sets, ok := route["rule_set"].([]any); ok {
			for _, s := range sets {
				if m, ok := s.(map[string]any); ok {
					delete(m, "path") // the file's place on this disk
				}
			}
		}
	}
}

// compileWithGroups compiles the person's text with their groups of sites in
// front of it. A group that cannot be in force (its target is a route that is
// gone) is left out, and says why in the third result, by its name: one group's
// trouble does not stop the rest, or the rules text.
func compileWithGroups(groups []domains.Group, text string, tags []string, choices map[string]string) (*rules.Plan, []rules.Error, map[string]string) {
	var bad map[string]string
	active := groups
	for {
		head, owners := domains.Head(active)
		plan, errs := rules.CompileWith(head, text, tags, choices)
		off := strings.Count(head, "\n")
		var mine []rules.Error
		drop := map[int]bool{}
		for _, er := range errs {
			if er.Line > 0 {
				mine = append(mine, er)
				continue
			}
			if i := er.Line + off - 1; i >= 0 && i < len(owners) {
				if bad == nil {
					bad = map[string]string{}
				}
				bad[active[owners[i]].Name] = er.Msg
				drop[owners[i]] = true
			}
		}
		if len(drop) == 0 {
			return plan, mine, bad
		}
		var keep []domains.Group
		for i, g := range active {
			if !drop[i] {
				keep = append(keep, g)
			}
		}
		active = keep
	}
}

// Targets that are not routes.
var plainTargets = map[string]bool{rules.TargetProxy: true, rules.TargetAuto: true, "direct": true, rules.TargetBlock: true}

// ErrGroup is a reason a change to the groups was refused.
type ErrGroup string

func (e ErrGroup) Error() string { return string(e) }

// editGroups changes the groups, keeps them, and has a running core load them.
// change gets a copy to edit and says what to refuse.
func (e *Engine) editGroups(change func(gs []domains.Group, routeIDs map[string]bool) ([]domains.Group, error)) error {
	e.mu.Lock()
	routeIDs := map[string]bool{}
	if e.plan != nil {
		for _, r := range e.plan.Routes {
			routeIDs[r.ID] = true
		}
	}
	next, err := change(domains.Copy(e.groupsList), routeIDs)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	if err := domains.Save(filepath.Join(e.core.Dir(), "domains.json"), next); err != nil {
		e.mu.Unlock()
		return err
	}
	e.groupsList = next
	e.reloadLocked()
	running := e.status == Running
	e.mu.Unlock()
	e.changed()
	if running {
		e.Restart()
	}
	return nil
}

func findGroup(gs []domains.Group, name string) int {
	for i, g := range gs {
		if g.Name == name {
			return i
		}
	}
	return -1
}

func checkTarget(t string, routeIDs map[string]bool) error {
	if plainTargets[t] || routeIDs[t] {
		return nil
	}
	return ErrGroup(fmt.Sprintf("没有叫 %s 的出口", t))
}

// place puts sites in a group, and takes them out of any other: a site is in one group.
func place(gs []domains.Group, into int, sites []string) {
	for _, d := range sites {
		for i := range gs {
			if i == into {
				continue
			}
			kept := gs[i].Domains[:0]
			for _, x := range gs[i].Domains {
				if x != d {
					kept = append(kept, x)
				}
			}
			gs[i].Domains = kept
		}
	}
	have := map[string]bool{}
	for _, x := range gs[into].Domains {
		have[x] = true
	}
	for _, d := range sites {
		if !have[d] {
			have[d] = true
			gs[into].Domains = append(gs[into].Domains, d)
		}
	}
}

// NewGroup makes a group, with the sites in text (which may be empty).
func (e *Engine) NewGroup(name, target, text string) error {
	name = strings.TrimSpace(name)
	sites, bad := domains.Parse(text)
	if len(bad) > 0 {
		return ErrGroup("不是域名：" + strings.Join(bad, " "))
	}
	return e.editGroups(func(gs []domains.Group, ids map[string]bool) ([]domains.Group, error) {
		switch {
		case name == "" || len([]rune(name)) > 30:
			return nil, ErrGroup("请给分组起个名字，不超过 30 个字")
		case findGroup(gs, name) >= 0:
			return nil, ErrGroup("已经有叫 " + name + " 的分组")
		}
		if err := checkTarget(target, ids); err != nil {
			return nil, err
		}
		gs = append(gs, domains.Group{Name: name, Target: target})
		place(gs, len(gs)-1, sites)
		return gs, nil
	})
}

// DeleteGroup removes a group and its sites.
func (e *Engine) DeleteGroup(name string) error {
	return e.editGroups(func(gs []domains.Group, _ map[string]bool) ([]domains.Group, error) {
		i := findGroup(gs, name)
		if i < 0 {
			return nil, ErrGroup("没有叫 " + name + " 的分组")
		}
		return append(gs[:i], gs[i+1:]...), nil
	})
}

// SetGroupTarget moves a whole group to another place.
func (e *Engine) SetGroupTarget(name, target string) error {
	return e.editGroups(func(gs []domains.Group, ids map[string]bool) ([]domains.Group, error) {
		i := findGroup(gs, name)
		if i < 0 {
			return nil, ErrGroup("没有叫 " + name + " 的分组")
		}
		if err := checkTarget(target, ids); err != nil {
			return nil, err
		}
		gs[i].Target = target
		return gs, nil
	})
}

// MoveGroup moves a group up (negative) or down in the order, which is the
// order they are checked in.
func (e *Engine) MoveGroup(name string, delta int) error {
	return e.editGroups(func(gs []domains.Group, _ map[string]bool) ([]domains.Group, error) {
		i := findGroup(gs, name)
		j := i + delta
		if i < 0 || j < 0 || j >= len(gs) {
			return gs, nil
		}
		gs[i], gs[j] = gs[j], gs[i]
		return gs, nil
	})
}

// SetGroupDomains makes the sites of a group those in the list (as the field of
// tokens holds them). What is not a site is refused, and says which.
func (e *Engine) SetGroupDomains(name string, tokens []string) error {
	sites, bad := domains.Parse(strings.Join(tokens, " "))
	if len(bad) > 0 {
		return ErrGroup("不是域名：" + strings.Join(bad, " "))
	}
	return e.editGroups(func(gs []domains.Group, _ map[string]bool) ([]domains.Group, error) {
		i := findGroup(gs, name)
		if i < 0 {
			return nil, ErrGroup("没有叫 " + name + " 的分组")
		}
		gs[i].Domains = nil
		place(gs, i, sites)
		return gs, nil
	})
}

// AddDomains adds the sites in text to a group, taking them from any other.
func (e *Engine) AddDomains(name, text string) error {
	sites, bad := domains.Parse(text)
	if len(bad) > 0 {
		return ErrGroup("不是域名：" + strings.Join(bad, " "))
	}
	return e.editGroups(func(gs []domains.Group, _ map[string]bool) ([]domains.Group, error) {
		i := findGroup(gs, name)
		if i < 0 {
			return nil, ErrGroup("没有叫 " + name + " 的分组")
		}
		place(gs, i, sites)
		return gs, nil
	})
}
