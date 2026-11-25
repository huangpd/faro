package ruleset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The catalog is the names of the rule sets a repository has, so that one can
// be searched for and added. Only the geosite sets are offered: they match on
// names, which is what the person's groups of sites are made of. The names
// are fetched, not shipped; what is shipped (Files) is searchable without a
// network.

var setName = regexp.MustCompile(`^geosite-[a-z0-9][a-z0-9@!._-]{0,80}$`)

// Valid says whether name is one a person may add: a geosite set, by its name.
func Valid(name string) bool { return setName.MatchString(name) }

// RepoOf is the repository a rule set of that name comes from.
func RepoOf(name string) (string, bool) {
	switch {
	case strings.HasPrefix(name, "geosite-"):
		return "sing-geosite", true
	case strings.HasPrefix(name, "geoip-"):
		return "sing-geoip", true
	}
	return "", false
}

const catalogFile = "catalog.txt"

// LoadCatalog is the names known: the fetched list, and those that ship.
func LoadCatalog(dir string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if Valid(n) && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, catalogFile)); err == nil {
		for _, l := range strings.Fields(string(b)) {
			add(l)
		}
	}
	for n := range Files {
		add(n)
	}
	for n := range Paths(dir) {
		add(n)
	}
	sort.Strings(out)
	return out
}

// CatalogAge is when the fetched list was made, zero if it never was.
func CatalogAge(dir string) time.Time {
	if fi, err := os.Stat(filepath.Join(dir, catalogFile)); err == nil {
		return fi.ModTime()
	}
	return time.Time{}
}

// Search finds the names holding every word of query, those that start with
// it first, at most limit of them. The shared "geosite-" is not searched.
func Search(names []string, query string, limit int) []string {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return nil
	}
	var first, rest []string
	for _, n := range names {
		short := strings.TrimPrefix(n, "geosite-")
		all := true
		for _, w := range words {
			if !strings.Contains(short, w) {
				all = false
				break
			}
		}
		switch {
		case !all:
		case strings.HasPrefix(short, words[0]):
			first = append(first, n)
		default:
			rest = append(rest, n)
		}
	}
	out := append(first, rest...)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Size is the size of an installed set's file, 0 if it is not installed.
func Size(dir, name string) int64 {
	if fi, err := os.Stat(filepath.Join(dir, name+".srs")); err == nil {
		return fi.Size()
	}
	return 0
}

// catalogURLs are the places the list of names is had from; a variable, so that
// tests can point it elsewhere.
var catalogURLs = func() []string {
	return []string{
		"https://api.github.com/repos/SagerNet/sing-geosite/git/trees/rule-set",
		"https://data.jsdelivr.com/v1/packages/gh/SagerNet/sing-geosite@rule-set?structure=flat",
	}
}

func httpClient(proxyPort int) *http.Client {
	c := &http.Client{Timeout: 30 * time.Second}
	if proxyPort > 0 {
		u, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", proxyPort))
		c.Transport = &http.Transport{Proxy: http.ProxyURL(u)}
	}
	return c
}

// FetchCatalog fetches the list of names and keeps it in dir.
func FetchCatalog(ctx context.Context, dir string, proxyPort int) ([]string, error) {
	client := httpClient(proxyPort)
	defer client.CloseIdleConnections() // a transport of its own keeps them open otherwise
	var last error
	for _, u := range catalogURLs() {
		names, err := fetchNames(ctx, client, u)
		if err != nil {
			last = err
			continue
		}
		if len(names) == 0 {
			last = errors.New("no rule sets in the list")
			continue
		}
		tmp := filepath.Join(dir, catalogFile+".tmp")
		if err := os.WriteFile(tmp, []byte(strings.Join(names, "\n")+"\n"), 0o600); err != nil {
			return nil, err
		}
		if err := os.Rename(tmp, filepath.Join(dir, catalogFile)); err != nil {
			return nil, err
		}
		return LoadCatalog(dir), nil
	}
	return nil, last
}

func fetchNames(ctx context.Context, c *http.Client, u string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	return parseNames(body)
}

// parseNames reads the names out of either list: GitHub's tree, or jsDelivr's.
func parseNames(body []byte) ([]string, error) {
	var doc struct {
		Tree  []struct{ Path string } `json:"tree"`
		Files []struct{ Name string } `json:"files"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	var raw []string
	for _, t := range doc.Tree {
		raw = append(raw, t.Path)
	}
	for _, f := range doc.Files {
		raw = append(raw, f.Name)
	}
	var out []string
	for _, p := range raw {
		p = strings.TrimPrefix(p, "/")
		if n, ok := strings.CutSuffix(p, ".srs"); ok && Valid(n) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, nil
}

// FetchOne fetches a single rule set into dir, by the same mirrors as Update.
func FetchOne(ctx context.Context, dir, name string, proxyPort int) error {
	repo, ok := RepoOf(name)
	if !ok {
		return fmt.Errorf("%s: not a rule set name", name)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	client := httpClient(proxyPort)
	defer client.CloseIdleConnections() // a transport of its own keeps them open otherwise
	var last error
	for _, u := range mirrorURLs(repo, name) {
		if _, err := fetch(ctx, client, u, filepath.Join(dir, name+".srs")); err != nil {
			last = err
			continue
		}
		return nil
	}
	return fmt.Errorf("%s: %w", name, last)
}
