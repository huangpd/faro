package engine

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"faro/internal/rules"
	"faro/internal/singcfg"
)

// A route may say where it is allowed to leave from (exit:<range>). While the
// core runs, the address the world sees of the route is asked for, through an
// entrance of the route's own that goes out by its node even while it is cut,
// at the start, when its node is changed, and on its own timer. The route
// starts cut; it is opened when it is seen to leave from one of its ranges, and
// cut again when it leaves from another, or cannot be told. A route the person
// cut stays cut.

// ExitCheck is what was last seen of a route's exit.
type ExitCheck struct {
	IP       string    // the address it left from; "" when that could not be told
	OK       bool      // that address is one the route may leave from
	Err      string    // why it could not be told
	At       time.Time // when it was asked
	Checking bool
}

func copyExits(m map[string]ExitCheck) map[string]ExitCheck {
	out := make(map[string]ExitCheck, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// kickExits asks for the checks to be done again now.
func (e *Engine) kickExits() {
	select {
	case e.exitKick <- struct{}{}:
	default: // one is asked for already
	}
}

// exitTick is how often the watcher looks at which exits are due.
const exitTick = 5 * time.Second

// watchExits checks the run's routes now, then each on its own timer (check:
// and hours: on its route line), and all of them when asked, until the run ends.
func (e *Engine) watchExits(ctx context.Context, g int) {
	all := true
	for {
		if !e.current(g) {
			return
		}
		e.checkExits(ctx, g, all)
		tick := exitTick
		if e.opts.ExitEvery != 0 && e.opts.ExitEvery < tick {
			tick = e.opts.ExitEvery
		}
		t := time.NewTimer(tick)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-e.quit:
			t.Stop()
			return
		case <-t.C:
			all = false
		case <-e.exitKick:
			t.Stop()
			all = true // asked for: the start, a new node; always, whatever the hours
		}
	}
}

// due says whether a route's exit is to be checked now: never checked, or its
// time has come within its hours.
func (e *Engine) due(r rules.Route, now time.Time) bool {
	last := e.exits[r.ID].At
	if last.IsZero() {
		return true
	}
	every := r.Every()
	if e.opts.ExitEvery != 0 {
		every = e.opts.ExitEvery
	}
	return now.Sub(last) >= every && r.InHours(now)
}

// checkExits asks the checked routes that are due (all of them, with all) where
// they leave from, and opens or cuts each.
func (e *Engine) checkExits(ctx context.Context, g int, all bool) {
	e.mu.Lock()
	var todo []rules.Route
	now := e.opts.Now()
	if e.plan != nil {
		for _, r := range e.plan.Routes {
			if len(r.Exit) > 0 && len(r.Nodes) > 0 && (all || e.due(r, now)) {
				todo = append(todo, r)
				c := e.exits[r.ID]
				c.Checking = true
				e.exits[r.ID] = c
			}
		}
	}
	e.mu.Unlock()
	if len(todo) == 0 {
		return
	}
	e.changed()
	for _, r := range todo {
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		ip, err := e.exitIP(cctx, r.Group())
		cancel()
		res := ExitCheck{IP: ip, At: e.opts.Now()}
		if err != nil {
			res.Err = err.Error()
		} else {
			res.OK = r.ExitAllows(ip)
		}
		e.settleExit(g, r, res)
	}
}

// settleExit keeps what was seen, and opens or cuts the route by it.
func (e *Engine) settleExit(g int, r rules.Route, res ExitCheck) {
	e.mu.Lock()
	if e.gen != g {
		e.mu.Unlock()
		return
	}
	e.exits[r.ID] = res
	// The route as it is now: the person may have cut it, or chosen another node,
	// while it was being asked about.
	var cur *rules.Route
	if e.plan != nil {
		for i := range e.plan.Routes {
			if e.plan.Routes[i].ID == r.ID {
				cur = &e.plan.Routes[i]
			}
		}
	}
	wasOpen := e.exitOpen[r.ID]
	var sel string
	switch {
	case cur == nil:
	case res.OK && !cur.Closed:
		if !wasOpen {
			sel = nodeOf(*cur)
		}
		e.exitOpen[r.ID] = true
	case !res.OK:
		sel = rules.GateTag // cut, whether it was open or not: it may be held open by nothing else
		e.exitOpen[r.ID] = false
	}
	first := !wasOpen && !e.alerted[r.ID]
	if !res.OK {
		e.alerted[r.ID] = true
	} else {
		delete(e.alerted, r.ID)
	}
	e.mu.Unlock()
	e.changed()
	if sel != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := e.core.API().Select(ctx, r.Group(), sel); err != nil {
			e.fail(err)
		}
		cancel()
	}
	// Told once when a route is cut by its check: when it was open, or at the
	// first check of all.
	if !res.OK && (wasOpen || first) && e.opts.Alert != nil {
		body := fmt.Sprintf("出口 %s 不在 %s 内", res.IP, strings.Join(r.Exit, "、"))
		if res.Err != "" {
			body = "无法确认出口：" + res.Err
		}
		e.opts.Alert(r.ID+" 已切断", body)
	}
}

// exitIP asks where a route group leaves from: through the hook in tests, else
// Cloudflare's trace, through the group's check entrance.
func (e *Engine) exitIP(ctx context.Context, group string) (string, error) {
	if e.opts.ExitIP != nil {
		return e.opts.ExitIP(ctx, group)
	}
	port := e.core.CheckPort(group)
	if port == 0 {
		return "", fmt.Errorf("no check entrance")
	}
	proxy, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, singcfg.TraceURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s", resp.Status)
	}
	return traceIP(io.LimitReader(resp.Body, 4096))
}

// traceIP reads the address out of Cloudflare's trace: lines of key=value.
func traceIP(r io.Reader) (string, error) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "ip="); ok && v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("the answer had no address")
}
