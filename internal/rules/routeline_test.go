package rules

import "testing"

func TestARouteLineIsChangedInPlace(t *testing.T) {
	text := "# top\nroute claude regex \"^英国-UK-1-\" gate ipv4 exit:198.51.100.0/24 # mine\nroute openai region UK gate ipv4\nclaude domain:claude.ai\n"
	rl, ok := ReadRoute(text, "claude")
	if !ok || rl.Kind != "regex" || rl.Arg != "^英国-UK-1-" || !rl.Gate || !rl.IPv4 || len(rl.Exit) != 1 {
		t.Fatalf("%+v %v", rl, ok)
	}
	rl.Arg = `^英国-UK-(1|2)-\d`
	rl.Exit = append(rl.Exit, "203.0.113.0/24")
	rl.Check, rl.Hours = "5m", "09:00-23:30"
	out, err := SetRoute(text, rl)
	if err != nil {
		t.Fatal(err)
	}
	want := "# top\nroute claude regex \"^英国-UK-(1|2)-\\d\" gate ipv4 exit:198.51.100.0/24 exit:203.0.113.0/24 check:5m hours:09:00-23:30 # mine\nroute openai region UK gate ipv4\nclaude domain:claude.ai\n"
	if out != want {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}
	if _, errs := Compile(out, []string{"英国-UK-2-a"}, nil); errs != nil {
		t.Errorf("what was written does not compile: %v", errs)
	}
	// Read back, it is the same.
	if back, _ := ReadRoute(out, "claude"); back.String() != rl.String() {
		t.Errorf("read back %q", back.String())
	}
	// Without exits, the times are not written: they would not compile.
	rl.Exit = nil
	if s := rl.String(); s != `route claude regex "^英国-UK-(1|2)-\d" gate ipv4` {
		t.Errorf("%q", s)
	}
	if _, err := SetRoute(text, RouteLine{ID: "nope"}); err == nil {
		t.Error("wrote a route that is not there")
	}
}

// noudp and noipv6 repeat each rule sent to the route as a block, above all.
func TestARoutesGuardsGoAboveEverything(t *testing.T) {
	p, errs := Compile(`route c region UK noudp noipv6
route d region UK
d domain:other.example
c domain:claude.example domain:x.example
c process:Claude
c tcp domain:tcp-only.example
c ipv4 cidr:203.0.113.0/24
`, []string{"UK 1"}, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	var guards, rest []Rule
	for _, r := range p.Rules {
		if r.Guard != "" {
			guards = append(guards, r)
		} else {
			rest = append(rest, r)
		}
	}
	if len(guards) == 0 || p.Rules[0].Guard != "c" || p.Rules[len(guards)].Guard != "" {
		t.Fatalf("the guards are not first: %+v", p.Rules)
	}
	udp, v6 := map[string]bool{}, map[string]bool{}
	for _, g := range guards {
		if g.Target != TargetBlock {
			t.Errorf("a guard that does not block: %+v", g)
		}
		for _, m := range g.Matchers {
			for _, v := range m.Values {
				if g.Network == "udp" {
					udp[v] = true
				}
				if g.IPVersion == 6 {
					v6[v] = true
				}
			}
		}
	}
	for _, v := range []string{"claude.example", "x.example", "Claude", "203.0.113.0/24"} {
		if !udp[v] {
			t.Errorf("%s: no UDP guard", v)
		}
	}
	for _, v := range []string{"claude.example", "Claude", "tcp-only.example"} {
		if !v6[v] {
			t.Errorf("%s: no IPv6 guard", v)
		}
	}
	if udp["tcp-only.example"] || v6["203.0.113.0/24"] || udp["other.example"] {
		t.Errorf("a guard where none is wanted: udp %v v6 %v", udp, v6)
	}
	// The route's own rules are left as they were, after the guards.
	if len(rest) != 5 {
		t.Errorf("%d rules after the guards, want 5: %+v", len(rest), rest)
	}
	rl, _ := ReadRoute("route c region UK noudp noipv6\n", "c")
	if !rl.NoUDP || !rl.NoIPv6 || rl.String() != "route c region UK noudp noipv6" {
		t.Errorf("%+v %q", rl, rl.String())
	}
}
