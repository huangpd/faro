package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"faro/internal/ruleset"
)

// fetcher stands in for the mirrors, and records every attempt.
type fetcher struct {
	mu    sync.Mutex
	ports []int
	fail  bool
	res   ruleset.Result
}

func (f *fetcher) fetch(ctx context.Context, dir string, port int) (ruleset.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ports = append(f.ports, port)
	if f.fail {
		return ruleset.Result{}, errors.New("no route to the mirror")
	}
	return f.res, nil
}

func (f *fetcher) calls() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.ports...)
}

// engineWith opens an engine whose rule sets are as old as given, and whose
// automatic look comes quickly.
func engineWith(t *testing.T, f *fetcher, age time.Duration, opts func(*Options)) *Engine {
	t.Helper()
	bin, _ := filepath.Abs("../../bin/sing-box")
	if _, err := os.Stat(bin); err != nil {
		t.Skip("no bin/sing-box")
	}
	t.Setenv("FARO_CORE", bin)
	t.Setenv("HOME", t.TempDir())
	// A data directory whose rule sets have the given age. They are real files: the
	// core reads them when it runs, and a made-up one would stop it.
	dir := filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "Faro", "rulesets")
	os.MkdirAll(dir, 0o700)
	when := time.Now().Add(-age)
	for _, n := range []string{"geosite-cn", "geoip-cn"} {
		p := filepath.Join(dir, n+".srs")
		j := filepath.Join(t.TempDir(), n+".json")
		os.WriteFile(j, []byte(`{"version":3,"rules":[{"domain_suffix":["example.invalid"]}]}`), 0o600)
		if out, err := exec.Command(bin, "rule-set", "compile", j, "-o", p).CombinedOutput(); err != nil {
			t.Fatalf("compile %s: %v %s", n, err, out)
		}
		os.Chtimes(p, when, when)
	}
	o := Options{
		ProxyRunner: (&fakeNetworksetup{}).run, FetchRuleSets: f.fetch,
		RuleSetsFirst: 30 * time.Millisecond, RuleSetsInterval: 20 * time.Millisecond, // it looks often
		RuleSetsRetry: 80 * time.Millisecond, // and does not try again at every look
	}
	if opts != nil {
		opts(&o)
	}
	e, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e
}

func TestStaleRuleSetsAreFetchedByThemselves(t *testing.T) {
	f := &fetcher{res: ruleset.Result{Fetched: 7, Changed: 3}}
	e := engineWith(t, f, 3*24*time.Hour, nil)
	before := e.State().RuleSetsChecked
	if time.Since(before) < 2*24*time.Hour {
		t.Fatalf("the sets should look three days old, not %v", time.Since(before))
	}
	waitFor(t, "an automatic fetch", func() bool { return len(f.calls()) >= 1 })
	waitFor(t, "it to be noted", func() bool { return time.Since(e.State().RuleSetsChecked) < time.Minute })
	// Nobody asked, so nothing is shown: no error, no busy line.
	if st := e.State(); st.Err != "" || st.Busy != "" {
		t.Errorf("an automatic update showed itself: err %q busy %q", st.Err, st.Busy)
	}
	// Stopped, so it went direct.
	if got := f.calls()[0]; got != 0 {
		t.Errorf("went through port %d with no core running", got)
	}
	// Done: no more, however often it looks.
	n := len(f.calls())
	time.Sleep(400 * time.Millisecond)
	if got := len(f.calls()); got != n {
		t.Errorf("%d more fetches after it was current", got-n)
	}
	// The time survives a restart of the app.
	e2, err := New(Options{ProxyRunner: (&fakeNetworksetup{}).run, FetchRuleSets: f.fetch})
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	if time.Since(e2.State().RuleSetsChecked) > time.Minute {
		t.Errorf("the last check was forgotten: %v ago", time.Since(e2.State().RuleSetsChecked))
	}
}

func TestFreshRuleSetsAreLeftAlone(t *testing.T) {
	f := &fetcher{}
	engineWith(t, f, 2*time.Hour, nil)
	time.Sleep(500 * time.Millisecond) // several looks
	if n := len(f.calls()); n != 0 {
		t.Errorf("%d fetches of rule sets two hours old", n)
	}
}

func TestAFailureIsQuietAndNotRepeatedEveryLook(t *testing.T) {
	f := &fetcher{fail: true}
	// It looks every 20ms, and may try again only every 400ms: the gap is what limits it.
	e := engineWith(t, f, 3*24*time.Hour, func(o *Options) { o.RuleSetsInterval = 20 * time.Millisecond; o.RuleSetsRetry = 400 * time.Millisecond })
	waitFor(t, "the first attempt", func() bool { return len(f.calls()) >= 1 })
	if st := e.State(); st.Err != "" {
		t.Errorf("a failed automatic update showed an error: %q", st.Err)
	}
	if time.Since(e.State().RuleSetsChecked) < 2*24*time.Hour {
		t.Error("a failed fetch was noted as a check")
	}
	// Over 1.1 seconds, looking 55 times, it may try about 3 times, not 55.
	time.Sleep(1100 * time.Millisecond)
	if n := len(f.calls()); n < 2 || n > 5 {
		t.Errorf("%d attempts in 1.1s with a 400ms gap (looked about 55 times)", n)
	}
	// And once the mirror answers, it takes it.
	f.mu.Lock()
	f.fail = false
	f.mu.Unlock()
	waitFor(t, "the late success", func() bool { return time.Since(e.State().RuleSetsChecked) < time.Minute })
}

// The sites may not be reachable directly, so an attempt made with no core is
// made again at once when the core is running, through it.
func TestGoesThroughTheCoreOnceItRuns(t *testing.T) {
	f := &fetcher{fail: true}
	e := engineWith(t, f, 3*24*time.Hour, func(o *Options) {
		o.RuleSetsFirst = 30 * time.Millisecond
		o.RuleSetsRetry = time.Hour // the gate: nothing else retries for an hour
	})
	waitFor(t, "the direct attempt", func() bool { return len(f.calls()) == 1 })
	if f.calls()[0] != 0 {
		t.Fatalf("first attempt through port %d", f.calls()[0])
	}
	e.Start()
	waitFor(t, "running", func() bool { return e.State().Status == Running })
	waitFor(t, "the attempt through the core", func() bool { return len(f.calls()) >= 2 })
	if got, want := f.calls()[1], e.State().MixedPort; got != want {
		t.Errorf("the second attempt went through port %d, want the core's %d", got, want)
	}
	// Failed again through the core: not hammered.
	time.Sleep(300 * time.Millisecond)
	if n := len(f.calls()); n != 2 {
		t.Errorf("%d attempts, want 2 (direct, then through the core)", n)
	}
}

// An update that finds new content must not restart the core: the core loads a
// replaced rule set file by itself, and a restart would drop every connection.
func TestAnUpdateDoesNotRestartTheCore(t *testing.T) {
	f := &fetcher{res: ruleset.Result{Fetched: 7, Changed: 7}}
	e := engineWith(t, f, 3*24*time.Hour, func(o *Options) { o.RuleSetsFirst = time.Hour }) // not yet
	e.Start()
	waitFor(t, "running", func() bool { return e.State().Status == Running })
	e.mu.Lock()
	gen := e.gen
	e.mu.Unlock()
	// The core's own start also looked; wait until that attempt is done.
	waitFor(t, "the fetch", func() bool { return len(f.calls()) >= 1 })
	time.Sleep(200 * time.Millisecond)

	e.UpdateRuleSets() // and by hand, too
	waitFor(t, "the manual update", func() bool { return e.State().Busy == "" && len(f.calls()) >= 2 })
	time.Sleep(200 * time.Millisecond)
	e.mu.Lock()
	after, status := e.gen, e.status
	e.mu.Unlock()
	if after != gen || status != Running {
		t.Errorf("the core was restarted by a rule set update: generation %d -> %d, %s", gen, after, status)
	}
}

func TestManualUpdateShowsItsFailureAndTheTime(t *testing.T) {
	f := &fetcher{fail: true}
	e := engineWith(t, f, 2*time.Hour, nil)
	e.UpdateRuleSets()
	waitFor(t, "the error", func() bool { return e.State().Err != "" && e.State().Busy == "" })
	if !strings.Contains(e.State().Err, "no route to the mirror") {
		t.Errorf("error %q", e.State().Err)
	}
	f.mu.Lock()
	f.fail = false
	f.mu.Unlock()
	e.UpdateRuleSets()
	waitFor(t, "the update", func() bool { return e.State().Busy == "" && time.Since(e.State().RuleSetsChecked) < time.Minute })
	if e.State().Err != "" {
		t.Errorf("the error stayed: %q", e.State().Err)
	}
}

func TestClosingStopsTheLooking(t *testing.T) {
	f := &fetcher{fail: true}
	e := engineWith(t, f, 3*24*time.Hour, func(o *Options) { o.RuleSetsInterval = 20 * time.Millisecond })
	waitFor(t, "an attempt", func() bool { return len(f.calls()) >= 1 })
	e.Close()
	time.Sleep(100 * time.Millisecond)
	n := len(f.calls())
	time.Sleep(300 * time.Millisecond)
	if got := len(f.calls()); got != n {
		t.Errorf("%d attempts after Close", got-n)
	}
}

// The reason no restart is needed: the core takes a replaced rule set file by
// itself. This is the real core, with a rule that blocks what a set names, and
// a file replaced the way an update replaces it (a rename), with no restart.
func TestTheCoreLoadsAReplacedRuleSetByItself(t *testing.T) {
	bin, _ := filepath.Abs("../../bin/sing-box")
	if _, err := os.Stat(bin); err != nil {
		t.Skip("no bin/sing-box")
	}
	dir := t.TempDir()
	compile := func(name, domain string) string {
		j := filepath.Join(dir, name+".json")
		os.WriteFile(j, []byte(fmt.Sprintf(`{"version":3,"rules":[{"domain_suffix":["%s"]}]}`, domain)), 0o600)
		out := filepath.Join(dir, name+".srs")
		if b, err := exec.Command(bin, "rule-set", "compile", j, "-o", out).CombinedOutput(); err != nil {
			t.Fatalf("compile: %v %s", err, b)
		}
		return out
	}
	a, b := compile("a", "example.net"), compile("b", "example.org")
	live := filepath.Join(dir, "live.srs")
	data, _ := os.ReadFile(a)
	os.WriteFile(live, data, 0o600)

	port := freePort(t)
	cfg := fmt.Sprintf(`{"log":{"level":"error"},"dns":{"servers":[{"type":"local","tag":"l"}]},
 "inbounds":[{"type":"mixed","tag":"in","listen":"127.0.0.1","listen_port":%d}],
 "outbounds":[{"type":"direct","tag":"direct"}],
 "route":{"default_domain_resolver":"l","rule_set":[{"type":"local","tag":"s","format":"binary","path":%q}],
  "rules":[{"rule_set":["s"],"action":"reject"}],"final":"direct"}}`, port, live)
	cp := filepath.Join(dir, "cfg.json")
	os.WriteFile(cp, []byte(cfg), 0o600)
	cmd := exec.Command(bin, "run", "-c", cp)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	waitFor(t, "the core", func() bool { return portOpen(port) })

	blocked := func(host string) bool {
		c, err := dial(port)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		fmt.Fprintf(c, "CONNECT %s:443 HTTP/1.1\r\nHost: %s:443\r\n\r\n", host, host)
		c.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
		buf := make([]byte, 256)
		c.Read(buf) // the 200, sent before the route is chosen
		_, err = c.Read(buf)
		var ne interface{ Timeout() bool }
		return err != nil && !(errors.As(err, &ne) && ne.Timeout()) // closed or reset: rejected
	}
	if !blocked("example.net") || blocked("example.org") {
		t.Fatal("the set A is not in force at the start")
	}
	// Replaced by a rename, as an update does.
	data, _ = os.ReadFile(b)
	os.WriteFile(live+".tmp", data, 0o600)
	os.Rename(live+".tmp", live)
	waitFor(t, "the new set to take effect with no restart", func() bool { return blocked("example.org") && !blocked("example.net") })
	_ = strconv.Itoa
}
