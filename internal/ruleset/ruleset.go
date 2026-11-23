// Package ruleset keeps the rule-set files (.srs) the rules refer to.
//
// A snapshot ships inside the app, so the first run works with no network
// and no core to go through. Update fetches newer ones later, through the
// core's own proxy port, since the sites may not be reachable directly.
package ruleset

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed sets.list
var setsList string

// Files are the rule sets Faro knows how to fetch, name to repository. The
// list is data (sets.list), shared with scripts/fetch-rulesets.sh.
var Files = parseList(setsList)

func parseList(text string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(strings.SplitN(line, "#", 2)[0])
		if len(f) == 2 {
			m[f[1]] = f[0]
		}
	}
	return m
}

// mirrors are tried in order.
func mirrors(repo, name string) []string {
	return []string{
		fmt.Sprintf("https://cdn.jsdelivr.net/gh/SagerNet/%s@rule-set/%s.srs", repo, name),
		fmt.Sprintf("https://raw.githubusercontent.com/SagerNet/%s/rule-set/%s.srs", repo, name),
	}
}

// Install copies the bundled snapshot in resDir/rulesets into dir, for each
// file that is missing there or older than the bundled one.
func Install(resDir, dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if resDir == "" {
		return nil
	}
	src, err := filepath.Glob(filepath.Join(resDir, "rulesets", "*.srs"))
	if err != nil {
		return err
	}
	for _, from := range src {
		to := filepath.Join(dir, filepath.Base(from))
		fi, err := os.Stat(from)
		if err != nil {
			continue
		}
		if ti, err := os.Stat(to); err == nil && !ti.ModTime().Before(fi.ModTime()) {
			continue
		}
		if err := copyFile(from, to, fi.ModTime()); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(from, to string, mtime time.Time) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := to + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	_ = os.Chtimes(tmp, mtime, mtime)
	return os.Rename(tmp, to)
}

// Paths maps each installed rule set's name to its file.
func Paths(dir string) map[string]string {
	m := map[string]string{}
	files, _ := filepath.Glob(filepath.Join(dir, "*.srs"))
	for _, f := range files {
		if fi, err := os.Stat(f); err == nil && fi.Size() > 0 {
			m[strings.TrimSuffix(filepath.Base(f), ".srs")] = f
		}
	}
	return m
}

// Result is what an Update did.
type Result struct {
	Fetched int // rule sets that came whole from a mirror
	Changed int // of those, the ones whose content differs from what was installed
}

// mirrorURLs gives the places to fetch a rule set from, in order. A variable,
// so that tests can point it at a server of their own.
var mirrorURLs = mirrors

// Update fetches every known rule set into dir. proxyPort, when not 0, is the
// local proxy to go through. A file is replaced only by a download that came
// whole, and only when it differs: one that is unchanged is left as it is, so
// that nothing downstream is disturbed by an update that found nothing new.
// The sing-box core watches the files and loads a replaced one by itself.
func Update(ctx context.Context, dir string, proxyPort int) (Result, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	if proxyPort > 0 {
		u, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", proxyPort))
		client.Transport = &http.Transport{Proxy: http.ProxyURL(u)}
	}
	var res Result
	var errs []error
	for name, repo := range wanted(dir) {
		var last error
		for _, u := range mirrorURLs(repo, name) {
			changed, err := fetch(ctx, client, u, filepath.Join(dir, name+".srs"))
			if err == nil {
				res.Fetched++
				if changed {
					res.Changed++
				}
				last = nil
				break
			}
			last = err
		}
		if last != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, last))
		}
	}
	return res, errors.Join(errs...)
}

// wanted is every rule set to keep current: those Faro knows, and any that was
// installed besides, such as one a person added.
func wanted(dir string) map[string]string {
	m := map[string]string{}
	for n, r := range Files {
		m[n] = r
	}
	for n := range Paths(dir) {
		if r, ok := RepoOf(n); ok {
			m[n] = r
		}
	}
	return m
}

// fetch downloads one rule set to a file and says whether the file changed.
func fetch(ctx context.Context, c *http.Client, u, to string) (changed bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("%s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return false, err
	}
	// A rule set starts with the magic "SRS"; anything else is a page in its place.
	if len(body) < 8 || string(body[:3]) != "SRS" {
		return false, errors.New("not a rule set")
	}
	if old, err := os.ReadFile(to); err == nil && bytes.Equal(old, body) {
		return false, nil
	}
	tmp := to + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return false, err
	}
	return true, os.Rename(tmp, to) // a rename: the core never sees half a file
}

// Newest is the time of the most recent change among the installed rule sets,
// zero if there are none.
func Newest(dir string) time.Time {
	var t time.Time
	files, _ := filepath.Glob(filepath.Join(dir, "*.srs"))
	for _, f := range files {
		if fi, err := os.Stat(f); err == nil && fi.ModTime().After(t) {
			t = fi.ModTime()
		}
	}
	return t
}
