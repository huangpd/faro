package engine

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"faro/internal/domains"
	"faro/internal/ruleset"
)

// A rule set stands for many sites at once, so two groups that send to
// different places can both claim one site without anyone having written it
// twice. The group that comes first decides; Overlaps says where that happens,
// so that it is known and not a surprise. Rule sets are read out by the core,
// once for each version of the file.

// Overlap is sites that two groups with different places both reach. Winner
// is the group checked first, which decides them.
type Overlap struct {
	Winner, Other string
	Count         int    // names, about: a name under a set's suffix counts once
	Example       string // the shortest of them
}

type cachedSet struct {
	mtime int64
	c     *ruleset.Content
}

// content reads a rule set out, or returns what was read of that version.
func (e *Engine) content(name string) *ruleset.Content {
	path := filepath.Join(e.setsDir, name+".srs")
	fi, err := os.Stat(path)
	if err != nil {
		return nil
	}
	e.ctMu.Lock()
	defer e.ctMu.Unlock()
	if hit, ok := e.contents[name]; ok && hit.mtime == fi.ModTime().UnixNano() {
		return hit.c
	}
	c, err := ruleset.Decompile(e.core.Bin(), path)
	if e.contents == nil {
		e.contents = map[string]cachedSet{}
	}
	if err != nil { // remembered: a file the core cannot read is not asked again until it changes
		c = nil
	}
	e.contents[name] = cachedSet{fi.ModTime().UnixNano(), c}
	return c
}

// cachedContent is what was read already; known says whether it was tried. It
// never waits for the core.
func (e *Engine) cachedContent(name string) (c *ruleset.Content, known bool) {
	fi, err := os.Stat(filepath.Join(e.setsDir, name+".srs"))
	if err != nil {
		return nil, true
	}
	e.ctMu.Lock()
	defer e.ctMu.Unlock()
	if hit, ok := e.contents[name]; ok && hit.mtime == fi.ModTime().UnixNano() {
		return hit.c, true
	}
	return nil, false
}

// refreshOverlaps works out the overlaps of the groups, in the background.
func (e *Engine) refreshOverlaps() {
	e.mu.Lock()
	e.overlapGen++
	gen := e.overlapGen
	groups := domains.Copy(e.groupsList)
	e.mu.Unlock()

	ov := overlapsOf(groups, e.content)

	e.mu.Lock()
	if gen != e.overlapGen { // a later change is working it out again
		e.mu.Unlock()
		return
	}
	e.overlaps = ov
	e.mu.Unlock()
	e.changed()
}

// overlapsOf compares each group with those after it. content gives what a set holds.
func overlapsOf(groups []domains.Group, content func(string) *ruleset.Content) []Overlap {
	var out []Overlap
	for i := range groups {
		for j := i + 1; j < len(groups); j++ {
			a, b := groups[i], groups[j]
			if a.Target == b.Target {
				continue // the same place: nothing is decided differently
			}
			n, ex := 0, ""
			note := func(name string) {
				n++
				if ex == "" || len(name) < len(ex) {
					ex = name
				}
			}
			// Sites against sites: one under the other.
			for _, x := range a.Domains {
				for _, y := range b.Domains {
					if x == y || strings.HasSuffix(x, "."+y) {
						note(x)
					} else if strings.HasSuffix(y, "."+x) {
						note(y)
					}
				}
			}
			// A set against the other group's sites, and against its sets.
			for _, s := range a.Sets {
				cs := content(s)
				if cs == nil {
					continue
				}
				for _, y := range b.Domains {
					if cs.Reaches(y) {
						note(y)
					}
				}
				for _, t := range b.Sets {
					if ct := content(t); ct != nil && s != t {
						if m, x := ruleset.Overlap(cs, ct); m > 0 {
							n += m
							if ex == "" || len(x) < len(ex) {
								ex = x
							}
						}
					}
				}
			}
			for _, t := range b.Sets {
				ct := content(t)
				if ct == nil {
					continue
				}
				for _, x := range a.Domains {
					if ct.Reaches(x) {
						note(x)
					}
				}
			}
			if n > 0 {
				out = append(out, Overlap{Winner: a.Name, Other: b.Name, Count: n, Example: ex})
			}
		}
	}
	return out
}

// SetsHolding says which of the rule sets here have a site in them. A set not
// read yet is read in the background and the view is drawn again, so this
// never waits.
func (e *Engine) SetsHolding(host string) []string {
	var out []string
	missing := false
	for name := range ruleset.Paths(e.setsDir) {
		if !ruleset.Valid(name) {
			continue
		}
		c, known := e.cachedContent(name)
		if !known {
			missing = true
			continue
		}
		if c != nil && c.Match(host) {
			out = append(out, name)
		}
	}
	if missing && e.reading.CompareAndSwap(false, true) {
		go e.readSets()
	}
	sort.Strings(out)
	return out
}

func (e *Engine) readSets() {
	defer e.reading.Store(false)
	for name := range ruleset.Paths(e.setsDir) {
		if ruleset.Valid(name) {
			e.content(name)
		}
	}
	e.changed()
}
