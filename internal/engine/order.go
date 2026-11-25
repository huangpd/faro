package engine

import (
	"strings"

	"faro/internal/domains"
	"faro/internal/rules"
)

// Step is one rule as the core checks it, in order: where it sends what it
// matches, what it is for, and which of its values a rule above takes first.
type Step struct {
	Target    string // a route's id, "proxy", "auto", "direct" or "block"
	Route     string // for "block": the route it was written for, which has no node
	Guard     string // for "block": the route whose noudp or noipv6 made it
	Label     string // the group it comes from, or the heading above it in the rules text
	Line      int    // its line in the rules text; 0 or less for a group's
	Network   string
	IPVersion int
	Matchers  []rules.Matcher
	Shadowed  []rules.Shadow // By is an index into the steps
}

// Order lists the rules in force as the core checks them. It is worked out
// when asked for, from the rules and groups in force.
func (e *Engine) Order() []Step {
	e.mu.Lock()
	plan, text := e.plan, e.rulesText
	var active []domains.Group
	for _, g := range e.groupsList {
		if e.groupErrs[g.Name] == "" {
			active = append(active, g)
		}
	}
	e.mu.Unlock()
	if plan == nil {
		return nil
	}
	_, owners := domains.Head(active)
	headings := sectionHeadings(text)
	shadows := rules.Shadows(plan.Rules)
	out := make([]Step, len(plan.Rules))
	for i, r := range plan.Rules {
		s := Step{Target: strings.TrimPrefix(r.Target, rules.GroupPrefix), Route: r.Route, Guard: r.Guard, Line: r.Line, Network: r.Network,
			IPVersion: r.IPVersion, Matchers: r.Matchers, Shadowed: shadows[i]}
		if r.Guard != "" {
			s.Label = r.Guard + " 的守卫"
		} else if r.Line <= 0 {
			if k := r.Line + len(owners) - 1; k >= 0 && k < len(owners) {
				s.Label = "分组 · " + active[owners[k]].Name
			}
		} else if r.Line-1 < len(headings) {
			s.Label = headings[r.Line-1]
		}
		out[i] = s
	}
	return out
}

// sectionHeadings gives each line of the rules text the heading it is under:
// the last comment of the form "# ---- <words> ----" above it.
func sectionHeadings(text string) []string {
	lines := strings.Split(text, "\n")
	out := make([]string, len(lines))
	cur := ""
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "#") {
			body := strings.TrimSpace(strings.TrimLeft(t, "#"))
			if strings.HasPrefix(body, "--") {
				cur = strings.TrimSpace(strings.Trim(body, "- "))
			}
		}
		out[i] = cur
	}
	return out
}

// EditRoute changes the line of route id in the rules text and applies the
// text, as ApplyRules does: what does not compile is refused, and said.
func (e *Engine) EditRoute(id string, change func(*rules.RouteLine)) []rules.Error {
	e.mu.Lock()
	text := e.rulesText
	e.mu.Unlock()
	rl, ok := rules.ReadRoute(text, id)
	if !ok {
		return []rules.Error{{Msg: "route " + id + " is not in the rules"}}
	}
	change(&rl)
	next, err := rules.SetRoute(text, rl)
	if err != nil {
		return []rules.Error{{Msg: err.Error()}}
	}
	if next == text {
		return nil
	}
	return e.ApplyRules(next)
}

// CheckExits asks every checked route where it leaves from, now.
func (e *Engine) CheckExits() { e.kickExits() }
