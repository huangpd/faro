package ruleset

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Content is what a rule set holds, as far as names go: the full names and
// the suffixes, which stand for the name and everything under it, and the
// keywords and expressions. It answers whether a name is in the set, and how
// two sets overlap.
type Content struct {
	domain  map[string]bool
	suffix  map[string]bool
	keyword []string
	regex   []*regexp.Regexp
}

// ParseContent reads a rule set as the core writes it out (rule-set decompile).
func ParseContent(b []byte) (*Content, error) {
	var doc struct {
		Rules []struct {
			Domain  []string `json:"domain"`
			Suffix  []string `json:"domain_suffix"`
			Keyword []string `json:"domain_keyword"`
			Regex   []string `json:"domain_regex"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	c := &Content{domain: map[string]bool{}, suffix: map[string]bool{}}
	for _, r := range doc.Rules {
		for _, d := range r.Domain {
			c.domain[strings.ToLower(d)] = true
		}
		for _, s := range r.Suffix {
			c.suffix[strings.TrimPrefix(strings.ToLower(s), ".")] = true
		}
		c.keyword = append(c.keyword, r.Keyword...)
		for _, x := range r.Regex {
			if re, err := regexp.Compile(x); err == nil {
				c.regex = append(c.regex, re)
			}
		}
	}
	return c, nil
}

// Decompile reads the rule set at path with the core at bin.
func Decompile(bin, path string) (*Content, error) {
	dir, err := os.MkdirTemp("", "faro-set")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "set.json")
	if b, err := exec.Command(bin, "rule-set", "decompile", path, "-o", out).CombinedOutput(); err != nil {
		return nil, &decompileError{strings.TrimSpace(string(b))}
	}
	b, err := os.ReadFile(out)
	if err != nil {
		return nil, err
	}
	return ParseContent(b)
}

type decompileError struct{ out string }

func (e *decompileError) Error() string { return "rule-set decompile: " + e.out }

// Len is how many full names and suffixes the set holds.
func (c *Content) Len() int { return len(c.domain) + len(c.suffix) }

// Match says whether a name is in the set: as a full name, under a suffix, or
// by a keyword or an expression.
func (c *Content) Match(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if c.domain[host] || c.hasSuffix(host) {
		return true
	}
	for _, k := range c.keyword {
		if strings.Contains(host, k) {
			return true
		}
	}
	for _, re := range c.regex {
		if re.MatchString(host) {
			return true
		}
	}
	return false
}

// hasSuffix is whether the name, or a name above it, is one of the suffixes.
func (c *Content) hasSuffix(host string) bool {
	for h := host; h != ""; {
		if c.suffix[h] {
			return true
		}
		i := strings.IndexByte(h, '.')
		if i < 0 {
			break
		}
		h = h[i+1:]
	}
	return false
}

// Reaches says whether the set decides any name at or under name: a group that
// holds "example.com" stands for everything under it, so a set that holds
// "mail.example.com" overlaps it as well as one that holds "example.com".
func (c *Content) Reaches(name string) bool {
	name = strings.ToLower(name)
	if c.Match(name) {
		return true
	}
	dot := "." + name
	for d := range c.domain {
		if strings.HasSuffix(d, dot) {
			return true
		}
	}
	for s := range c.suffix {
		if strings.HasSuffix(s, dot) {
			return true
		}
	}
	return false
}

// names are the full names and suffixes, for comparing sets.
func (c *Content) names() []string {
	out := make([]string, 0, c.Len())
	for d := range c.domain {
		out = append(out, d)
	}
	for s := range c.suffix {
		out = append(out, s)
	}
	return out
}

// Overlap counts the names of a that b decides as well, and of b that a does,
// and gives one for an example. Keywords and expressions are not compared.
func Overlap(a, b *Content) (n int, example string) {
	for _, x := range a.names() {
		if b.Match(x) {
			n++
			if example == "" || len(x) < len(example) {
				example = x
			}
		}
	}
	for _, x := range b.names() {
		if a.Match(x) {
			n++
			if example == "" || len(x) < len(example) {
				example = x
			}
		}
	}
	return n, example
}
