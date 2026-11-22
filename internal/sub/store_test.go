package sub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStore(t *testing.T) {
	body := "trojan://pw@a.example.com:443#same\ntrojan://pw@b.example.com:443#same\ntrojan://pw@c.example.com:443#auto\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-Userinfo", "upload=10; download=20; total=100; expire=1900000000")
		w.Write([]byte(body))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "subs.json")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add(context.Background(), "ftp://x"); err == nil {
		t.Error("non-http URL accepted")
	}
	if err := s.Add(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	subs, _ := s.Snapshot()
	if len(subs) != 1 || subs[0].Used != 30 || subs[0].Total != 100 || len(subs[0].Nodes) != 3 {
		t.Fatalf("snapshot: %+v", subs)
	}
	tags := map[string]bool{}
	for _, o := range s.Outbounds() {
		if tags[o.Tag] || o.Tag == "auto" || o.Tag == "proxy" {
			t.Errorf("bad tag %q", o.Tag)
		}
		tags[o.Tag] = true
	}
	if len(tags) != 3 {
		t.Errorf("tags %v", tags)
	}

	// Survives a reopen; a failed refresh keeps the nodes.
	s.SetChoice("proxy", "same #2")
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	subs, sel := s2.Snapshot()
	if len(subs) != 1 || sel["proxy"] != "same #2" || len(subs[0].Nodes) != 3 {
		t.Fatalf("reopened: %+v %q", subs, sel)
	}
	srv.Close()
	if err := s2.Refresh(context.Background(), subs[0].ID); err == nil {
		t.Error("refresh of a dead server succeeded")
	}
	if subs, _ = s2.Snapshot(); len(subs[0].Nodes) != 3 {
		t.Error("failed refresh lost nodes")
	}
	s2.Remove(subs[0].ID)
	if subs, _ = s2.Snapshot(); len(subs) != 0 {
		t.Error("remove failed")
	}
}

// An older file kept the main group's choice as "selected".
func TestLegacySelectedMigrates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subs.json")
	os.WriteFile(path, []byte(`{"subs":[],"selected":"JP 02"}`), 0o600)
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ch := s.Snapshot(); ch["proxy"] != "JP 02" {
		t.Errorf("choices %v", ch)
	}
	s.SetChoice("claude", "UK 02")
	s.SetChoice("proxy", "")
	s2, _ := OpenStore(path)
	if _, ch := s2.Snapshot(); ch["claude"] != "UK 02" || ch["proxy"] != "" {
		t.Errorf("after reopen %v", ch)
	}
}

// A node may not take the name of a route's group.
func TestRouteGroupTagsReserved(t *testing.T) {
	s := &Store{Subs: []*Subscription{{Nodes: []Node{
		{Name: "route-claude", Out: map[string]any{}}, {Name: "route-claude", Out: map[string]any{}},
		{Name: "proxy", Out: map[string]any{}}, {Name: "auto", Out: map[string]any{}}}}}}
	seen := map[string]bool{}
	for _, o := range s.Outbounds() { // must also end: this once looped for ever
		if strings.HasPrefix(o.Tag, "route-") || o.Tag == "proxy" || o.Tag == "auto" || o.Tag == "direct" {
			t.Errorf("tag %q collides with a group", o.Tag)
		}
		if seen[o.Tag] {
			t.Errorf("tag %q twice", o.Tag)
		}
		seen[o.Tag] = true
	}
	if len(seen) != 4 {
		t.Errorf("%d tags, want 4", len(seen))
	}
}

func TestAddFromFileAndRefreshReadsItAgain(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "profile.yaml")
	os.WriteFile(file, []byte("proxies:\n  - {name: UK 01, type: trojan, server: h1.example.com, port: 443, password: p}\n"), 0o600)
	s, err := OpenStore(filepath.Join(dir, "subs.json"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Add(ctx, file); err != nil {
		t.Fatal(err)
	}
	subs, _ := s.Snapshot()
	if len(subs) != 1 || subs[0].Name != "profile.yaml" || len(subs[0].Nodes) != 1 || subs[0].URL != file {
		t.Fatalf("%+v", subs)
	}
	// The other client updates the file; a refresh picks the change up.
	os.WriteFile(file, []byte("proxies:\n  - {name: UK 01, type: trojan, server: h1.example.com, port: 443, password: p}\n  - {name: DE 01, type: trojan, server: h2.example.com, port: 443, password: p}\n"), 0o600)
	if err := s.Refresh(ctx, subs[0].ID); err != nil {
		t.Fatal(err)
	}
	if subs, _ = s.Snapshot(); len(subs[0].Nodes) != 2 {
		t.Errorf("refresh did not re-read the file: %d nodes", len(subs[0].Nodes))
	}
	// file:// and ~ forms.
	if err := s.Add(ctx, "file://"+file); err != nil {
		t.Errorf("file URL: %v", err)
	}
	t.Setenv("HOME", dir)
	if err := s.Add(ctx, "~/profile.yaml"); err != nil {
		t.Errorf("~ path: %v", err)
	}
	// What is not a file or an http URL is refused, and so is a folder or a missing file.
	for _, bad := range []string{"ftp://x/y", "relative/path.yaml", dir, filepath.Join(dir, "missing.yaml"), ""} {
		if err := s.Add(ctx, bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	// A file holding the provider's notice is an error that says so.
	notice := filepath.Join(dir, "notice.yaml")
	os.WriteFile(notice, []byte("proxies:\n  - {name: \"访问受限 REF: x\", type: trojan, server: 127.0.0.1, port: 1, password: blocked}\n"), 0o600)
	if err := s.Add(ctx, notice); err == nil || !strings.Contains(err.Error(), "notice") {
		t.Errorf("notice file: %v", err)
	}
}

// A server that knows only some client names: Faro finds the one it takes,
// and never makes more than a few requests while being refused.
func TestFetchAnyTriesClientNames(t *testing.T) {
	good := "trojan://pw@h.example.com:443#UK 01\n"
	serve := func(accept string, count *int) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*count++
			if accept != "" && strings.HasPrefix(r.Header.Get("User-Agent"), accept) {
				w.Write([]byte(good))
				return
			}
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte("proxies:\n  - {name: \"访问受限\", type: trojan, server: 127.0.0.1, port: 1, password: blocked}\n"))
		}))
	}
	ctx := context.Background()
	for accept, want := range map[string]struct {
		ua       string
		requests int
	}{
		"clash-verge": {"clash-verge/v2.2.3", 1},
		"ClashX":      {"ClashX/1.118.0", 2},
		"clash.meta":  {"clash.meta", 3},
	} {
		var n int
		srv := serve(accept, &n)
		_, _, ua, err := FetchAny(ctx, srv.URL)
		srv.Close()
		if err != nil || ua != want.ua || n != want.requests {
			t.Errorf("server taking %q: ua %q, %d requests, err %v; want %q in %d", accept, ua, n, err, want.ua, want.requests)
		}
	}
	// A server that refuses all: an error naming the notice, and no more than three requests.
	var n int
	srv := serve("", &n)
	defer srv.Close()
	if _, _, _, err := FetchAny(ctx, srv.URL); err == nil || n > len(UserAgents) {
		t.Errorf("refused everywhere: err %v after %d requests", err, n)
	}
}
