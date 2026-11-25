package core

import (
	"bufio"
	"context"
	"faro/internal/fixtures"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"faro/internal/domains"
	"faro/internal/rules"
	"faro/internal/ruleset"
	"faro/internal/singcfg"
)

// fakeNode is a SOCKS5 server standing in for a proxy node. It records the
// host each connection asks for, which is how the tests see where the core
// sent it.
type fakeNode struct {
	port int
	mu   sync.Mutex
	seen []string
}

func newFakeNode(t *testing.T) *fakeNode {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	n := &fakeNode{port: l.Addr().(*net.TCPAddr).Port}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go n.serve(c)
		}
	}()
	return n
}

func (n *fakeNode) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return
	}
	io.CopyN(io.Discard, r, int64(hdr[1])) // the methods offered
	c.Write([]byte{5, 0})                  // no authentication
	req := make([]byte, 4)
	if _, err := io.ReadFull(r, req); err != nil {
		return
	}
	var host string
	switch req[3] {
	case 3:
		l, _ := r.ReadByte()
		b := make([]byte, int(l))
		io.ReadFull(r, b)
		host = string(b)
	case 1:
		b := make([]byte, 4)
		io.ReadFull(r, b)
		host = net.IP(b).String()
	case 4:
		b := make([]byte, 16)
		io.ReadFull(r, b)
		host = net.IP(b).String()
	}
	io.CopyN(io.Discard, r, 2) // the port
	n.mu.Lock()
	n.seen = append(n.seen, host)
	n.mu.Unlock()
	c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}) // connected; then nothing flows
}

func (n *fakeNode) saw(host string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, h := range n.seen {
		if h == host {
			return true
		}
	}
	return false
}

// routingRig runs the real core with the given routing text over fake nodes.
type routingRig struct {
	t     *testing.T
	core  *Core
	nodes map[string]*fakeNode // by tag
}

func newRig(t *testing.T, text string, tags []string, choices map[string]string) *routingRig {
	return newRigWith(t, text, tags, choices, false)
}

// newRigWith is newRig; keepIPv4 keeps the routes' ipv4, which looks every name
// sent to them up through the route first. A fake node cannot answer that lookup,
// so the tests of which node a name goes to leave it out; it has tests of its own.
func newRigWith(t *testing.T, text string, tags []string, choices map[string]string, keepIPv4 bool) *routingRig {
	bin, _ := filepath.Abs("../../bin/sing-box")
	if _, err := os.Stat(bin); err != nil {
		t.Skip("no bin/sing-box")
	}
	res, _ := filepath.Abs("../../resources")
	t.Setenv("FARO_CORE", bin)
	t.Setenv("HOME", t.TempDir())
	c, err := New("", nil)
	if err != nil {
		t.Fatal(err)
	}
	setsDir := filepath.Join(c.Dir(), "rulesets")
	if err := ruleset.Install(res, setsDir); err != nil {
		t.Fatal(err)
	}
	sets := ruleset.Paths(setsDir)
	if len(sets) == 0 {
		t.Skip("no resources/rulesets: run scripts/fetch-rulesets.sh")
	}

	rig := &routingRig{t: t, core: c, nodes: map[string]*fakeNode{}}
	var outs []singcfg.Outbound
	for _, tag := range tags {
		n := newFakeNode(t)
		rig.nodes[tag] = n
		outs = append(outs, singcfg.Outbound{Tag: tag, Out: map[string]any{"type": "socks", "server": "127.0.0.1", "server_port": n.port}})
	}
	plan, errs := rules.Compile(text, tags, choices)
	if errs != nil {
		t.Fatal(errs)
	}
	if !keepIPv4 {
		for i := range plan.Routes {
			plan.Routes[i].IPv4 = false
		}
	}
	if err := c.Start(singcfg.Input{Nodes: outs, Plan: plan, Choices: choices, Sets: sets}, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Stop)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := c.WaitReady(ctx); err != nil {
		t.Fatalf("%v\n%q", err, c.Logs())
	}
	return rig
}

// connect sends an HTTP CONNECT through the core's proxy and returns the
// status line, "" if the connection was closed without one.
func (r *routingRig) connect(host string) string {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", r.core.MixedPort()), 3*time.Second)
	if err != nil {
		r.t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(4 * time.Second))
	fmt.Fprintf(c, "CONNECT %s:443 HTTP/1.1\r\nHost: %s:443\r\n\r\n", host, host)
	line, _ := bufio.NewReader(c).ReadString('\n')
	return strings.TrimSpace(line)
}

// connectClosed reports whether the core closes the tunnel it just opened,
// as it does for a rejected destination, rather than leaving it open.
func (r *routingRig) connectClosed(host string) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", r.core.MixedPort()), 3*time.Second)
	if err != nil {
		r.t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprintf(c, "CONNECT %s:443 HTTP/1.1\r\nHost: %s:443\r\n\r\n", host, host)
	br := bufio.NewReader(c)
	br.ReadString('\n') // the 200
	br.ReadString('\n') // the blank line
	_, err = br.ReadByte()
	return err == io.EOF || (err != nil && !os.IsTimeout(err))
}

// whoSaw names the nodes that were asked for host, after a moment.
func (r *routingRig) whoSaw(host string) []string {
	time.Sleep(300 * time.Millisecond)
	var out []string
	for tag, n := range r.nodes {
		if n.saw(host) {
			out = append(out, tag)
		}
	}
	return out
}

func (r *routingRig) expect(host string, want ...string) {
	r.t.Helper()
	status := r.connect(host)
	got := r.whoSaw(host)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		r.t.Errorf("%s (%s): went to %v, want %v", host, status, got, want)
	}
}

var allNodes = []string{"🇬🇧 London 01", "Ukraine 01", "🇩🇪 Frankfurt 01", "Sweden 01", "🇺🇸 LA 01"}

func TestClaudeToUKGeminiToDE(t *testing.T) {
	r := newRig(t, fixtures.AIAccounts(), allNodes, nil)
	// Claude, in all its names, leaves from the one UK node and no other.
	for _, h := range []string{"claude.ai", "api.anthropic.com", "www.anthropic.com", "claude.com"} {
		r.expect(h, "🇬🇧 London 01")
	}
	// Gemini from Germany.
	for _, h := range []string{"gemini.google.com", "aistudio.google.com", "generativelanguage.googleapis.com"} {
		r.expect(h, "🇩🇪 Frankfurt 01")
	}
	// Ukraine and Sweden are never a UK or German exit.
	if n := r.nodes["Ukraine 01"]; len(n.seen) != 0 && n.saw("claude.ai") {
		t.Error("Claude reached the Ukraine node")
	}
	for _, tag := range []string{"Ukraine 01", "Sweden 01"} {
		for _, h := range []string{"claude.ai", "gemini.google.com"} {
			if r.nodes[tag].saw(h) {
				t.Errorf("%s reached %s", h, tag)
			}
		}
	}
}

func TestGeneralTrafficAndDirect(t *testing.T) {
	r := newRig(t, fixtures.AIAccounts(), allNodes, nil)
	// Not matched by anything: the main group, i.e. some node.
	r.connect("example.org")
	if len(r.whoSaw("example.org")) != 1 {
		t.Errorf("example.org went to %v, want one node", r.whoSaw("example.org"))
	}
	// China goes direct: no node is asked.
	r.connect("www.baidu.com")
	if got := r.whoSaw("www.baidu.com"); len(got) != 0 {
		t.Errorf("a Chinese site went through %v", got)
	}
}

func TestStrictNoGermanNode(t *testing.T) {
	r := newRig(t, fixtures.AIAccounts(), []string{"🇬🇧 London 01", "🇺🇸 LA 01"}, nil)
	// CONNECT is answered before the route is chosen, so blocked is seen in
	// what follows: the core closes the connection, and no node is asked.
	if closed := r.connectClosed("gemini.google.com"); !closed {
		t.Error("the connection to Gemini was left open with no German node")
	}
	if got := r.whoSaw("gemini.google.com"); len(got) != 0 {
		t.Errorf("Gemini went out through %v, want blocked", got)
	}
	r.expect("claude.ai", "🇬🇧 London 01") // the rest is unaffected
}

func TestPrivateRuleBeatsGeneral(t *testing.T) {
	text := "route claude region UK\n\ndirect full:internal.claude.ai\nclaude domain:claude.ai\n"
	r := newRig(t, text, []string{"UK 01", "DE 01"}, nil)
	r.connect("internal.claude.ai")
	if got := r.whoSaw("internal.claude.ai"); len(got) != 0 {
		t.Errorf("the private direct rule lost: went to %v", got)
	}
	r.expect("www.claude.ai", "UK 01")
}

func TestFixedNodeStaysAndCanBeSwitched(t *testing.T) {
	tags := []string{"UK 01", "UK 02", "UK 03", "DE 01"}
	// The user picked UK 02: Claude uses it, not the first.
	r := newRig(t, fixtures.AIAccounts(), tags, map[string]string{"claude": "UK 02"})
	r.expect("claude.ai", "UK 02")
	r.connect("claude.ai") // and again: no drifting to another exit
	r.connect("api.anthropic.com")
	for _, other := range []string{"UK 01", "UK 03"} {
		if r.nodes[other].saw("claude.ai") || r.nodes[other].saw("api.anthropic.com") {
			t.Errorf("Claude drifted to %s", other)
		}
	}
	// Switching the route's node takes effect at once, through the controller.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.core.API().Select(ctx, "route-claude", "UK 03"); err != nil {
		t.Fatal(err)
	}
	r.expect("claude.com", "UK 03")
}

// connectRaw is connect with the authority written out, for an IPv6 address.
func (r *routingRig) connectRaw(authority string) string {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", r.core.MixedPort()), 3*time.Second)
	if err != nil {
		r.t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(4 * time.Second))
	fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", authority, authority)
	line, _ := bufio.NewReader(c).ReadString('\n')
	return strings.TrimSpace(line)
}

func (r *routingRig) selectIn(group, choice string) {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.core.API().Select(ctx, group, choice); err != nil {
		r.t.Fatalf("select %s %s: %v", choice, group, err)
	}
}

func TestGateCutsClaudeAndOpensAgain(t *testing.T) {
	tags := []string{"UK 01", "DE 01"}

	// Opened by choice at the start: the gate is open and Claude goes out.
	r := newRig(t, fixtures.AIAccounts(), tags, nil)
	r.expect("claude.ai", "UK 01")

	// Shut at run time: Claude is cut at once, without a restart.
	r.selectIn("route-claude", "block")
	if !r.connectClosed("api.anthropic.com") {
		t.Error("the gate is shut, but the connection to Anthropic stayed open")
	}
	if got := r.whoSaw("api.anthropic.com"); len(got) != 0 {
		t.Errorf("a shut gate let Anthropic out through %v", got)
	}
	// OpenAI follows the node, not the gate: it is not cut.
	r.expect("api.openai.com", "UK 01")
	// And others are not affected.
	r.expect("www.google.com", "DE 01")

	// Opened again: Claude goes out on the same node.
	r.selectIn("route-claude", "UK 01")
	r.expect("console.anthropic.com", "UK 01")
}

func TestGateClosedFromTheStart(t *testing.T) {
	// A restart with the gate shut must not open for a moment.
	r := newRig(t, fixtures.AIAccounts(), []string{"UK 01", "DE 01"}, map[string]string{"claude" + rules.GateSuffix: "closed"})
	if !r.connectClosed("claude.ai") {
		t.Error("Claude got out with the gate shut at the start")
	}
	if got := r.whoSaw("claude.ai"); len(got) != 0 {
		t.Errorf("went to %v", got)
	}
	r.expect("www.google.com", "DE 01")
}

func TestIPv6ToClaudeBlockedIPv4Routed(t *testing.T) {
	r := newRig(t, fixtures.AIAccounts(), []string{"UK 01", "DE 01"}, nil)
	// Anthropic's IPv6 range is rejected; its IPv4 range goes through the route.
	if !closedAfterConnect(r, "[2607:6bc0::1]:443") {
		t.Error("an Anthropic IPv6 address was not rejected")
	}
	if got := r.whoSaw("2607:6bc0::1"); len(got) != 0 {
		t.Errorf("the IPv6 address was sent out through %v", got)
	}
	r.connectRaw("160.79.104.5:443")
	if got := r.whoSaw("160.79.104.5"); len(got) != 1 || got[0] != "UK 01" {
		t.Errorf("an Anthropic IPv4 address went to %v, want UK 01", got)
	}
}

func closedAfterConnect(r *routingRig, authority string) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", r.core.MixedPort()), 3*time.Second)
	if err != nil {
		r.t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", authority, authority)
	br := bufio.NewReader(c)
	br.ReadString('\n')
	br.ReadString('\n')
	_, err = br.ReadByte()
	return err == io.EOF || (err != nil && !os.IsTimeout(err))
}

// A rule can look at the program that made the connection, and the core can
// tell through the proxy port, with no TUN: Claude's app and command line,
// and Codex, are caught by name.
func TestProcessMatchThroughProxyPort(t *testing.T) {
	self := filepath.Base(os.Args[0]) // the test binary: "core.test"
	text := "route uk region UK\nuk process:other process:" + self + "\nblock process:nobody-has-this-name domain:example.net\n"
	r := newRig(t, text, []string{"UK 01", "DE 01"}, nil)
	r.expect("example.org", "UK 01") // this program is named: it goes to the route
	r.connect("example.net")         // a rule for another program does not catch it
	if got := r.whoSaw("example.net"); len(got) != 1 {
		t.Errorf("example.net went to %v: another program's block rule must not apply to this one", got)
	}
}

// ---- DNS

// anyNodeSaw reports whether any node was asked to connect to host.
func (r *routingRig) anyNodeSaw(host string) bool {
	for _, n := range r.nodes {
		if n.saw(host) {
			return true
		}
	}
	return false
}

// A name that must be looked up (a rule on addresses makes the core resolve it)
// is looked up THROUGH the proxy, unless it is a name in China. The question
// reaches a node as a connection to the DNS server, which is how it is seen
// here, with no need of the network: the node is a stand-in that records where
// it was asked to connect.
func TestForeignNamesAreLookedUpThroughTheProxy(t *testing.T) {
	text := "direct set:geosite-cn\ndirect set:geoip-cn\n"
	r := newRig(t, text, []string{"UK 01"}, nil)
	time.Sleep(1500 * time.Millisecond) // the automatic health checks of the nodes are over
	before := r.anyNodeSaw("8.8.8.8")

	// example.org is not in China: it must be looked up, and by the foreign server, via the node.
	r.connect("example.org")
	waitFor := time.Now().Add(5 * time.Second)
	for !r.anyNodeSaw("8.8.8.8") && time.Now().Before(waitFor) {
		time.Sleep(50 * time.Millisecond)
	}
	if !r.anyNodeSaw("8.8.8.8") {
		t.Fatalf("a foreign name was looked up, but not through the proxy: the node saw no connection to the foreign DNS server (saw it before: %v)", before)
	}
	// And never the domestic server, which is asked directly, not through the node.
	if r.anyNodeSaw("223.5.5.5") {
		t.Error("the domestic DNS server was asked through the proxy")
	}
}

// A name in China that no rule has sent direct yet (these rules do not say
// "direct set:geosite-cn": only a rule on addresses follows) reaches the point
// where the core must look it up. The DNS rule is the last thing between it and
// the foreign server: it must go to the domestic one, which is not asked through
// the proxy. Without that rule it would go to the foreign server, through the node.
func TestNamesInChinaAreNotLookedUpThroughTheProxy(t *testing.T) {
	text := "direct set:geoip-cn\n"
	r := newRig(t, text, []string{"UK 01"}, nil)
	time.Sleep(1500 * time.Millisecond)

	r.connect("www.baidu.com")
	time.Sleep(2 * time.Second)
	if r.anyNodeSaw("8.8.8.8") {
		t.Error("a name in China was looked up through the proxy, by the foreign server")
	}
	if r.anyNodeSaw("www.baidu.com") {
		t.Error("a name in China was sent to the proxy")
	}
	// The control: a name that is not in China, with the same rules, is looked up through it.
	r.connect("example.org")
	deadline := time.Now().Add(5 * time.Second)
	for !r.anyNodeSaw("8.8.8.8") && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !r.anyNodeSaw("8.8.8.8") {
		t.Error("the control failed: a foreign name was not looked up through the proxy, so the check above proves nothing")
	}
}

// Whatever the DNS does, a name the rules send to a route is handed to the node
// by name and is not looked up here at all: the node does it, at its end.
func TestRoutedNamesAreNotLookedUpHere(t *testing.T) {
	r := newRig(t, "route work region UK\nwork domain:example.com\ndirect set:geosite-cn\n", []string{"UK 01"}, nil)
	time.Sleep(1500 * time.Millisecond)
	r.connect("www.example.com")
	time.Sleep(800 * time.Millisecond)
	if !r.nodes["UK 01"].saw("www.example.com") {
		t.Fatal("the node was not handed the name")
	}
	if r.anyNodeSaw("8.8.8.8") || r.anyNodeSaw("223.5.5.5") {
		t.Error("a routed name caused a DNS lookup")
	}
}

// The person's groups of sites decide before the rules text, with a real core:
// a site goes where its group says, even where the text says otherwise, and a
// group aimed at block shuts it out.
func TestGroupsOfSitesDecideFirst(t *testing.T) {
	head, _ := domains.Head([]domains.Group{
		{Name: "to the UK", Target: "claude", Domains: []string{"gemini.google.com", "plain.example.net"}},
		{Name: "shut", Target: "block", Domains: []string{"shut.example.net"}},
	})
	r := newRig(t, head+fixtures.AIAccounts(), allNodes, nil)
	// The text sends Google from Germany; the group says the UK node, and wins.
	r.expect("gemini.google.com", "🇬🇧 London 01")
	// Something no rule names would go to the main group; the group sends it to the UK.
	r.expect("plain.example.net", "🇬🇧 London 01")
	// What the group does not hold goes as the text says.
	r.expect("aistudio.google.com", "🇩🇪 Frankfurt 01")
	// A group aimed at block lets nothing through.
	r.connect("shut.example.net")
	if got := r.whoSaw("shut.example.net"); len(got) != 0 {
		t.Errorf("a blocked site reached %v", got)
	}
}

// An ipv4 route has the names sent to it looked up through itself: the question
// goes out through its node, to the foreign server, and through no other node.
func TestAnIPv4RouteLooksNamesUpThroughItsOwnNode(t *testing.T) {
	r := newRigWith(t, fixtures.AIAccounts(), allNodes, nil, true)
	var dns string
	for _, s := range singcfg.DNSServers() {
		if s.Side == "foreign" {
			dns = s.Address
			break
		}
	}
	r.connect("claude.ai")
	waitFor := time.Now().Add(5 * time.Second)
	for !r.nodes["🇬🇧 London 01"].saw(dns) && time.Now().Before(waitFor) {
		time.Sleep(100 * time.Millisecond)
	}
	if got := r.whoSaw(dns); strings.Join(got, ",") != "🇬🇧 London 01" {
		t.Errorf("the lookup for a name sent to the UK route went through %v, want the UK node alone", got)
	}
	// The name itself is never handed to a node: only an address would be, once
	// the lookup answered, which a fake node never lets it.
	if got := r.whoSaw("claude.ai"); len(got) != 0 {
		t.Errorf("the name reached %v", got)
	}
}
