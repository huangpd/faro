package engine

import (
	"context"
	"strings"
	"time"

	"faro/internal/applib"
	"faro/internal/domains"
	"faro/internal/rules"
	"faro/internal/ruleset"
)

// An application on an exit is a group: a name, the exit, and its sites, rule
// sets and other rules. One comes from the library, or is written by hand.

// AddApp puts the library's application id on the exit target. Its rule sets
// that are not here yet are fetched after; until then its sites stand for it.
func (e *Engine) AddApp(target, id string) error {
	a, ok := applib.Find(id)
	if !ok {
		return ErrGroup("规则库里没有 " + id)
	}
	return e.addApp(target, a.Name, id, a.Domains(), a.Sets(), a.Others())
}

// AddCustomApp puts an application written by hand on the exit target: a
// name, and what it is, as sites (example.com) or kind:value of the rules
// language, separated by spaces or lines.
func (e *Engine) AddCustomApp(target, name, text string) error {
	var sites, sets, others []string
	for _, tok := range applib.Tokens(strings.NewReplacer("，", " ", ",", " ").Replace(text)) {
		kind, val, ok := strings.Cut(tok, ":")
		if !ok || strings.Contains(tok, "://") {
			kind, val = "domain", tok
		}
		switch kind {
		case "domain":
			d, ok := domains.One(val)
			if !ok {
				return ErrGroup("不是域名：" + tok)
			}
			sites = append(sites, d)
		case "set":
			if !ruleset.Valid(val) {
				return ErrGroup("不是规则集：" + val)
			}
			sets = append(sets, val)
		default:
			others = append(others, kind+":"+val)
		}
	}
	if len(sites)+len(sets)+len(others) == 0 {
		return ErrGroup("写上这个应用的域名，或者 process:、cidr: 这样的条件")
	}
	head, _ := domains.Head([]domains.Group{{Name: name, Target: rules.TargetProxy, Rules: others}})
	if _, errs := rules.Compile(head, nil, nil); errs != nil {
		return ErrGroup(errs[0].Msg)
	}
	return e.addApp(target, name, "", sites, sets, others)
}

func (e *Engine) addApp(target, name, id string, sites, sets, others []string) error {
	name = strings.TrimSpace(name)
	e.mu.Lock()
	var have, fetch []string
	for _, s := range sets {
		if ruleset.Size(e.setsDir, s) > 0 {
			have = append(have, s)
		} else {
			fetch = append(fetch, s)
		}
	}
	e.mu.Unlock()
	err := e.editGroups(func(gs []domains.Group, ids map[string]bool) ([]domains.Group, error) {
		switch {
		case name == "" || len([]rune(name)) > 30:
			return nil, ErrGroup("请给应用起个名字，不超过 30 个字")
		case findGroup(gs, name) >= 0:
			return nil, ErrGroup("已经有叫 " + name + " 的应用")
		}
		if err := checkTarget(target, ids); err != nil {
			return nil, err
		}
		gs = append(gs, domains.Group{Name: name, Target: target, App: id, Rules: others})
		place(gs, len(gs)-1, sites)
		return gs, nil
	})
	if err != nil {
		return err
	}
	for _, s := range have {
		if err := e.putSet(name, s); err != nil {
			return err
		}
	}
	if len(fetch) > 0 {
		go e.fetchSetsFor(name, fetch)
	}
	return nil
}

// fetchSetsFor fetches rule sets and puts each that came in the group. What
// did not come is said; the group works by its sites meanwhile.
func (e *Engine) fetchSetsFor(group string, names []string) {
	var failed []string
	for _, name := range names {
		e.mu.Lock()
		port := e.proxyPortLocked()
		e.busy = "正在取规则集 " + strings.TrimPrefix(name, "geosite-") + "…"
		e.mu.Unlock()
		e.changed()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		err := e.opts.FetchSet(ctx, e.setsDir, name, port)
		cancel()
		if err == nil {
			err = e.putSet(group, name)
		}
		if err != nil {
			failed = append(failed, strings.TrimPrefix(name, "geosite-"))
		}
	}
	e.mu.Lock()
	e.busy = ""
	if len(failed) > 0 {
		e.err = group + "：规则集 " + strings.Join(failed, "、") + " 没取到，先按它的域名走"
	}
	e.mu.Unlock()
	e.changed()
}
