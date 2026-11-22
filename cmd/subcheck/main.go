// Command subcheck reads a subscription on this machine and prints what
// Faro makes of it: the format, the nodes' names and protocols, which
// country each is taken to be in, and how the default routes would use them.
//
// It prints no server address, port, password or id, so its output can be
// shared. The URL, or the path of a subscription file, is read from the
// argument or FARO_SUB_URL.
//
//	FARO_SUB_URL='https://…' go run ./cmd/subcheck
//	FARO_PROBE=1 …    ask the server in several ways, when it answers 403
//	FARO_LIVE=1 …     also run the core on these nodes and time each one
//	FARO_RULES=file …  rules to try with these nodes (default: the ai-accounts test setup)
package main

import (
	"context"
	"faro/internal/fixtures"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"faro/internal/core"
	"faro/internal/rules"
	"faro/internal/ruleset"
	"faro/internal/singcfg"
	"faro/internal/sub"
)

func main() {
	url := os.Getenv("FARO_SUB_URL")
	if len(os.Args) > 1 {
		url = os.Args[1]
	}
	if url == "" {
		fmt.Fprintln(os.Stderr, "usage: FARO_SUB_URL=<url> go run ./cmd/subcheck")
		os.Exit(2)
	}
	if os.Getenv("FARO_PROBE") != "" {
		probe(url)
		return
	}
	var body []byte
	var hdr http.Header
	var err error
	if path, ok := sub.LocalPath(url); ok {
		body, err = os.ReadFile(path)
		hdr = http.Header{}
	} else {
		var ua string
		body, hdr, ua, err = sub.FetchAny(context.Background(), url)
		if err == nil {
			fmt.Println("the server answered the client name:", ua)
		}
	}
	if err != nil {
		// The URL holds a token: say what failed, not where.
		fmt.Fprintln(os.Stderr, "fetch failed:", strings.ReplaceAll(err.Error(), url, "<url>"))
		os.Exit(1)
	}
	format, skipped := sub.Describe(body)
	nodes, err := sub.Parse(body)
	fmt.Printf("format: %s, %d bytes\n", format, len(body))
	if info := hdr.Get("Subscription-Userinfo"); info != "" {
		fmt.Println("has usage info: yes")
	}
	if len(skipped) > 0 {
		fmt.Printf("left out (unsupported): %v\n", skipped)
	}
	if err != nil {
		fmt.Println("parse:", err)
		os.Exit(1)
	}

	byType := map[string]int{}
	var tags []string
	fmt.Printf("\n%d nodes:\n", len(nodes))
	fmt.Printf("  %-6s %-12s %-7s %s\n", "region", "type", "notice", "name")
	for _, n := range nodes {
		t, _ := n.Out["type"].(string)
		byType[t]++
		tags = append(tags, n.Name)
		reg := "-"
		var in []string
		for _, id := range rules.RegionIDs() {
			if rules.InRegion(n.Name, id) {
				in = append(in, id)
			}
		}
		if len(in) > 0 {
			reg = strings.Join(in, "+") // more than one is a name that says two countries
		}
		notice := ""
		if rules.IsNotice(n.Name) {
			notice = "yes"
		}
		fmt.Printf("  %-6s %-12s %-7s %s\n", reg, t, notice, n.Name)
	}
	var types []string
	for t, n := range byType {
		types = append(types, fmt.Sprintf("%s×%d", t, n))
	}
	sort.Strings(types)
	fmt.Println("\nprotocols:", strings.Join(types, " "))

	text := fixtures.AIAccounts()
	if f := os.Getenv("FARO_RULES"); f != "" { // a rules file of one's own
		b, err := os.ReadFile(f)
		if err != nil {
			fmt.Println("rules:", err)
			return
		}
		text = string(b)
	}
	plan, errs := rules.Compile(text, tags, nil)
	if errs != nil {
		fmt.Println("default rules:", errs)
		return
	}
	fmt.Println("\nthe routes of the rules with these nodes:")
	for _, r := range plan.Routes {
		on := r.Chosen
		if on == "" && len(r.Nodes) > 0 {
			on = "(fastest)"
		}
		fmt.Printf("  %-8s %d nodes", r.ID, len(r.Nodes))
		if len(r.Nodes) > 0 {
			fmt.Printf("  starts on: %s", on)
		}
		fmt.Println()
		if r.Kind != "like" {
			for _, n := range r.Nodes {
				fmt.Printf("      · %s\n", n)
			}
		}
	}
	for _, w := range plan.Warnings {
		fmt.Println("  ⚠", w)
	}
	unmatched := 0
	matched := map[string]bool{}
	for _, r := range plan.Routes {
		for _, n := range r.Nodes {
			matched[n] = true
		}
	}
	for _, t := range tags {
		if !matched[t] && !rules.IsNotice(t) {
			unmatched++
		}
	}
	fmt.Printf("\nnodes in no route (they stay available in the main group): %d\n", unmatched)

	checkConfig(nodes, tags, plan)
}

// secrets collects what must not be printed: the values of the fields that
// identify a server or authenticate to it.
func secrets(nodes []sub.Node) []string {
	keys := map[string]bool{"server": true, "uuid": true, "password": true, "public_key": true, "short_id": true,
		"username": true, "id": true, "auth_str": true, "server_name": true, "host": true, "service_name": true, "path": true}
	var out []string
	var walk func(k string, v any)
	walk = func(k string, v any) {
		switch x := v.(type) {
		case string:
			if keys[k] && len(x) >= 4 {
				out = append(out, x)
			}
		case map[string]any:
			for kk, vv := range x {
				walk(kk, vv)
			}
		case []any:
			for _, vv := range x {
				walk(k, vv)
			}
		}
	}
	for _, n := range nodes {
		for k, v := range n.Out {
			walk(k, v)
		}
	}
	// Longest first, so a value holding another is not left half showing.
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

func redact(text string, secret []string) string {
	for _, v := range secret {
		text = strings.ReplaceAll(text, v, "<redacted>")
	}
	return text
}

var addrRe = regexp.MustCompile(`(\d{1,3}\.){3}\d{1,3}|\[?[0-9a-fA-F]{0,4}(:[0-9a-fA-F]{0,4}){3,}\]?`)

// printWhy shows the kinds of error the core logged while the nodes were
// timed, each once, with times, addresses and names of servers taken out.
func printWhy(logs []string, secret []string) {
	seen := map[string]int{}
	var order []string
	for _, l := range logs {
		if !strings.Contains(l, "ERROR") && !strings.Contains(l, "WARN") {
			continue
		}
		l = redact(addrRe.ReplaceAllString(l, "<addr>"), secret)
		if i := strings.Index(l, "ERROR"); i >= 0 {
			l = l[i:]
		} else if i := strings.Index(l, "WARN"); i >= 0 {
			l = l[i:]
		}
		l = regexp.MustCompile(`\[\d+ \d+ms\]|\[\d+\]`).ReplaceAllString(l, "")
		if seen[l] == 0 {
			order = append(order, l)
		}
		seen[l]++
	}
	if len(order) == 0 {
		return
	}
	fmt.Println("\nwhat the core logged (each kind once):")
	for i, l := range order {
		if i == 12 {
			fmt.Printf("  … and %d more kinds\n", len(order)-i)
			break
		}
		fmt.Printf("  %s  (×%d)\n", strings.TrimSpace(l), seen[l])
	}
}

// checkConfig makes the config Faro would run with these nodes and the
// default rules, and has the real core check it; with FARO_LIVE it also
// runs it and times every node.
func checkConfig(nodes []sub.Node, tags []string, plan *rules.Plan) {
	fmt.Println("\n--- the core's own check of the config made from these nodes")
	home, err := os.MkdirTemp("", "subcheck")
	if err != nil {
		fmt.Println("no temp dir:", err)
		return
	}
	defer os.RemoveAll(home)
	os.Setenv("HOME", home) // Faro's data directory goes here, not into the real one

	c, err := core.New("", nil)
	if err != nil {
		fmt.Println("skipped:", err)
		return
	}
	setsDir := filepath.Join(c.Dir(), "rulesets")
	if wd, err := os.Getwd(); err == nil {
		_ = ruleset.Install(filepath.Join(wd, "resources"), setsDir)
	}
	st := &sub.Store{Subs: []*sub.Subscription{{Nodes: nodes}}}
	in := singcfg.Input{Nodes: st.Outbounds(), Plan: plan, Sets: ruleset.Paths(setsDir)}
	secret := secrets(nodes)

	if err := c.Start(in, 0); err != nil {
		fmt.Println("the core REFUSES this config:")
		fmt.Println("  " + redact(err.Error(), secret))
		return
	}
	defer c.Stop()
	fmt.Println("config accepted by sing-box", "(", len(in.Nodes), "nodes,", len(in.Sets), "rule sets )")
	for _, n := range c.Notes() {
		fmt.Println("  note:", n)
	}
	if os.Getenv("FARO_LIVE") == "" {
		fmt.Println("(add FARO_LIVE=1 to also time each node)")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := c.WaitReady(ctx); err != nil {
		fmt.Println("the core did not come up:", redact(err.Error(), secret))
		for _, l := range c.Logs() {
			fmt.Println("  ", redact(l, secret))
		}
		return
	}
	fmt.Println("\n--- time to each node (a request to", singcfg.ProbeURL+")")
	type res struct {
		tag string
		ms  int
		err error
	}
	results := make([]res, len(in.Nodes))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	api := c.API()
	for i, o := range in.Nodes {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			cctx, ccancel := context.WithTimeout(ctx, 8*time.Second)
			defer ccancel()
			ms, err := api.Delay(cctx, o.Tag, singcfg.ProbeURL)
			results[i] = res{o.Tag, ms, err}
		}()
	}
	wg.Wait()
	ok := 0
	for _, r := range results {
		if r.err == nil && r.ms > 0 {
			ok++
			fmt.Printf("  %5d ms  %s\n", r.ms, r.tag)
		} else {
			fmt.Printf("  failed    %s\n", r.tag)
		}
	}
	fmt.Printf("\n%d of %d nodes answered\n", ok, len(results))
	printWhy(c.Logs(), secret)
}

// probe asks the server the same question in several ways and prints only
// what it answered: the status, the kind and size of the body, the server's
// name, and whether Faro could read nodes from it.
func probe(url string) {
	uas := []string{"clash-verge/v2.2.3", "ClashX/1.118.0", "clash.meta", "mihomo/1.18", "sing-box 1.14.4", "v2rayN/6.0", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/120 Safari/537.36", "Faro/0.1 sing-box"}
	proxied := os.Getenv("HTTPS_PROXY") != "" || os.Getenv("https_proxy") != "" || os.Getenv("ALL_PROXY") != "" || os.Getenv("all_proxy") != ""
	fmt.Printf("proxy variables set in this shell: %v\n\n", proxied)
	fmt.Printf("%-8s %-6s %-26s %8s  %-12s %s\n", "route", "status", "content-type", "bytes", "server", "user-agent")
	for _, via := range []bool{true, false} {
		if via && !proxied {
			continue // the same as direct
		}
		for _, ua := range uas {
			r, err := sub.FetchWith(context.Background(), url, ua, via)
			route := "direct"
			if via && proxied {
				route = "env-proxy"
			}
			if r.Status == 0 {
				fmt.Printf("%-8s %-6s %s\n", route, "error", strings.ReplaceAll(fmt.Sprint(err), url, "<url>"))
				continue
			}
			ct := strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0]
			nodes := ""
			if r.Status == 200 {
				if n, perr := sub.Parse(r.Body); perr == nil {
					nodes = fmt.Sprintf("  -> %d nodes", len(n))
				} else {
					nodes = "  -> NOT NODES: " + perr.Error()
				}
			}
			if len(ua) > 40 {
				ua = ua[:40] + "…"
			}
			fmt.Printf("%-8s %-6d %-26s %8d  %-12s %s%s\n", route, r.Status, ct, len(r.Body), r.Header.Get("Server"), ua, nodes)
		}
	}
}
