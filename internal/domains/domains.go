// Package domains is the person's own list of sites, kept in named groups:
// each group has one place its sites go, and holds as many sites as wanted.
// The list is the person's day-to-day way of saying where a site goes; the
// rules text is for what needs more than that. The groups are checked first.
package domains

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Group is a name, a target (a route, or proxy, auto, direct, block), and sites.
// A site stands for itself and everything under it: example.com is www.example.com too.
type Group struct {
	Name    string   `json:"name"`
	Target  string   `json:"target"`
	Domains []string `json:"domains"`
	// Sets are rule sets, by name, that stand for many sites at once.
	Sets []string `json:"sets,omitempty"`
	// Rules are what else the group holds, in the rules language's kind:value:
	// programs (process:, path:), exact names (full:), address ranges (cidr:).
	// Each kind goes on lines of its own, so that they are "or", never "and".
	Rules []string `json:"rules,omitempty"`
	// App is the library entry the group was added from, "" for one written by hand.
	App string `json:"app,omitempty"`
}

var label = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// One reads a site out of what was typed or pasted: a whole address, with its
// scheme, path and port, is cut down to the name.
func One(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(s, "*."), "."), ".")
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(s) > 253 {
		return "", false
	}
	for _, p := range parts {
		if !label.MatchString(p) {
			return "", false
		}
	}
	if last := parts[len(parts)-1]; strings.Trim(last, "0123456789") == "" {
		return "", false // an IP address, not a name
	}
	return s, true
}

// Parse reads sites out of text, separated by spaces, commas, semicolons or
// lines. Those it cannot read come back as bad, as typed.
func Parse(text string) (ok, bad []string) {
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == '，' || r == '；'
	}) {
		d, good := One(f)
		switch {
		case !good:
			bad = append(bad, f)
		case !seen[d]:
			seen[d] = true
			ok = append(ok, d)
		}
	}
	return ok, bad
}

// Base is the name a site is added under when it is kept as a whole: the last
// two labels, or three under a country's own "com.cn" and the like.
func Base(host string) string {
	d, ok := One(host)
	if !ok {
		return host
	}
	p := strings.Split(d, ".")
	n := 2
	if len(p) >= 3 && len(p[len(p)-1]) == 2 {
		switch p[len(p)-2] {
		case "com", "co", "net", "org", "gov", "edu", "ac":
			n = 3
		}
	}
	if len(p) <= n {
		return d
	}
	return strings.Join(p[len(p)-n:], ".")
}

const perLine = 8

// Head is the groups as lines of the rules language, for putting before the
// person's text. owners[i] is the group that line i+1 came from.
func Head(groups []Group) (text string, owners []int) {
	var b strings.Builder
	for gi, g := range groups {
		if b.Len() > 0 && len(g.Domains)+len(g.Sets)+len(g.Rules) > 0 { // a blank line between groups: their rules are never merged
			b.WriteString("\n")
			owners = append(owners, gi)
		}
		for i := 0; i < len(g.Domains); i += perLine {
			end := i + perLine
			if end > len(g.Domains) {
				end = len(g.Domains)
			}
			b.WriteString(g.Target)
			for _, d := range g.Domains[i:end] {
				b.WriteString(" domain:" + d)
			}
			b.WriteString("\n")
			owners = append(owners, gi)
		}
		for i := 0; i < len(g.Sets); i += perLine {
			end := min(i+perLine, len(g.Sets))
			b.WriteString(g.Target)
			for _, s := range g.Sets[i:end] {
				b.WriteString(" set:" + s)
			}
			b.WriteString("\n")
			owners = append(owners, gi)
		}
		var kinds []string
		byKind := map[string][]string{}
		for _, r := range g.Rules {
			k, v, ok := strings.Cut(r, ":")
			if !ok {
				continue
			}
			if byKind[k] == nil {
				kinds = append(kinds, k)
			}
			if strings.ContainsAny(v, " \t") {
				v = `"` + v + `"`
			}
			byKind[k] = append(byKind[k], k+":"+v)
		}
		for _, k := range kinds {
			toks := byKind[k]
			for i := 0; i < len(toks); i += perLine {
				b.WriteString(g.Target + " " + strings.Join(toks[i:min(i+perLine, len(toks))], " ") + "\n")
				owners = append(owners, gi)
			}
		}
	}
	if b.Len() > 0 { // and after them: the text's first rule is not merged with a group's
		b.WriteString("\n")
		owners = append(owners, len(groups)-1)
	}
	return b.String(), owners
}

// Load reads the groups; a missing file is no groups.
func Load(path string) ([]Group, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f struct {
		Groups []Group `json:"groups"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return f.Groups, nil
}

// Save writes the groups whole, so that a crash leaves the old file or the new.
func Save(path string, groups []Group) error {
	b, err := json.MarshalIndent(struct {
		Groups []Group `json:"groups"`
	}{groups}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Copy is a deep copy, for handing out a snapshot.
func Copy(groups []Group) []Group {
	out := make([]Group, len(groups))
	for i, g := range groups {
		out[i] = Group{Name: g.Name, Target: g.Target, Domains: append([]string(nil), g.Domains...),
			Sets: append([]string(nil), g.Sets...), Rules: append([]string(nil), g.Rules...), App: g.App}
	}
	return out
}
