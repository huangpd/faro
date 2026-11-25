package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"faro/internal/applib"
)

func TestApplicationsAreAddedToAnExit(t *testing.T) {
	res, _ := filepath.Abs("../../resources/rulesets/geosite-xai.srs")
	var mu sync.Mutex
	var fetched []string
	e := newEngineWith(t, (&fakeNetworksetup{}).run, func(o *Options) {
		o.FetchSet = func(_ context.Context, dir, name string, _ int) error {
			mu.Lock()
			fetched = append(fetched, name)
			mu.Unlock()
			if strings.Contains(name, "fail") {
				return errors.New("no route to host")
			}
			b, err := os.ReadFile(res)
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dir, name+".srs"), b, 0o600)
		}
	})
	e.addAndWait(t, subFile(t, "🇬🇧 London 01"), 1)
	if errs := e.ApplyRules("route uk region UK noudp\n"); errs != nil {
		t.Fatal(errs)
	}
	// From the library: a group with its sites, its other rules, and its sets once fetched.
	lib := applib.All()[0]
	if err := e.AddApp("uk", lib.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "its sets", func() bool { return e.State().Busy == "" && len(e.State().Groups[0].Sets) == len(lib.Sets()) })
	g := e.State().Groups[0]
	if g.Name != lib.Name || g.Target != "uk" || g.App != lib.ID || len(g.Domains) != len(lib.Domains()) || len(g.Rules) != len(lib.Others()) {
		t.Errorf("group %+v", g)
	}
	if err := e.AddApp("uk", lib.ID); err == nil {
		t.Error("the same application added twice")
	}
	if err := e.AddApp("nowhere", "github"); err == nil {
		t.Error("added to an exit that is not there")
	}
	// The exit's noudp reaches what the group holds: its UDP is blocked above all.
	order := e.Order()
	if len(order) == 0 || order[0].Guard != "uk" || order[0].Network != "udp" {
		t.Fatalf("no guard first: %+v", order)
	}
	// By hand: sites, and kind:value; a set that cannot be fetched is said, the rest stays.
	if err := e.AddCustomApp("uk", "工作", "https://work.example/x, process:\"Work App\" cidr:203.0.113.0/24 set:geosite-fail"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the fetch", func() bool { return e.State().Busy == "" && strings.Contains(e.State().Err, "fail") })
	w := e.State().Groups[1]
	if strings.Join(w.Domains, " ") != "work.example" || strings.Join(w.Rules, " ") != "process:Work App cidr:203.0.113.0/24" || w.App != "" || len(w.Sets) != 0 {
		t.Errorf("by hand %+v", w)
	}
	for _, bad := range []string{"", "not a site", "port:nope", "set:nope"} {
		if err := e.AddCustomApp("uk", "坏的", bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
