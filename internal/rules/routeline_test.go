package rules

import "testing"

func TestARouteLineIsChangedInPlace(t *testing.T) {
	text := "# top\nroute claude regex \"^英国-UK-1-\" gate ipv4 exit:198.51.100.0/24 # mine\nroute openai like claude gate ipv4\nclaude domain:claude.ai\n"
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
	want := "# top\nroute claude regex \"^英国-UK-(1|2)-\\d\" gate ipv4 exit:198.51.100.0/24 exit:203.0.113.0/24 check:5m hours:09:00-23:30 # mine\nroute openai like claude gate ipv4\nclaude domain:claude.ai\n"
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
