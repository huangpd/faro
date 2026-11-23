package ruleset

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestValidTakesOnlyGeositeNames(t *testing.T) {
	for _, ok := range []string{"geosite-google", "geosite-google-gemini", "geosite-apple@cn", "geosite-category-ai-chat-!cn"} {
		if !Valid(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"geoip-cn", "geosite-", "geosite-../x", "geosite-a b", "google", "geosite-A", ""} {
		if Valid(bad) {
			t.Errorf("%q should not be valid", bad)
		}
	}
}

func TestSearchPutsNamesThatStartWithTheWordFirst(t *testing.T) {
	names := []string{"geosite-cn", "geosite-google", "geosite-google-gemini", "geosite-not-google", "geosite-openai"}
	got := Search(names, "google", 8)
	want := []string{"geosite-google", "geosite-google-gemini", "geosite-not-google"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got := Search(names, "GOOGLE gemini", 8); !reflect.DeepEqual(got, []string{"geosite-google-gemini"}) {
		t.Errorf("every word must be there: %v", got)
	}
	if Search(names, "  ", 8) != nil || len(Search(names, "zzz", 8)) != 0 {
		t.Error("nothing asked, or nothing there, finds nothing")
	}
	if got := Search(names, "o", 2); len(got) != 2 {
		t.Errorf("limit: %v", got)
	}
}

func TestParseNamesReadsBothLists(t *testing.T) {
	tree := `{"tree":[{"path":"geosite-a.srs"},{"path":"geosite-b.srs"},{"path":"README.md"},{"path":"geoip-cn.srs"}]}`
	flat := `{"files":[{"name":"/geosite-b.srs"},{"name":"/geosite-c.srs"}]}`
	got, err := parseNames([]byte(tree))
	if err != nil || !reflect.DeepEqual(got, []string{"geosite-a", "geosite-b"}) {
		t.Errorf("tree: %v %v", got, err)
	}
	got, err = parseNames([]byte(flat))
	if err != nil || !reflect.DeepEqual(got, []string{"geosite-b", "geosite-c"}) {
		t.Errorf("flat: %v %v", got, err)
	}
}

func TestCatalogIsKeptAndSearchedWithWhatShips(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			http.Error(w, "no", 500)
			return
		}
		w.Write([]byte(`{"tree":[{"path":"geosite-zzz-new.srs"}]}`))
	}))
	defer srv.Close()
	old := catalogURLs
	defer func() { catalogURLs = old }()
	dir := t.TempDir()

	catalogURLs = func() []string { return []string{srv.URL + "/bad", srv.URL + "/ok"} } // the second is tried
	names, err := FetchCatalog(context.Background(), dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	has := func(ns []string, n string) bool {
		for _, x := range ns {
			if x == n {
				return true
			}
		}
		return false
	}
	if !has(names, "geosite-zzz-new") || !has(names, "geosite-google") {
		t.Errorf("fetched and shipped names are both there: %d names", len(names))
	}
	if !has(LoadCatalog(dir), "geosite-zzz-new") || CatalogAge(dir).IsZero() {
		t.Error("the list is kept")
	}

	catalogURLs = func() []string { return []string{srv.URL + "/bad"} }
	if _, err := FetchCatalog(context.Background(), dir, 0); err == nil {
		t.Error("a list that cannot be had is an error")
	}
	if !has(LoadCatalog(dir), "geosite-zzz-new") {
		t.Error("the list kept is not lost by a failed fetch")
	}
}

func TestUpdateAlsoKeepsASetThatWasAdded(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "geosite-added.srs"), []byte("SRS-old-content"), 0o600)
	got := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got[filepath.Base(r.URL.Path)] = true
		w.Write([]byte("SRS-new-content"))
	}))
	defer srv.Close()
	old := mirrorURLs
	defer func() { mirrorURLs = old }()
	mirrorURLs = func(repo, name string) []string { return []string{srv.URL + "/" + repo + "/" + name + ".srs"} }
	if _, err := Update(context.Background(), dir, 0); err != nil {
		t.Fatal(err)
	}
	if !got["geosite-added.srs"] {
		t.Errorf("an added set is kept current too: %v", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "geosite-added.srs")); string(b) != "SRS-new-content" {
		t.Errorf("not replaced: %q", b)
	}
}
