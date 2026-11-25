package applib

import (
	"strings"
	"testing"

	"faro/internal/domains"
	"faro/internal/rules"
	"faro/internal/ruleset"
)

// Every entry is whole, unique, and compiles as a group sent to a route.
func TestEveryEntryCompiles(t *testing.T) {
	all := All()
	if len(all) < 10 {
		t.Fatalf("%d entries", len(all))
	}
	seen := map[string]bool{}
	for _, a := range all {
		if a.ID == "" || a.Name == "" || a.Icon == "" || len(a.Rules) == 0 || seen[a.ID] {
			t.Errorf("entry %+v", a)
		}
		seen[a.ID] = true
		for _, s := range a.Sets() {
			if !ruleset.Valid(s) {
				t.Errorf("%s: %s is not a set that can be added", a.ID, s)
			}
		}
		for _, d := range a.Domains() {
			if got, ok := domains.One(d); !ok || got != d {
				t.Errorf("%s: %q is not a site as a group keeps it", a.ID, d)
			}
		}
		head, _ := domains.Head([]domains.Group{{Name: a.Name, Target: "x", Domains: a.Domains(), Rules: a.Others()}})
		if _, errs := rules.Compile("route x region UK\n"+head, []string{"UK 1"}, nil); errs != nil {
			t.Errorf("%s: %v\n%s", a.ID, errs, head)
		}
	}
}

func TestSearchAndTokens(t *testing.T) {
	if got := Tokens(`process:a process:"b c (d)" full:x`); strings.Join(got, "|") != "process:a|process:b c (d)|full:x" {
		t.Errorf("%q", got)
	}
	if len(Search("")) != len(All()) {
		t.Error("an empty search is not all")
	}
	for _, a := range Search("GIT") {
		if !strings.Contains(strings.ToLower(a.ID+a.Name+strings.Join(a.Rules, " ")), "git") {
			t.Errorf("%s does not hold the text", a.ID)
		}
	}
	if _, ok := Find("no-such"); ok {
		t.Error("found what is not there")
	}
}
