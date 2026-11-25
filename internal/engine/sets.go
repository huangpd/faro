package engine

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"faro/internal/domains"
	"faro/internal/ruleset"
)

// Rule sets a person adds to a group: searched for by name in a list fetched
// from the repository, fetched when added, and from then on kept current with
// the others. A group that holds one sends every site in it to its place.

const catalogMaxAge = 7 * 24 * time.Hour

// SearchSets finds rule sets by name, for adding.
func (e *Engine) SearchSets(query string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return ruleset.Search(e.catalog, query, 8)
}

// EnsureCatalog fetches the list of rule sets in the background when there is
// none, or it is old. It is asked for as a person starts to search, and says
// nothing if it cannot: what ships can still be searched.
func (e *Engine) EnsureCatalog() {
	e.mu.Lock()
	if e.catalogBusy || e.opts.Now().Sub(e.catalogTried) < time.Hour ||
		(!ruleset.CatalogAge(e.setsDir).IsZero() && e.opts.Now().Sub(ruleset.CatalogAge(e.setsDir)) < catalogMaxAge) {
		e.mu.Unlock()
		return
	}
	e.catalogBusy, e.catalogTried = true, e.opts.Now()
	port := e.proxyPortLocked()
	e.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		names, err := e.opts.FetchCatalog(ctx, e.setsDir, port)
		e.mu.Lock()
		e.catalogBusy = false
		if err != nil {
			e.catalogErr = "规则集目录没取到：" + err.Error()
		} else {
			e.catalogErr, e.catalog = "", names
		}
		e.mu.Unlock()
		e.changed()
	}()
}

func (e *Engine) proxyPortLocked() int {
	if e.status == Running {
		return e.core.MixedPort()
	}
	return 0
}

// AddSet puts a rule set in a group. It is fetched first, if it is not here:
// a group with a set that is missing would let its sites through to wherever
// the rest go. It returns at once; the fetch is shown as busy, and its failure
// as the error.
func (e *Engine) AddSet(group, name string) error {
	if !ruleset.Valid(name) {
		return ErrGroup("不是规则集：" + name)
	}
	e.mu.Lock()
	if findGroup(e.groupsList, group) < 0 {
		e.mu.Unlock()
		return ErrGroup("没有叫 " + group + " 的分组")
	}
	if e.busy != "" {
		e.mu.Unlock()
		return ErrGroup("正在忙：" + e.busy)
	}
	have := ruleset.Size(e.setsDir, name) > 0
	if have {
		e.mu.Unlock()
		return e.putSet(group, name)
	}
	port := e.proxyPortLocked()
	e.busy, e.err = "正在取规则集 "+strings.TrimPrefix(name, "geosite-")+"…", ""
	e.mu.Unlock()
	e.changed()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		err := e.opts.FetchSet(ctx, e.setsDir, name, port)
		e.mu.Lock()
		e.busy = ""
		if err != nil {
			e.err = "规则集没取到，没有加入：" + err.Error()
		}
		e.mu.Unlock()
		if err != nil {
			e.changed()
			return
		}
		if err := e.putSet(group, name); err != nil {
			e.mu.Lock()
			e.err = err.Error()
			e.mu.Unlock()
		}
		e.changed()
	}()
	return nil
}

// putSet puts the set in the group, taking it from any other: one set is in one
// group, so that where its sites go is never in doubt.
func (e *Engine) putSet(group, name string) error {
	return e.putSets(group, []string{name})
}

// putSets puts several rule sets in a group in one change: one restart of a
// running core, not one for each.
func (e *Engine) putSets(group string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	return e.editGroups(func(gs []domains.Group, _ map[string]bool) ([]domains.Group, error) {
		i := findGroup(gs, group)
		if i < 0 {
			return nil, ErrGroup("没有叫 " + group + " 的分组")
		}
		for _, name := range names {
			for j := range gs {
				var keep []string
				for _, s := range gs[j].Sets {
					if s != name {
						keep = append(keep, s)
					}
				}
				gs[j].Sets = keep
			}
			gs[i].Sets = append(gs[i].Sets, name)
		}
		return gs, nil
	})
}

// RemoveSet takes a rule set out of a group. The file stays, as it may be
// used by the rules text.
func (e *Engine) RemoveSet(group, name string) error {
	return e.editGroups(func(gs []domains.Group, _ map[string]bool) ([]domains.Group, error) {
		i := findGroup(gs, group)
		if i < 0 {
			return nil, ErrGroup("没有叫 " + group + " 的分组")
		}
		var keep []string
		for _, s := range gs[i].Sets {
			if s != name {
				keep = append(keep, s)
			}
		}
		gs[i].Sets = keep
		return gs, nil
	})
}

// SetsDir is where the rule sets are, for showing their size.
func (e *Engine) SetsDir() string { return filepath.Join(e.core.Dir(), "rulesets") }
