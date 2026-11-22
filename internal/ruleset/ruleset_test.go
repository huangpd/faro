package ruleset

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path, body string, mtime time.Time) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(path, mtime, mtime)
}

func TestInstallCopiesNewerOnly(t *testing.T) {
	res, dir := t.TempDir(), filepath.Join(t.TempDir(), "sets")
	old, recent := time.Now().Add(-48*time.Hour), time.Now().Add(-time.Hour)
	write(t, filepath.Join(res, "rulesets", "geosite-cn.srs"), "SRS-bundled", recent)
	write(t, filepath.Join(res, "rulesets", "geoip-cn.srs"), "SRS-bundled-ip", old)

	if err := Install(res, dir); err != nil {
		t.Fatal(err)
	}
	got := Paths(dir)
	if len(got) != 2 {
		t.Fatalf("paths %v", got)
	}
	// The user updated geoip-cn after the bundle was made: it must not be put back.
	write(t, filepath.Join(dir, "geoip-cn.srs"), "SRS-updated", time.Now())
	// And geosite-cn in the bundle got newer than the installed one.
	write(t, filepath.Join(res, "rulesets", "geosite-cn.srs"), "SRS-bundled-v2", time.Now().Add(time.Minute))
	if err := Install(res, dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "geoip-cn.srs")); string(b) != "SRS-updated" {
		t.Errorf("an updated set was overwritten by the older bundle: %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "geosite-cn.srs")); string(b) != "SRS-bundled-v2" {
		t.Errorf("a newer bundle was not installed: %s", b)
	}
	if err := Install("", dir); err != nil { // no bundle: fine
		t.Error(err)
	}
}

func TestPathsIgnoresEmptyAndOtherFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.srs"), "SRSx", time.Now())
	write(t, filepath.Join(dir, "empty.srs"), "", time.Now())
	write(t, filepath.Join(dir, "notes.txt"), "x", time.Now())
	got := Paths(dir)
	if len(got) != 1 || got["a"] == "" {
		t.Errorf("%v", got)
	}
}

func TestFetchRejectsNonRuleSets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/good":
			w.Write([]byte("SRS\x03........"))
		case "/html":
			w.Write([]byte("<html>captive portal</html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	to := filepath.Join(dir, "x.srs")
	write(t, to, "SRS-keep-me", time.Now())
	ctx := context.Background()

	if _, err := fetch(ctx, srv.Client(), srv.URL+"/html", to); err == nil {
		t.Error("a web page was taken for a rule set")
	}
	if _, err := fetch(ctx, srv.Client(), srv.URL+"/missing", to); err == nil {
		t.Error("404 accepted")
	}
	if b, _ := os.ReadFile(to); string(b) != "SRS-keep-me" {
		t.Errorf("a failed fetch damaged the file: %q", b)
	}
	if _, err := fetch(ctx, srv.Client(), srv.URL+"/good", to); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(to); len(b) < 8 || string(b[:3]) != "SRS" || string(b) == "SRS-keep-me" {
		t.Errorf("good fetch not stored: %q", b)
	}
}

// serve is a stand-in for the mirrors: it holds the rule sets by name, and
// answers anything else with 404. What it holds can be changed between updates.
func serve(t *testing.T, sets map[string]string) (*httptest.Server, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".srs")
		body, ok := sets[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
	old := mirrorURLs
	mirrorURLs = func(repo, name string) []string { return []string{srv.URL + "/" + name + ".srs"} }
	return srv, func() { srv.Close(); mirrorURLs = old }
}

func allSets(version string) map[string]string {
	m := map[string]string{}
	for name := range Files {
		m[name] = "SRS\x03" + version + "-" + name
	}
	return m
}

func TestUpdateReplacesOnlyWhatChanged(t *testing.T) {
	sets := allSets("one")
	_, stop := serve(t, sets)
	defer stop()
	dir := t.TempDir()
	ctx := context.Background()

	// The first time, everything is new.
	res, err := Update(ctx, dir, 0)
	if err != nil || res.Fetched != len(Files) || res.Changed != len(Files) {
		t.Fatalf("first update: %+v %v", res, err)
	}
	// Nothing new upstream: everything is fetched, nothing is changed, and no file is touched.
	before := map[string]time.Time{}
	for name := range Files {
		fi, _ := os.Stat(filepath.Join(dir, name+".srs"))
		before[name] = fi.ModTime()
	}
	time.Sleep(20 * time.Millisecond)
	res, err = Update(ctx, dir, 0)
	if err != nil || res.Fetched != len(Files) || res.Changed != 0 {
		t.Fatalf("second update: %+v %v", res, err)
	}
	for name := range Files {
		if fi, _ := os.Stat(filepath.Join(dir, name+".srs")); !fi.ModTime().Equal(before[name]) {
			t.Errorf("%s was rewritten though it had not changed", name)
		}
	}
	// One set changed upstream: that one is replaced.
	sets["geosite-cn"] = "SRS\x03two-geosite-cn"
	res, err = Update(ctx, dir, 0)
	if err != nil || res.Changed != 1 {
		t.Fatalf("third update: %+v %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "geosite-cn.srs")); !strings.Contains(string(b), "two") {
		t.Error("the changed set was not replaced")
	}
}

// A mirror that fails for one set must not cost the others, nor damage the one it failed for.
func TestUpdateOneFailingDoesNotHurtTheRest(t *testing.T) {
	sets := allSets("one")
	_, stop := serve(t, sets)
	defer stop()
	dir := t.TempDir()
	if _, err := Update(context.Background(), dir, 0); err != nil {
		t.Fatal(err)
	}
	delete(sets, "geoip-cn")                        // gone from the mirror
	sets["geosite-openai"] = "<html>blocked</html>" // a page in its place
	sets["geosite-cn"] = "SRS\x03new-cn"

	res, err := Update(context.Background(), dir, 0)
	if err == nil || !strings.Contains(err.Error(), "geoip-cn") || !strings.Contains(err.Error(), "geosite-openai") {
		t.Fatalf("errors %v", err)
	}
	if res.Fetched != len(Files)-2 || res.Changed != 1 {
		t.Errorf("%+v", res)
	}
	for name, want := range map[string]string{"geoip-cn": "one-geoip-cn", "geosite-openai": "one-geosite-openai", "geosite-cn": "new-cn"} {
		if b, _ := os.ReadFile(filepath.Join(dir, name+".srs")); !strings.Contains(string(b), want) {
			t.Errorf("%s holds %q, want it to hold %q", name, b, want)
		}
	}
}

func TestNewest(t *testing.T) {
	dir := t.TempDir()
	if !Newest(dir).IsZero() {
		t.Error("an empty directory has a newest")
	}
	old, recent := time.Now().Add(-72*time.Hour), time.Now().Add(-time.Hour)
	write(t, filepath.Join(dir, "a.srs"), "SRSa", old)
	write(t, filepath.Join(dir, "b.srs"), "SRSb", recent)
	if got := Newest(dir); got.Sub(recent).Abs() > time.Second {
		t.Errorf("newest %v, want about %v", got, recent)
	}
}
