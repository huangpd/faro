package engine

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"faro/internal/sub"
)

// prober stands in for the core's delay test: each node answers as told, after a
// delay, and every question is recorded. What is tested here is how the engine
// keeps the tests (which node, shown as testing, the answer kept, no two at once,
// nothing from a core that is gone), not the core's own timing, which the real
// core tests cover (TestNodesSelectAndDelay).
type prober struct {
	mu      sync.Mutex
	answers map[string]int // ms; -1 an error
	wait    map[string]time.Duration
	asked   map[string]int
	gate    chan struct{} // when set, an answer waits for it
}

func newProber() *prober {
	return &prober{answers: map[string]int{}, wait: map[string]time.Duration{}, asked: map[string]int{}}
}

func (p *prober) probe(ctx context.Context, tag string) (int, error) {
	p.mu.Lock()
	p.asked[tag]++
	ans, ok := p.answers[tag]
	w := p.wait[tag]
	gate := p.gate
	p.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	time.Sleep(w)
	if !ok || ans < 0 {
		return 0, errors.New("unreachable")
	}
	return ans, nil
}

// rtt answers the round trip to the node's server: as told, at once, and not counted.
func (p *prober) rtt(_ context.Context, tag string) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ans, ok := p.answers[tag]
	if !ok || ans < 0 {
		return 0, errors.New("unreachable")
	}
	return ans, nil
}

func (p *prober) count(tag string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.asked[tag]
}

// engineWithNodes is a running engine with the named nodes, whose tests are answered by p.
func engineWithNodes(t *testing.T, p *prober, names ...string) *Engine {
	t.Helper()
	e := engineWith(t, &fetcher{}, 2*time.Hour, func(o *Options) {
		o.RuleSetsFirst = time.Hour
		o.Probe = p.probe
		o.RTT = p.rtt
	})
	var ns []sub.Node
	for _, n := range names {
		ns = append(ns, sub.Node{Name: n, Out: map[string]any{"type": "shadowsocks", "server": "h.example.com", "server_port": 1, "method": "aes-256-gcm", "password": "p"}})
	}
	e.store.Subs = []*sub.Subscription{{ID: "t", Name: "t", Nodes: ns}}
	e.mu.Lock()
	e.reloadLocked()
	e.mu.Unlock()
	return e
}

func TestTestingOneNode(t *testing.T) {
	p := newProber()
	p.answers = map[string]int{"日本-快": 80, "日本-慢": 420, "日本-死": -1}
	p.wait["日本-慢"] = 150 * time.Millisecond
	e := engineWithNodes(t, p, "日本-快", "日本-慢", "日本-死")

	// Stopped: the node is not loaded, there is nothing to ask. Nothing happens.
	e.TestNode("日本-快")
	time.Sleep(80 * time.Millisecond)
	if len(e.State().Delays) != 0 || len(e.State().Testing) != 0 || p.count("日本-快") != 0 {
		t.Fatal("a node was tested with no core running")
	}

	e.Start()
	waitFor(t, "running", func() bool { return e.State().Status == Running })

	// One node: only that one is asked, and it is shown as under test while it is.
	e.TestNode("日本-慢")
	if !e.State().Testing["日本-慢"] {
		t.Error("the node is not shown as being tested")
	}
	waitFor(t, "the answer", func() bool { _, ok := e.State().Delays["日本-慢"]; return ok })
	st := e.State()
	if st.Delays["日本-慢"] != 420 || st.Testing["日本-慢"] {
		t.Errorf("delay %d, testing %v", st.Delays["日本-慢"], st.Testing["日本-慢"])
	}
	if len(st.Delays) != 1 || p.count("日本-快") != 0 || p.count("日本-死") != 0 {
		t.Errorf("other nodes were tested: delays %v", st.Delays)
	}

	// A node that cannot be reached is a failure (-1), shown, not a hang.
	e.TestNode("日本-死")
	waitFor(t, "the dead node", func() bool { _, ok := e.State().Delays["日本-死"]; return ok })
	if e.State().Delays["日本-死"] != -1 {
		t.Errorf("a node that cannot be reached: %d", e.State().Delays["日本-死"])
	}

	// Again: the old answer goes while it is asked again, and the new one replaces it.
	p.mu.Lock()
	p.answers["日本-慢"] = 90
	p.mu.Unlock()
	e.TestNode("日本-慢")
	if _, ok := e.State().Delays["日本-慢"]; ok {
		t.Error("the old answer stays while the node is tested again")
	}
	waitFor(t, "the new answer", func() bool { return e.State().Delays["日本-慢"] == 90 })

	// Not a node: nothing, and nothing is asked.
	e.TestNode("没有这个节点")
	e.TestNode("")
	time.Sleep(50 * time.Millisecond)
	if p.count("没有这个节点") != 0 || p.count("") != 0 || len(e.State().Testing) != 0 {
		t.Error("an unknown node was tested")
	}
}

// Clicking a node again while it is still being tested must not start a second test.
func TestAClickDuringATestIsIgnored(t *testing.T) {
	p := newProber()
	p.answers = map[string]int{"a": 100}
	p.gate = make(chan struct{})
	e := engineWithNodes(t, p, "a")
	e.Start()
	waitFor(t, "running", func() bool { return e.State().Status == Running })

	e.TestNode("a")
	waitFor(t, "the question", func() bool { return p.count("a") == 1 })
	for i := 0; i < 5; i++ {
		e.TestNode("a") // the person clicks again, impatient
	}
	time.Sleep(60 * time.Millisecond)
	if n := p.count("a"); n != 1 {
		t.Errorf("asked %d times while a test was going, want 1", n)
	}
	close(p.gate)
	waitFor(t, "the answer", func() bool { return e.State().Delays["a"] == 100 })
	// Done: it can be tested again.
	p.mu.Lock()
	p.gate = nil
	p.mu.Unlock()
	e.TestNode("a")
	waitFor(t, "the second test", func() bool { return p.count("a") == 2 })
}

// An answer that comes after the core was stopped is about a core that is gone.
func TestAnAnswerFromAGoneCoreIsDropped(t *testing.T) {
	p := newProber()
	p.answers = map[string]int{"a": 100}
	p.wait["a"] = 700 * time.Millisecond
	e := engineWithNodes(t, p, "a")
	e.Start()
	waitFor(t, "running", func() bool { return e.State().Status == Running })
	e.TestNode("a")
	waitFor(t, "the question", func() bool { return p.count("a") == 1 })
	e.Stop()
	waitFor(t, "stopped", func() bool { return e.State().Status == Stopped })
	time.Sleep(1200 * time.Millisecond) // the answer is in by now
	if d, ok := e.State().Delays["a"]; ok {
		t.Errorf("a gone core's answer was kept: %d ms", d)
	}
	if len(e.State().Testing) != 0 {
		t.Error("still shown as being tested")
	}
}

// Testing all at once and one by one use the same record.
func TestTestAllAndOneShareTheRecord(t *testing.T) {
	p := newProber()
	p.answers = map[string]int{"a": 10, "b": 20, "c": 30}
	e := engineWithNodes(t, p, "a", "b", "c")
	e.Start()
	waitFor(t, "running", func() bool { return e.State().Status == Running })
	e.TestAll()
	waitFor(t, "all three", func() bool { return len(e.State().Delays) == 3 })
	if d := e.State().Delays; d["a"] != 10 || d["b"] != 20 || d["c"] != 30 {
		t.Errorf("%v", d)
	}
	p.mu.Lock()
	p.answers["b"] = 25
	p.mu.Unlock()
	e.TestNode("b")
	waitFor(t, "b again", func() bool { return e.State().Delays["b"] == 25 })
	if d := e.State().Delays; d["a"] != 10 || d["c"] != 30 {
		t.Errorf("testing one node changed the others: %v", d)
	}
}

func TestADistanceAndWhetherItWorksAreKeptApart(t *testing.T) {
	var rtt, probe struct {
		ms  int
		err error
	}
	e := engineWith(t, &fetcher{}, 2*time.Hour, func(o *Options) {
		o.RuleSetsFirst = time.Hour
		o.RTT = func(context.Context, string) (int, error) { return rtt.ms, rtt.err }
		o.Probe = func(context.Context, string) (int, error) { return probe.ms, probe.err }
	})
	e.store.Subs = []*sub.Subscription{{ID: "t", Name: "t", Nodes: []sub.Node{{Name: "n", Out: map[string]any{"type": "shadowsocks", "server": "h.example.com", "server_port": 1, "method": "aes-256-gcm", "password": "p"}}}}}
	e.mu.Lock()
	e.reloadLocked()
	e.status = Running
	e.mu.Unlock()
	ask := func() (int, bool) {
		e.TestNode("n")
		waitFor(t, "the answer", func() bool { _, ok := e.State().Delays["n"]; return ok && !e.State().Testing["n"] })
		st := e.State()
		return st.Delays["n"], st.Works["n"]
	}
	no := errors.New("no")

	rtt.ms, probe.ms = 200, 420 // the round trip is the delay; the request's time is not
	if d, w := ask(); d != 200 || !w {
		t.Errorf("delay %d, works %v: want 200 and true", d, w)
	}
	probe.ms, probe.err = 0, no // the server answers, but nothing goes through it
	if d, w := ask(); d != 200 || w {
		t.Errorf("delay %d, works %v: a server that answers is not a node that works", d, w)
	}
	rtt.ms, rtt.err, probe.ms, probe.err = 0, no, 390, nil // cannot be timed from here, yet it carries
	if d, w := ask(); d != 390 || !w {
		t.Errorf("delay %d, works %v: a node that works is not shown as no answer", d, w)
	}
	probe.ms, probe.err = 0, no // neither
	if d, w := ask(); d != -1 || w {
		t.Errorf("delay %d, works %v: want -1 and false", d, w)
	}
	if _, ok := e.State().Works["n"]; !ok {
		t.Error("a failed test is not recorded as one")
	}
}

func TestTheRoundTripToAServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	ms, err := dialRTT(context.Background(), "127.0.0.1", port)
	if err != nil || ms < 1 || ms > 500 {
		t.Errorf("%d ms, %v", ms, err)
	}
	if ms, err := dialRTT(context.Background(), "localhost", port); err != nil || ms < 1 {
		t.Errorf("by name: %d ms, %v", ms, err)
	}
	ln.Close()
	if ms, err := dialRTT(context.Background(), "127.0.0.1", port); err == nil {
		t.Errorf("a closed port answered in %d ms", ms)
	}
	if _, err := dialRTT(context.Background(), "no-such-host.invalid", 443); err == nil {
		t.Error("a name that is not there was timed")
	}

	a := addrOf("trojan", map[string]any{"server": "h.example", "server_port": 443})
	b := addrOf("hysteria2", map[string]any{"server": "h.example", "server_port": float64(8443)})
	if a.host != "h.example" || a.port != 443 || a.udp || b.port != 8443 || !b.udp {
		t.Errorf("%+v %+v", a, b)
	}
}
