package engine

import (
	"context"
	"encoding/json"
	"errors"
	"faro/internal/singcfg"
	"faro/internal/sub"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"faro/internal/domains"
	"faro/internal/fixtures"
	"faro/internal/presets"
	"faro/internal/rules"
	"faro/internal/ruleset"
)

// ---- helpers

// newEngine opens an engine in a private data directory, on the real core.
func newEngine(t *testing.T, run func(args ...string) (string, error)) *Engine {
	t.Helper()
	return newEngineWith(t, run, nil)
}

// newEngineWith is newEngine with the options changed by set.
func newEngineWith(t *testing.T, run func(args ...string) (string, error), set func(*Options)) *Engine {
	t.Helper()
	bin, _ := filepath.Abs("../../bin/sing-box")
	if _, err := os.Stat(bin); err != nil {
		t.Skip("no bin/sing-box")
	}
	res, _ := filepath.Abs("../../resources")
	t.Setenv("FARO_CORE", bin)
	t.Setenv("HOME", t.TempDir())
	o := Options{ResourcesDir: res, ProxyRunner: run}
	if set != nil {
		set(&o)
	}
	e, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// subFile writes a Clash subscription file with the named nodes, to add by path.
func subFile(t *testing.T, names ...string) string {
	var b strings.Builder
	b.WriteString("proxies:\n")
	for i, n := range names {
		fmt.Fprintf(&b, "  - {name: %q, type: ss, server: h%d.example.com, port: %d, cipher: aes-256-gcm, password: p}\n", n, i, 1000+i)
	}
	p := filepath.Join(t.TempDir(), "profile.yaml")
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *Engine) addAndWait(t *testing.T, path string, wantNodes int) {
	t.Helper()
	e.AddSubscription(path)
	waitFor(t, "the subscription", func() bool { st := e.State(); return st.Busy == "" && len(st.Nodes) == wantNodes })
	if st := e.State(); st.Err != "" {
		t.Fatal(st.Err)
	}
}

// fakeNetworksetup answers like networksetup with one service and no proxy
// set, and remembers what it was told to change.
type fakeNetworksetup struct {
	mu    sync.Mutex
	state map[string]string // "-setwebproxy" etc. -> last "host port"
	calls []string
}

func (f *fakeNetworksetup) run(args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.Join(args, " "))
	switch {
	case args[0] == "-listallnetworkservices":
		return "An asterisk (*) denotes that a network service is disabled.\nWi-Fi\n", nil
	case strings.HasPrefix(args[0], "-get") && args[0] != "-getproxybypassdomains":
		if v, ok := f.state[strings.Replace(args[0], "-get", "-set", 1)]; ok {
			h, p, _ := strings.Cut(v, " ")
			return fmt.Sprintf("Enabled: Yes\nServer: %s\nPort: %s\n", h, p), nil
		}
		return "Enabled: No\nServer: \nPort: 0\n", nil
	case args[0] == "-getproxybypassdomains":
		return "There aren't any bypass domains set on Wi-Fi.\n", nil
	case strings.HasPrefix(args[0], "-set") && !strings.HasSuffix(args[0], "state") && args[0] != "-setproxybypassdomains":
		if f.state == nil {
			f.state = map[string]string{}
		}
		f.state[args[0]] = args[2] + " " + args[3]
	case strings.HasSuffix(args[0], "state") && args[len(args)-1] == "off":
		delete(f.state, strings.TrimSuffix(args[0], "state"))
	}
	return "", nil
}

func (f *fakeNetworksetup) enabledPort() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.state["-setwebproxy"]; ok {
		return strings.Fields(v)[1]
	}
	return ""
}

// ---- tests

func TestNewState(t *testing.T) {
	e := newEngine(t, (&fakeNetworksetup{}).run)
	st := e.State()
	if st.Status != Stopped || st.MixedPort == 0 || st.Err != "" {
		t.Errorf("state %+v", st)
	}
	if st.Plan == nil || st.RulesText != presets.Base() {
		t.Error("a new install starts with the base rules, which compile")
	}
	if st.DataDir == "" || !strings.HasSuffix(st.DataDir, "Faro") {
		t.Errorf("data dir %q", st.DataDir)
	}
	// A snapshot is a copy: changing it does not change the engine.
	st.Choices["x"] = "y"
	st.Delays["x"] = 1
	if _, ok := e.State().Choices["x"]; ok {
		t.Error("the snapshot shares its map")
	}
}

func TestSubscriptionFromFileAndNodes(t *testing.T) {
	e := newEngine(t, (&fakeNetworksetup{}).run)
	var notified int
	var mu sync.Mutex
	e.opts.Notify = func() { mu.Lock(); notified++; mu.Unlock() }
	e.addAndWait(t, subFile(t, "UK 01", "DE 01", "剩余流量：1 GB"), 3)
	st := e.State()
	if len(st.Subs) != 1 || st.Nodes[0].Tag != "UK 01" || st.Nodes[0].Kind != "shadowsocks" {
		t.Errorf("state %+v", st)
	}
	mu.Lock()
	n := notified
	mu.Unlock()
	if n == 0 {
		t.Error("the owner was never told of the change")
	}
	// A refusal is shown, and leaves things as they were.
	e.AddSubscription("ftp://nope")
	waitFor(t, "the error", func() bool { return e.State().Err != "" && e.State().Busy == "" })
	if len(e.State().Nodes) != 3 {
		t.Error("a refused subscription changed the nodes")
	}
}

func TestStartStopAndStablePort(t *testing.T) {
	e := newEngine(t, (&fakeNetworksetup{}).run)
	port := e.State().MixedPort
	for i := 0; i < 2; i++ {
		e.Start()
		waitFor(t, "running", func() bool { return e.State().Status == Running })
		st := e.State()
		if st.Version == "" || st.MixedPort != port {
			t.Errorf("run %d: %+v", i, st)
		}
		e.Stop()
		waitFor(t, "stopped", func() bool { return e.State().Status == Stopped })
	}
	if e.State().MixedPort != port {
		t.Error("the proxy port changed between runs")
	}
	// Starting twice does not start two cores.
	e.Start()
	e.Start()
	waitFor(t, "running", func() bool { return e.State().Status == Running })
}

func TestRulesApplyAndPresets(t *testing.T) {
	e := newEngine(t, (&fakeNetworksetup{}).run)
	e.addAndWait(t, subFile(t, "🇬🇧 London 01", "🇬🇧 London 02", "🇩🇪 Frankfurt 01"), 3)

	// Errors come back with their lines, and nothing is kept.
	before := e.State().RulesText
	errs := e.ApplyRules("route a region ZZ\nnope domain:x.com\n")
	if len(errs) != 2 || errs[0].Line != 1 || errs[1].Line != 2 {
		t.Fatalf("errors %v", errs)
	}
	if e.State().RulesText != before {
		t.Error("bad rules replaced the good ones")
	}
	if _, err := os.Stat(filepath.Join(e.State().DataDir, "rules.txt")); err == nil {
		t.Error("bad rules were saved")
	}

	// New rules replace the old; their routes find their nodes.
	if errs := e.ApplyRules(fixtures.AIAccounts()); errs != nil {
		t.Fatal(errs)
	}
	st := e.State()
	by := map[string][]string{}
	for _, r := range st.Plan.Routes {
		by[r.ID] = r.Nodes
	}
	if len(by["claude"]) != 2 || len(by["google"]) != 1 {
		t.Errorf("routes %v", by)
	}
	b, _ := os.ReadFile(filepath.Join(st.DataDir, "rules.txt"))
	if string(b) != st.RulesText {
		t.Error("the rules in force are not the ones saved")
	}
	// The same rules after a restart of the app.
	e2, err := New(Options{ResourcesDir: "", ProxyRunner: (&fakeNetworksetup{}).run})
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	if e2.State().RulesText != st.RulesText {
		t.Error("the rules were not read back")
	}

	// Rules that define a route twice are an error to read, not a mess.
	if errs := e.ApplyRules(e.State().RulesText + "\n\n" + fixtures.AIAccounts()); len(errs) == 0 {
		t.Error("the same rules written twice were accepted")
	}
	// More rules written after the base rules are fine.
	e.ApplyRules(presets.Base())
	if errs := e.ApplyRules(presets.Base() + "\n\n" + fixtures.AIAccounts()); errs != nil {
		t.Errorf("rules added to the base rules: %v", errs)
	}
}

func TestChooseAndGateReachTheRunningCore(t *testing.T) {
	e := newEngine(t, (&fakeNetworksetup{}).run)
	e.addAndWait(t, subFile(t, "🇬🇧 London 01", "🇬🇧 London 02", "🇩🇪 Frankfurt 01"), 3)
	if errs := e.ApplyRules(fixtures.AIAccounts()); errs != nil {
		t.Fatal(errs)
	}
	e.Start()
	waitFor(t, "running", func() bool { return e.State().Status == Running })
	now := func(group string) string {
		ctx, cancel := timeout(5 * time.Second)
		defer cancel()
		ps, err := e.core.API().Proxies(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return ps[group].Now
	}

	e.Choose("claude", "🇬🇧 London 02")
	waitFor(t, "the node", func() bool { return now("route-claude") == "🇬🇧 London 02" })
	if e.State().Choices["claude"] != "🇬🇧 London 02" {
		t.Error("the choice is not in the state")
	}

	e.SetGate("claude", true)
	waitFor(t, "the gate", func() bool { return now("route-claude") == "block" })
	// Choosing while the gate is shut keeps it shut.
	e.Choose("claude", "🇬🇧 London 01")
	time.Sleep(300 * time.Millisecond)
	if now("route-claude") != "block" {
		t.Errorf("a node choice opened the gate: %q", now("route-claude"))
	}
	e.SetGate("claude", false)
	waitFor(t, "the gate open", func() bool { return now("route-claude") == "🇬🇧 London 01" })

	// The main group.
	e.Choose("proxy", "🇩🇪 Frankfurt 01")
	waitFor(t, "the main node", func() bool { return now("proxy") == "🇩🇪 Frankfurt 01" })
	e.Choose("proxy", "")
	waitFor(t, "auto", func() bool { return now("proxy") == "auto" })

	// A shut gate and the choices survive a restart of the core.
	e.SetGate("claude", true)
	e.Restart()
	waitFor(t, "running again", func() bool { return e.State().Status == Running })
	waitFor(t, "shut at start", func() bool { return now("route-claude") == "block" })
}

func timeout(d time.Duration) (ctx ctxT, cancel func()) { return ctxWithTimeout(d) }

func TestSystemProxyFollowsTheCore(t *testing.T) {
	f := &fakeNetworksetup{}
	e := newEngine(t, f.run)
	port := strconv.Itoa(e.State().MixedPort)

	// Wanted while stopped: nothing is touched until the core runs.
	e.SetSystemProxy(true)
	time.Sleep(200 * time.Millisecond)
	if f.enabledPort() != "" {
		t.Fatal("the system proxy was set with no core to point at")
	}
	e.Start()
	waitFor(t, "running", func() bool { return e.State().Status == Running })
	waitFor(t, "the proxy set", func() bool { return f.enabledPort() == port })

	// Stop puts it back before the core goes.
	e.Stop()
	waitFor(t, "stopped", func() bool { return e.State().Status == Stopped })
	waitFor(t, "the proxy put back", func() bool { return f.enabledPort() == "" })
	if _, err := os.Stat(filepath.Join(e.State().DataDir, "sysproxy-backup.json")); err == nil {
		t.Error("the backup was left")
	}

	// Turned off while running: put back, and the core goes on.
	e.Start()
	waitFor(t, "set again", func() bool { return f.enabledPort() == port })
	e.SetSystemProxy(false)
	waitFor(t, "put back", func() bool { return f.enabledPort() == "" })
	if e.State().Status != Running {
		t.Error("turning the proxy off stopped the core")
	}
	if e.State().SystemProxy {
		t.Error("the state still says on")
	}
}

func TestRapidRestartsLeaveOneCoreAndTheLastWord(t *testing.T) {
	e := newEngine(t, (&fakeNetworksetup{}).run)
	e.Start()
	waitFor(t, "running", func() bool { return e.State().Status == Running })
	// Restarts on top of each other, and then a stop: an old run must not stop the new core,
	// and the stop, being last, must win.
	for i := 0; i < 4; i++ {
		e.Restart()
	}
	e.Stop()
	waitFor(t, "stopped", func() bool { return e.State().Status == Stopped })
	time.Sleep(1500 * time.Millisecond)
	if st := e.State(); st.Status != Stopped {
		t.Errorf("an old run came back: %s (%s)", st.Status, st.Err)
	}
	if n := coresOf(e); n != 0 {
		t.Errorf("%d cores left running", n)
	}

	// The other way: restarts, no stop. One core, running.
	e.Start()
	for i := 0; i < 3; i++ {
		e.Restart()
	}
	waitFor(t, "running", func() bool { return e.State().Status == Running })
	time.Sleep(1500 * time.Millisecond)
	if st := e.State(); st.Status != Running || st.Err != "" {
		t.Errorf("after restarts: %s (%s)", st.Status, st.Err)
	}
	if n := coresOf(e); n != 1 {
		t.Errorf("%d cores running, want 1", n)
	}
}

// coresOf counts the running cores that use this engine's data directory.
func coresOf(e *Engine) int {
	out, _ := exec.Command("pgrep", "-f", "sing-box run -c "+e.State().DataDir).Output()
	return len(strings.Fields(string(out)))
}

func TestCoreDyingIsShownAndTheProxyPutBack(t *testing.T) {
	f := &fakeNetworksetup{}
	e := newEngine(t, f.run)
	e.SetSystemProxy(true)
	e.Start()
	waitFor(t, "running", func() bool { return e.State().Status == Running })
	waitFor(t, "the proxy set", func() bool { return f.enabledPort() != "" })

	out, _ := exec.Command("pgrep", "-f", "sing-box run -c "+e.State().DataDir).Output()
	for _, p := range strings.Fields(string(out)) {
		pid, _ := strconv.Atoi(p)
		syscall.Kill(pid, syscall.SIGKILL)
	}
	waitFor(t, "stopped", func() bool { return e.State().Status == Stopped })
	if st := e.State(); !strings.Contains(st.Err, "stopped unexpectedly") {
		t.Errorf("error %q", st.Err)
	}
	waitFor(t, "the proxy put back", func() bool { return f.enabledPort() == "" })
}

func TestStartErrorsAreShownWithTheReason(t *testing.T) {
	e := newEngine(t, (&fakeNetworksetup{}).run)
	// The proxy port is taken by someone else: an error to read, not a move to another port.
	l, err := listen(e.State().MixedPort)
	if err != nil {
		t.Skip("could not take the port")
	}
	defer l.Close()
	e.Start()
	waitFor(t, "the error", func() bool { st := e.State(); return st.Status == Stopped && st.Err != "" })
	if !strings.Contains(e.State().Err, "in use") {
		t.Errorf("error %q", e.State().Err)
	}
	if e.State().MixedPort != 0 && coresOf(e) != 0 {
		t.Error("a core was started anyway")
	}
}

func TestLastLogLine(t *testing.T) {
	logs := []string{"+0800 INFO start", "+0800 ERROR dial tcp: refused", "+0800 FATAL start service: bad uuid", "INFO bye"}
	if got := lastLogLine(logs); got != ": FATAL start service: bad uuid" {
		t.Errorf("%q", got)
	}
	if got := lastLogLine([]string{"INFO only"}); got != "" {
		t.Errorf("%q", got)
	}
}

func TestConnectionsAndTrafficHistory(t *testing.T) {
	e := newEngine(t, (&fakeNetworksetup{}).run)
	e.Start()
	waitFor(t, "running", func() bool { return e.State().Status == Running })

	// Nobody is watching: nobody is asked.
	time.Sleep(1500 * time.Millisecond)
	if n := len(e.State().Conns); n != 0 {
		t.Fatalf("%d connections listed while not watched", n)
	}

	e.WatchConnections(true)
	// A connection that stays open: a CONNECT to an address nothing answers at.
	c, err := dial(e.State().MixedPort)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "CONNECT 192.0.2.1:443 HTTP/1.1\r\nHost: 192.0.2.1:443\r\n\r\n")
	waitFor(t, "the connection", func() bool {
		for _, k := range e.State().Conns {
			if k.Host == "192.0.2.1" {
				return true
			}
		}
		return false
	})
	var got string
	for _, k := range e.State().Conns {
		if k.Host == "192.0.2.1" {
			got = k.Port + " " + k.Network + " " + k.Rule + " " + strings.Join(k.Chain, ">")
			if k.Start.IsZero() || len(k.Chain) == 0 {
				t.Errorf("connection %+v", k)
			}
			e.CloseConnection(k.ID)
		}
	}
	if !strings.HasPrefix(got, "443 tcp") {
		t.Errorf("connection: %q", got)
	}
	waitFor(t, "the connection to close", func() bool {
		for _, k := range e.State().Conns {
			if k.Host == "192.0.2.1" {
				return false
			}
		}
		return true
	})

	// Not watched again: the list goes, and polling stops.
	e.WatchConnections(false)
	if n := len(e.State().Conns); n != 0 {
		t.Errorf("%d connections kept after the last view left", n)
	}

	// A minute of traffic, a sample a second, oldest first, never more than 60.
	waitFor(t, "samples", func() bool { return len(e.State().History) >= 3 })
	if n := len(e.State().History); n > 60 {
		t.Errorf("%d samples", n)
	}
	e.Stop()
	waitFor(t, "stopped", func() bool { return e.State().Status == Stopped })
	if st := e.State(); len(st.History) != 0 || len(st.Conns) != 0 {
		t.Error("history or connections kept after stop")
	}
}

// The preview is for reading and for sharing: no server, no port, no key, no
// password and no controller secret may be in it.
func TestPreviewHoldsNoSecrets(t *testing.T) {
	e := newEngine(t, (&fakeNetworksetup{}).run)
	path := filepath.Join(t.TempDir(), "profile.yaml")
	os.WriteFile(path, []byte(`proxies:
  - {name: "🇬🇧 London 01", type: trojan, server: secret-host-uk.example.com, port: 9443, password: SECRETPASSWORD1, sni: sni-secret.example.com}
  - {name: "🇩🇪 Frankfurt 01", type: vless, server: secret-host-de.example.com, port: 8443, uuid: 11111111-2222-3333-4444-555555555555, tls: true, servername: sni2.example.com, reality-opts: {public-key: SECRETPUBLICKEY, short-id: SECRETSHORTID}}
`), 0o600)
	e.AddSubscription(path)
	waitFor(t, "nodes", func() bool { st := e.State(); return st.Busy == "" && len(st.Nodes) == 2 })
	if errs := e.ApplyRules(fixtures.AIAccounts()); errs != nil {
		t.Fatal(errs)
	}
	out, err := e.Preview()
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-host", "9443", "8443", "SECRETPASSWORD1", "11111111-2222", "SECRETPUBLICKEY", "SECRETSHORTID",
		"sni-secret", "sni2.example", "external_controller", "secret", e.State().DataDir, "127.0.0.1"} {
		if strings.Contains(out, secret) {
			t.Errorf("the preview holds %q", secret)
		}
	}
	// What it is for is still there: the groups, the rules, which node is where.
	for _, want := range []string{`"route-claude"`, `"route-google"`, `"selector"`, `"🇬🇧 London 01"`, `"domain_suffix"`, `"rule_set"`, `"action": "reject"`, `"type": "trojan"`, `"type": "vless"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the preview lacks %s", want)
		}
	}
	// The routing comes before the nodes, and it is valid JSON.
	if r, o := strings.Index(out, `"route":`), strings.Index(out, `"outbounds":`); r < 0 || o < 0 || r > o {
		t.Errorf("route at %d, outbounds at %d", r, o)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Errorf("the preview is not JSON: %v", err)
	}
	// Rules with errors have nothing to preview.
	e.mu.Lock()
	e.plan = nil
	e.mu.Unlock()
	if _, err := e.Preview(); err == nil {
		t.Error("a preview of rules with errors")
	}
}

func TestChangingThePort(t *testing.T) {
	f := &fakeNetworksetup{}
	e := newEngine(t, f.run)
	old := e.State().MixedPort

	// Refused, with a reason, and nothing changes.
	for _, bad := range []int{0, 80, 1023, 65536, -5} {
		if err := e.SetMixedPort(bad); err == nil {
			t.Errorf("port %d accepted", bad)
		}
	}
	taken, err := listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	takenPort := taken.Addr().(*net.TCPAddr).Port
	if err := e.SetMixedPort(takenPort); err == nil || !strings.Contains(err.Error(), "in use") {
		t.Errorf("a port in use: %v", err)
	}
	if e.State().MixedPort != old {
		t.Fatal("a refused port changed the port")
	}
	if err := e.SetMixedPort(old); err != nil { // the same one: nothing to do
		t.Error(err)
	}

	// Stopped: it is kept, and is the one the next run uses.
	free := freePort(t)
	if err := e.SetMixedPort(free); err != nil {
		t.Fatal(err)
	}
	if e.State().MixedPort != free {
		t.Fatalf("port %d, want %d", e.State().MixedPort, free)
	}
	b, _ := os.ReadFile(filepath.Join(e.State().DataDir, "prefs.json"))
	if !strings.Contains(string(b), strconv.Itoa(free)) {
		t.Errorf("not saved: %s", b)
	}
	e.SetSystemProxy(true)
	e.Start()
	waitFor(t, "running", func() bool { return e.State().Status == Running })
	if !portOpen(free) {
		t.Fatalf("the core is not on port %d", free)
	}
	waitFor(t, "the proxy follows", func() bool { return f.enabledPort() == strconv.Itoa(free) })

	// Running: the core moves, and the system proxy with it.
	next := freePort(t)
	if err := e.SetMixedPort(next); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "restarted on the new port", func() bool { return e.State().Status == Running && portOpen(next) })
	waitFor(t, "the proxy on the new port", func() bool { return f.enabledPort() == strconv.Itoa(next) })
	if portOpen(free) {
		t.Error("the old port is still open")
	}
	if e.State().Err != "" {
		t.Errorf("error %q", e.State().Err)
	}

	// It is the same after the app is opened again.
	e2, err := New(Options{ProxyRunner: (&fakeNetworksetup{}).run})
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	if e2.State().MixedPort != next {
		t.Errorf("reopened on %d, want %d", e2.State().MixedPort, next)
	}
}

func TestChoosingDNS(t *testing.T) {
	e := newEngine(t, (&fakeNetworksetup{}).run)
	// Nothing chosen is the first of each side.
	if st := e.State(); st.DNS.Domestic != "" || st.DNS.Foreign != "" {
		t.Errorf("a new install has chosen %+v", st.DNS)
	}
	// Refused: an id that is not on its side, and one from the other side.
	for _, bad := range [][2]string{{"nope", "cloudflare"}, {"alidns", "nope"}, {"cloudflare", "alidns"}, {"", ""}} {
		if err := e.SetDNS(bad[0], bad[1]); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	if st := e.State(); st.DNS.Domestic != "" {
		t.Fatal("a refused choice was kept")
	}

	if err := e.SetDNS("dnspod", "google"); err != nil {
		t.Fatal(err)
	}
	if st := e.State(); st.DNS.Domestic != "dnspod" || st.DNS.Foreign != "google" {
		t.Errorf("state %+v", st.DNS)
	}
	b, _ := os.ReadFile(filepath.Join(e.State().DataDir, "prefs.json"))
	if !strings.Contains(string(b), "dnspod") || !strings.Contains(string(b), "google") {
		t.Errorf("not saved: %s", b)
	}
	// It is in the config the engine makes: the preview shows the servers.
	prev, err := e.Preview()
	if err != nil {
		t.Fatal(err)
	}
	_ = prev // the preview leaves the DNS out; the running core is the check, below

	// The same after the app is opened again.
	e2, err := New(Options{ProxyRunner: (&fakeNetworksetup{}).run})
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	if st := e2.State(); st.DNS.Domestic != "dnspod" || st.DNS.Foreign != "google" {
		t.Errorf("reopened with %+v", st.DNS)
	}
}

// A running core restarts to use the new servers, and runs on them.
func TestChangingDNSRestartsTheCoreOnTheNewServers(t *testing.T) {
	e := newEngine(t, (&fakeNetworksetup{}).run)
	addSocksNodes := func() {
		e.store.Subs = []*sub.Subscription{{ID: "t", Name: "t", Nodes: []sub.Node{{Name: "日本-1", Out: map[string]any{"type": "shadowsocks", "server": "h.example.com", "server_port": 1, "method": "aes-256-gcm", "password": "p"}}}}}
		e.mu.Lock()
		e.reloadLocked()
		e.mu.Unlock()
	}
	addSocksNodes()
	e.Start()
	waitFor(t, "running", func() bool { return e.State().Status == Running })
	e.mu.Lock()
	gen := e.gen
	e.mu.Unlock()

	// The same choice again: nothing restarts.
	if err := e.SetDNS("", ""); err == nil {
		t.Fatal("the empty choice was accepted")
	}
	if err := e.SetDNS("alidns", "cloudflare"); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	same := e.gen == gen+1 // a first explicit choice differs from "none": it restarts once
	e.mu.Unlock()
	waitFor(t, "running again", func() bool { return e.State().Status == Running })
	if err := e.SetDNS("alidns", "cloudflare"); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	g2 := e.gen
	e.mu.Unlock()
	time.Sleep(200 * time.Millisecond)
	e.mu.Lock()
	if e.gen != g2 {
		t.Error("choosing what is already chosen restarted the core")
	}
	e.mu.Unlock()
	_ = same

	if err := e.SetDNS("dnspod", "google"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "restarted", func() bool { return e.State().Status == Running })
	// The config the core runs holds the new servers.
	b, err := os.ReadFile(filepath.Join(e.State().DataDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"120.53.53.53", "doh.pub", "8.8.8.8", "dns.google"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the running config lacks %s", want)
		}
	}
	for _, gone := range []string{"223.5.5.5", "1.1.1.1"} {
		if strings.Contains(string(b), gone) {
			t.Errorf("the running config still has %s", gone)
		}
	}
	if strings.Contains(string(b), `"type": "local"`) && strings.Contains(string(b), `"tag": "domestic"`) {
		// the system resolver is the thing this is for avoiding
		if strings.Contains(string(b), `"dns": {`) && strings.Contains(strings.SplitN(string(b), `"inbounds"`, 2)[0], `"type": "local"`) {
			t.Error("the system's resolver is in the running config")
		}
	}
}

// The person's groups of sites: made, changed, kept, checked before the rules.
func TestGroupsOfSites(t *testing.T) {
	e := newEngine(t, (&fakeNetworksetup{}).run)
	e.addAndWait(t, subFile(t, "🇬🇧 London 01", "🇩🇪 Frankfurt 01"), 2)
	if errs := e.ApplyRules(fixtures.AIAccounts()); errs != nil {
		t.Fatal(errs)
	}
	first := func() string { // what the first rule says
		r := e.State().Plan.Rules[0]
		return r.Target
	}

	if err := e.NewGroup("AI", "claude", "https://claude.ai/chat chatgpt.com"); err != nil {
		t.Fatal(err)
	}
	st := e.State()
	if len(st.Groups) != 1 || st.Groups[0].Target != "claude" || len(st.Groups[0].Domains) != 2 || st.Groups[0].Domains[0] != "claude.ai" {
		t.Fatalf("groups %+v", st.Groups)
	}
	if first() != "route-claude" { // a route's outbound is named for it
		t.Errorf("the groups are checked before the rules text: first is %q", first())
	}
	if st.Plan.Rules[0].Line > 0 || st.Plan.Rules[len(st.Plan.Rules)-1].Line < 1 {
		t.Error("a group's rule is before line 1; the text's rules keep their lines")
	}

	// The rules text is as it was, and the groups are in a file of their own.
	if st.RulesText != fixtures.AIAccounts() {
		t.Error("the groups changed the rules text")
	}
	if _, err := os.Stat(filepath.Join(st.DataDir, "domains.json")); err != nil {
		t.Error("the groups were not kept")
	}

	// A site is in one group: adding it to another takes it from the first.
	if err := e.NewGroup("直连", "direct", "intranet.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := e.AddDomains("直连", "claude.ai"); err != nil {
		t.Fatal(err)
	}
	g := e.State().Groups
	if len(g[0].Domains) != 1 || g[0].Domains[0] != "chatgpt.com" || len(g[1].Domains) != 2 {
		t.Errorf("claude.ai was not moved: %+v", g)
	}
	// The field of tokens sets a group's whole list.
	if err := e.SetGroupDomains("直连", []string{"a.com", "b.com"}); err != nil {
		t.Fatal(err)
	}
	if g := e.State().Groups[1].Domains; len(g) != 2 || g[0] != "a.com" {
		t.Errorf("sites %v", g)
	}
	// A whole group goes somewhere else at once; order is the order of checking.
	if err := e.SetGroupTarget("AI", "proxy"); err != nil {
		t.Fatal(err)
	}
	if e.State().Groups[0].Target != "proxy" {
		t.Error("the target was not changed")
	}
	if err := e.MoveGroup("直连", -1); err != nil {
		t.Fatal(err)
	}
	if e.State().Groups[0].Name != "直连" || first() != "direct" {
		t.Errorf("order %+v, first %q", e.State().Groups, first())
	}

	// Refused: not a site, no such place, a name twice, no name.
	for what, err := range map[string]error{
		"not a site":    e.AddDomains("AI", "not a site"),
		"no such place": e.SetGroupTarget("AI", "mars"),
		"name twice":    e.NewGroup("AI", "direct", ""),
		"no name":       e.NewGroup("  ", "direct", ""),
		"no such group": e.DeleteGroup("nope"),
	} {
		if err == nil {
			t.Errorf("%s was accepted", what)
		}
	}
	if len(e.State().Groups) != 2 {
		t.Error("a refused change changed the groups")
	}

	// They are there after a restart of the app.
	e2, err := New(Options{ResourcesDir: "", ProxyRunner: (&fakeNetworksetup{}).run})
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	if g := e2.State().Groups; len(g) != 2 || g[0].Name != "直连" {
		t.Errorf("after a restart %+v", g)
	}

	if err := e.DeleteGroup("AI"); err != nil || len(e.State().Groups) != 1 {
		t.Errorf("delete: %v", err)
	}
}

// A group whose route is gone from the rules is out of force and says why;
// everything else goes on.
func TestAGroupWithAMissingRouteIsBlockedAndSaysWhy(t *testing.T) {
	e := newEngine(t, (&fakeNetworksetup{}).run)
	e.addAndWait(t, subFile(t, "🇬🇧 London 01", "🇩🇪 Frankfurt 01"), 2)
	e.ApplyRules(fixtures.AIAccounts())
	e.NewGroup("AI", "claude", "claude.ai")
	e.NewGroup("直连", "direct", "a.example.com")
	if errs := e.ApplyRules(presets.Base()); errs != nil { // no route "claude" any more
		t.Fatalf("the rules were refused because of a group: %v", errs)
	}
	st := e.State()
	if st.GroupErrors["AI"] == "" || st.GroupErrors["直连"] != "" {
		t.Errorf("group errors %v", st.GroupErrors)
	}
	// Its sites are blocked, not left to the rules below; the other group is as it was.
	if st.Plan == nil || st.Plan.Rules[0].Target != rules.TargetBlock || st.Plan.Rules[0].Matchers[0].Values[0] != "claude.ai" ||
		st.Plan.Rules[1].Target != "direct" {
		t.Errorf("rules %+v", st.Plan.Rules[:2])
	}
	// The route comes back, and the group is in force again.
	e.ApplyRules(fixtures.AIAccounts())
	if len(e.State().GroupErrors) != 0 {
		t.Errorf("group errors %v", e.State().GroupErrors)
	}
}

// The groups reach the config the core is run with.
func TestGroupsReachTheConfig(t *testing.T) {
	e := newEngine(t, (&fakeNetworksetup{}).run)
	e.addAndWait(t, subFile(t, "🇬🇧 London 01", "🇩🇪 Frankfurt 01"), 2)
	e.ApplyRules(fixtures.AIAccounts())
	e.NewGroup("视频", "block", "video.example.com")
	out, err := e.Preview()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "video.example.com") {
		t.Error("the group's site is not in the config")
	}
}

func TestLightModeIsKept(t *testing.T) {
	e := newEngine(t, (&fakeNetworksetup{}).run)
	if e.State().LightMode {
		t.Fatal("on from the start")
	}
	e.SetLightMode(true)
	if !e.State().LightMode {
		t.Error("not turned on")
	}
	e2, err := New(Options{ResourcesDir: "", ProxyRunner: (&fakeNetworksetup{}).run})
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	if !e2.State().LightMode {
		t.Error("not there after a restart of the app")
	}
}

func TestRuleSetsAddedToGroups(t *testing.T) {
	res, _ := filepath.Abs("../../resources/rulesets/geosite-xai.srs")
	fetched := 0
	fail := false
	e := newEngineWith(t, (&fakeNetworksetup{}).run, func(o *Options) {
		o.FetchSet = func(_ context.Context, dir, name string, _ int) error {
			fetched++
			if fail {
				return errors.New("no route to host")
			}
			b, err := os.ReadFile(res)
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dir, name+".srs"), b, 0o600)
		}
		o.FetchCatalog = func(context.Context, string, int) ([]string, error) { return []string{"geosite-zzz-found"}, nil }
	})
	e.addAndWait(t, subFile(t, "🇬🇧 London 01", "🇩🇪 Frankfurt 01"), 2)
	if errs := e.ApplyRules(fixtures.AIAccounts()); errs != nil {
		t.Fatal(errs)
	}
	if err := e.NewGroup("AI", "claude", "claude.ai"); err != nil {
		t.Fatal(err)
	}
	if err := e.NewGroup("搜索", "direct", "intranet.example.com"); err != nil {
		t.Fatal(err)
	}
	idle := func() bool { return e.State().Busy == "" }

	// A set that cannot be fetched is not added: its sites would go where the rest go.
	fail = true
	if err := e.AddSet("AI", "geosite-zzz-one"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the failed fetch", func() bool { return idle() && e.State().Err != "" })
	if g := e.State().Groups[0]; len(g.Sets) != 0 {
		t.Errorf("a set that was not fetched is in the group: %v", g.Sets)
	}
	if !strings.Contains(e.State().Err, "没有加入") {
		t.Errorf("the error does not say it was not added: %q", e.State().Err)
	}

	fail = false
	if err := e.AddSet("AI", "geosite-zzz-one"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the set", func() bool { return idle() && len(e.State().Groups[0].Sets) == 1 })
	st := e.State()
	if st.Err != "" {
		t.Errorf("error %q", st.Err)
	}
	found := false
	for _, r := range st.Plan.Rules {
		for _, m := range r.Matchers {
			if m.Kind == "rule_set" && len(m.Values) == 1 && m.Values[0] == "geosite-zzz-one" && r.Target == "route-claude" {
				found = true
			}
		}
	}
	if !found {
		t.Error("the set is not a rule that goes where the group goes")
	}
	if _, err := e.Preview(); err != nil {
		t.Errorf("the config: %v", err)
	}

	// One set is in one group: adding it to another takes it from the first. It is
	// here now, so it is not fetched again.
	before := fetched
	if err := e.AddSet("搜索", "geosite-zzz-one"); err != nil {
		t.Fatal(err)
	}
	g := e.State().Groups
	if len(g[0].Sets) != 0 || len(g[1].Sets) != 1 || fetched != before {
		t.Errorf("groups %+v, fetched %d more times", g, fetched-before)
	}

	if err := e.RemoveSet("搜索", "geosite-zzz-one"); err != nil || len(e.State().Groups[1].Sets) != 0 {
		t.Errorf("remove: %v %+v", err, e.State().Groups)
	}
	for _, bad := range []string{"geoip-cn", "geosite-../x", ""} {
		if err := e.AddSet("AI", bad); err == nil {
			t.Errorf("%q was taken", bad)
		}
	}
	if err := e.AddSet("nope", "geosite-xai"); err == nil {
		t.Error("a group that is not there was taken")
	}

	// The shipped names can be searched at once; the list is fetched once asked for.
	if got := e.SearchSets("gemini"); len(got) == 0 || got[0] != "geosite-google-gemini" {
		t.Errorf("search %v", got)
	}
	e.EnsureCatalog()
	waitFor(t, "the list", func() bool { return len(e.SearchSets("zzz-found")) == 1 })
}

func TestGroupsThatClaimTheSameSitesAreToldApart(t *testing.T) {
	parse := func(js string) *ruleset.Content {
		c, err := ruleset.ParseContent([]byte(js))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	sets := map[string]*ruleset.Content{
		"geosite-a": parse(`{"rules":[{"domain_suffix":["video.example","shared.example"]}]}`),
		"geosite-b": parse(`{"rules":[{"domain_suffix":["shared.example","other.example"]}]}`),
	}
	get := func(n string) *ruleset.Content { return sets[n] }

	gs := []domains.Group{
		{Name: "first", Target: "direct", Sets: []string{"geosite-a"}},
		{Name: "second", Target: "proxy", Domains: []string{"unrelated.example"}, Sets: []string{"geosite-b"}},
		{Name: "third", Target: "direct", Domains: []string{"video.example"}},
	}
	ov := overlapsOf(gs, get)
	if len(ov) != 1 || ov[0].Winner != "first" || ov[0].Other != "second" || ov[0].Count < 2 {
		t.Fatalf("overlaps %+v", ov) // third goes where first does: not an overlap
	}
	if ov[0].Example == "" {
		t.Error("no example")
	}
	gs[0], gs[1] = gs[1], gs[0]
	if ov := overlapsOf(gs, get); len(ov) != 1 || ov[0].Winner != "second" || ov[0].Other != "first" {
		t.Errorf("the group first decides: %+v", ov)
	}
	nested := overlapsOf([]domains.Group{{Name: "p", Target: "direct", Domains: []string{"example.com"}}, {Name: "q", Target: "proxy", Domains: []string{"www.example.com"}}}, get)
	if len(nested) != 1 || nested[0].Winner != "p" || nested[0].Example != "www.example.com" {
		t.Errorf("a site under another is an overlap: %+v", nested)
	}
	if ov := overlapsOf([]domains.Group{{Name: "x", Target: "direct", Sets: []string{"geosite-missing"}}, {Name: "y", Target: "proxy", Domains: []string{"a.com"}}}, get); ov != nil {
		t.Errorf("a set that is not there overlaps nothing: %+v", ov)
	}
}

func TestOverlapsAndHoldersFromRealRuleSets(t *testing.T) {
	e := newEngine(t, (&fakeNetworksetup{}).run)
	if err := e.NewGroup("one", "direct", ""); err != nil {
		t.Fatal(err)
	}
	if err := e.NewGroup("two", "proxy", "ai.google.dev"); err != nil {
		t.Fatal(err)
	}
	if err := e.AddSet("one", "geosite-google-gemini"); err != nil { // shipped: nothing to fetch
		t.Fatal(err)
	}
	waitFor(t, "the overlap", func() bool { return len(e.State().Overlaps) == 1 })
	o := e.State().Overlaps[0]
	if o.Winner != "one" || o.Other != "two" || o.Example != "ai.google.dev" {
		t.Errorf("overlap %+v", o)
	}
	if err := e.MoveGroup("two", -1); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the winner to change", func() bool { o := e.State().Overlaps; return len(o) == 1 && o[0].Winner == "two" })

	var got []string
	waitFor(t, "the sets read", func() bool {
		got = e.SetsHolding("ai.google.dev")
		return len(got) > 0
	})
	has := false
	for _, n := range got {
		has = has || n == "geosite-google-gemini"
	}
	if !has {
		t.Errorf("holders %v", got)
	}
	if got := e.SetsHolding("no-such-site.invalid"); len(got) != 0 {
		t.Errorf("a site in no set: %v", got)
	}
}

func TestARouteIsOpenOnlyWhileItLeavesFromItsExit(t *testing.T) {
	var mu sync.Mutex
	answer, answerErr := "1.2.3.4", error(nil)
	var alerts []string
	e := newEngineWith(t, (&fakeNetworksetup{}).run, func(o *Options) {
		o.ExitIP = func(context.Context, string) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			return answer, answerErr
		}
		o.ExitEvery = time.Hour // only when asked, in this test
		o.Alert = func(title, body string) {
			mu.Lock()
			alerts = append(alerts, title+" "+body)
			mu.Unlock()
		}
	})
	set := func(ip string, err error) {
		mu.Lock()
		answer, answerErr = ip, err
		mu.Unlock()
	}
	told := func() int { mu.Lock(); defer mu.Unlock(); return len(alerts) }
	e.addAndWait(t, subFile(t, "🇬🇧 London 01", "🇬🇧 London 02"), 2)
	if errs := e.ApplyRules("route work region UK exit:198.51.100.0/24\nwork domain:work.example\n"); errs != nil {
		t.Fatal(errs)
	}
	e.Start()
	waitFor(t, "running", func() bool { return e.State().Status == Running })
	now := func(group string) string {
		ctx, cancel := timeout(5 * time.Second)
		defer cancel()
		ps, err := e.core.API().Proxies(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return ps[group].Now
	}
	checked := func() ExitCheck { return e.State().Exits["work"] }

	// It leaves from elsewhere: it stays cut, says where from, and the person is told once.
	waitFor(t, "the first check", func() bool { c := checked(); return !c.At.IsZero() && !c.Checking })
	if c := checked(); c.OK || c.IP != "1.2.3.4" || now("route-work") != rules.GateTag {
		t.Fatalf("check %+v, the route on %q", c, now("route-work"))
	}
	if told() != 1 || !strings.Contains(alerts[0], "1.2.3.4") || !strings.Contains(alerts[0], "198.51.100.0/24") {
		t.Errorf("alerts %v", alerts)
	}

	// It leaves from its range: opened, on its node.
	set("198.51.100.222", nil)
	e.kickExits()
	waitFor(t, "opened", func() bool { return now("route-work") == "🇬🇧 London 01" })
	if c := checked(); !c.OK || c.IP != "198.51.100.222" {
		t.Errorf("check %+v", c)
	}

	// Another node: cut until it is seen to leave from the range, then open on it.
	e.Choose("work", "🇬🇧 London 02")
	waitFor(t, "the new node", func() bool { return now("route-work") == "🇬🇧 London 02" })
	if now(singcfg.CheckTag("route-work")) != "🇬🇧 London 02" {
		t.Error("the check does not go out by the chosen node")
	}

	// It changes to another address while open: cut, and told.
	set("198.51.101.1", nil)
	e.kickExits()
	waitFor(t, "cut", func() bool { return now("route-work") == rules.GateTag })
	waitFor(t, "told", func() bool { return told() == 2 })

	// It cannot be told: cut, and why is kept.
	set("", errors.New("timeout"))
	e.kickExits()
	waitFor(t, "the failed check", func() bool { c := checked(); return c.Err != "" && !c.Checking })
	if now("route-work") != rules.GateTag || checked().OK {
		t.Error("a route whose exit cannot be told is open")
	}

	// Cut by the person, it stays cut whatever the check sees; opened by the
	// person, it is opened by the check alone.
	set("198.51.100.222", nil)
	e.SetGate("work", true)
	e.kickExits()
	waitFor(t, "a check", func() bool { return checked().OK })
	time.Sleep(300 * time.Millisecond)
	if now("route-work") != rules.GateTag {
		t.Error("the check opened a route the person cut")
	}
	set("9.9.9.9", nil)
	e.SetGate("work", false)
	time.Sleep(500 * time.Millisecond)
	if now("route-work") != rules.GateTag {
		t.Error("opening by hand let it out before it was checked")
	}
	set("198.51.100.222", nil)
	e.kickExits()
	waitFor(t, "opened by the check", func() bool { return now("route-work") == "🇬🇧 London 02" })
}

// Each route's exit is checked on its own timer, and only within its hours.
func TestAnExitIsDueByItsOwnTimer(t *testing.T) {
	p, errs := rules.Compile("route a region UK exit:1.2.3.4 check:5m hours:09:00-18:00\nroute b region UK exit:1.2.3.4\n", []string{"UK 1"}, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	a, b := p.Routes[0], p.Routes[1]
	noon := time.Date(2026, 1, 1, 12, 0, 0, 0, time.Local)
	e := &Engine{exits: map[string]ExitCheck{}}
	if !e.due(a, noon) {
		t.Error("never checked, and not due")
	}
	e.exits["a"] = ExitCheck{At: noon}
	e.exits["b"] = ExitCheck{At: noon}
	if e.due(a, noon.Add(4*time.Minute)) || !e.due(a, noon.Add(5*time.Minute)) {
		t.Error("a: not on its 5 minutes")
	}
	if e.due(b, noon.Add(time.Minute)) || !e.due(b, noon.Add(2*time.Minute)) {
		t.Error("b: not on the default 2 minutes")
	}
	e.exits["a"] = ExitCheck{At: noon.Add(7 * time.Hour)}
	if e.due(a, noon.Add(8*time.Hour)) {
		t.Error("a was due at 20:00, outside its hours")
	}
}
