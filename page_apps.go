package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"faro/internal/domains"
	"faro/internal/engine"
	"faro/internal/rules"
	"faro/internal/ruleset"
)

// An application is a group of sites (and programs, address ranges, rule sets)
// sent to one exit. They are listed under their exits on the exits page, each
// with its priority: they are checked in order, before the rules text, so
// where two claim the same site, the one in front decides.

// target is a place an application can be sent: how it is written in the
// rules, and what it is called on the screen.
type target struct{ id, label string }

// targets lists the places, with the rules' own routes after the fixed ones.
// current is added when it is not there (a route that has gone), so that an
// application that points at it still says so.
func targets(st engine.State, current string) []target {
	out := []target{{"direct", "直连"}, {rules.TargetProxy, rules.TargetProxy}, {rules.TargetAuto, rules.TargetAuto}, {rules.TargetBlock, "阻断"}}
	if st.Plan != nil {
		for _, r := range st.Plan.Routes {
			out = append(out, target{r.ID, r.ID})
		}
	}
	for _, t := range out {
		if t.id == current {
			return out
		}
	}
	if current != "" {
		out = append(out, target{current, current})
	}
	return out
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// priorityHint says, once above the exits, what the numbers are and how they
// are changed. It is said only when there are two applications to order.
func (a *app) priorityHint(c *ui.Context, st engine.State) {
	if len(st.Groups) < 2 {
		return
	}
	ui.Row(c).Gap(8).Padding(2, 4).AlignItems(ui.Center).Children(func() {
		icon(c, "list-checks", 13, a.k.text2)
		ui.Text(c, "应用前的 #编号 是优先级：同一个网站出现在几个应用里时，编号小的生效。可以在应用右侧的 ⋯ 里上移、下移来调整。").
			FontSize(fsCaption).TextColor(a.k.text2)
	})
}

// appRow is an application under its exit: its priority, what it is, what it
// holds; a click opens it to be edited, and its menu moves it up or down, to
// another exit, or out.
func (a *app) appRow(c *ui.Context, st engine.State, s section) {
	i := slices.IndexFunc(st.Groups, func(g domains.Group) bool { return g.Name == s.label })
	if i < 0 {
		return
	}
	g := st.Groups[i]
	open := a.appOpen[g.Name]
	ui.Row(c).Height(32).Gap(6).AlignItems(ui.Center).Children(func() {
		row := ui.ButtonBase(c).Grow(1).Height(32).Gap(10).Padding(0, 4).Radius(8).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(g.Name)
		if row.Hovered() {
			row.Background(a.k.surface2.Alpha(0.6))
		}
		row.Children(func() {
			mono(ui.Text(c, fmt.Sprintf("#%d", i+1)).FontSize(fsMeta).TextColor(a.k.text2)).Width(30).SingleLine().
				Tooltip(fmt.Sprintf("优先级第 %d：编号小的先匹配", i+1))
			a.appIcon(c, s)
			ui.Text(c, g.Name).FontSize(fsBody).FontWeight(500).Width(170).SingleLine().Ellipsis("…").Tooltip(g.Name)
			mono(ui.Text(c, valuesLine(s.values)).FontSize(fsMeta).TextColor(a.k.text2)).Grow(1).SingleLine().Ellipsis("…").Tooltip(strings.Join(s.values, "\n"))
			if s.app != "" {
				a.pill(c, "规则库", a.k.text2)
			} else {
				a.pill(c, "自定义", a.k.accent)
			}
			fold := "chevron-right"
			if open {
				fold = "chevron-down"
			}
			icon(c, fold, 13, a.k.text2)
		})
		if row.Clicked() {
			a.appOpen[g.Name] = !open
		}
		a.iconButton(c, "ellipsis", g.Name+"的更多操作").Menu(func(m *ui.Menu) { a.appMenu(c, st, m, i, g) })
	})
	if open {
		ui.Column(c).Gap(8).Padding(4, 0, 8, 44).Children(func() { a.appEditor(c, st, g) })
	}
}

// appMenu is what an application's ⋯ offers.
func (a *app) appMenu(c *ui.Context, st engine.State, m *ui.Menu, i int, g domains.Group) {
	if m.Item("上移（优先级提高）").Disabled(i == 0).Chosen() {
		a.exitErr = errText(a.eng.MoveGroup(g.Name, -1))
	}
	if m.Item("下移（优先级降低）").Disabled(i == len(st.Groups)-1).Chosen() {
		a.exitErr = errText(a.eng.MoveGroup(g.Name, 1))
	}
	m.Separator()
	m.Submenu("移到其他出口", func(m *ui.Menu) {
		for _, t := range targets(st, g.Target) {
			if t.id == g.Target {
				continue
			}
			if m.Item(t.label).Chosen() {
				if err := a.eng.SetGroupTarget(g.Name, t.id); err != nil {
					a.exitErr = err.Error()
				} else {
					a.exitErr = ""
					a.exitOpen[t.id] = true // shown where it went
					c.Toast(g.Name + " 已移到 " + t.label)
				}
			}
		}
	})
	m.Separator()
	if m.Item("删除").Chosen() {
		a.removeApp, a.removeAsk = g.Name, true
	}
}

// appEditor is an open application: what it holds, its rule sets, and where
// it claims what another does.
func (a *app) appEditor(c *ui.Context, st engine.State, g domains.Group) {
	tok := a.domTokens[g.Name]
	if tok == nil {
		tok = new([]string)
		a.domTokens[g.Name] = tok
	}
	*tok = append((*tok)[:0], engine.Items(g)...)
	if ui.TokenField(c, tok, []string{}).Label(g.Name + "的内容").Placeholder("添加域名，或 process:App、cidr:1.2.3.0/24").Changed() {
		a.exitErr = errText(a.eng.SetGroupItems(g.Name, *tok))
	}
	a.groupSets(c, st, g)
	a.groupOverlaps(c, st, g)
}

// valuesLine is the first values of an application, and how many there are.
func valuesLine(values []string) string {
	what := strings.Join(values[:min(len(values), 4)], " · ")
	if len(values) > 4 {
		what += fmt.Sprintf(" 等 %d 项", len(values))
	}
	return what
}

// groupOverlaps says where this application and another claim the same sites
// while sending them to different places, which of the two decides, and offers
// to put this one in front.
func (a *app) groupOverlaps(c *ui.Context, st engine.State, g domains.Group) {
	num := func(name string) string {
		if i := slices.IndexFunc(st.Groups, func(x domains.Group) bool { return x.Name == name }); i >= 0 {
			return fmt.Sprintf("#%d", i+1)
		}
		return ""
	}
	for _, o := range st.Overlaps {
		var text string
		behind := false
		switch g.Name {
		case o.Winner:
			text = fmt.Sprintf("与「%s」%s 重叠 %d 处，如 %s；本应用排在前面，由它决定", o.Other, num(o.Other), o.Count, o.Example)
		case o.Other:
			text = fmt.Sprintf("与「%s」%s 重叠 %d 处，如 %s；「%s」排在前面，由它决定", o.Winner, num(o.Winner), o.Count, o.Example, o.Winner)
			behind = true
		default:
			continue
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			icon(c, "shield-alert", 14, a.k.warn)
			ui.Text(c, text).FontSize(fsMeta).TextColor(a.k.warn).Grow(1).Selectable()
			if behind && a.button(c, btnSecondary, "arrow-up", "让本应用优先").Label("让 "+g.Name+" 排到 "+o.Winner+" 前面").Clicked() {
				a.exitErr = errText(a.eng.MoveGroupBefore(g.Name, o.Winner))
			}
		})
	}
}

// groupSets shows the rule sets an application holds, and a search to add
// more: what is typed finds names in the list, and one click puts a set in.
func (a *app) groupSets(c *ui.Context, st engine.State, g domains.Group) {
	if len(g.Sets) > 0 {
		ui.Row(c).Gap(6).Wrap().Children(func() {
			for _, name := range g.Sets {
				short := strings.TrimPrefix(name, "geosite-")
				ui.Row(c).Gap(6).AlignItems(ui.Center).Padding(2, 4, 2, 10).Radius(999).Background(a.k.surface2).Border(1, a.k.border).Children(func() {
					icon(c, "globe", 12, a.k.text2)
					ui.Text(c, short).FontSize(fsMeta).Font(monoFont).SingleLine()
					ui.Text(c, sizeText(ruleset.Size(a.eng.SetsDir(), name))).FontSize(fsCaption).TextColor(a.k.text2).SingleLine()
					if a.iconButton(c, "x", "移除规则集 "+short).Clicked() {
						a.exitErr = errText(a.eng.RemoveSet(g.Name, name))
					}
				})
			}
		})
	}
	q := a.setQuery[g.Name]
	if q == nil {
		q = new(string)
		a.setQuery[g.Name] = q
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		icon(c, "search", 14, a.k.text2)
		ui.TextInput(c, q).Label(g.Name + "的规则集搜索").Placeholder("搜索规则集，加到这个应用").Grow(1)
	})
	if strings.TrimSpace(*q) == "" {
		return
	}
	a.eng.EnsureCatalog()
	found := a.eng.SearchSets(*q)
	if len(found) == 0 {
		msg := "没有匹配的规则集"
		if st.CatalogErr != "" {
			msg = st.CatalogErr
		}
		ui.Text(c, msg).FontSize(fsMeta).TextColor(a.k.text2)
		return
	}
	for _, name := range found {
		held := slices.Contains(g.Sets, name)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, strings.TrimPrefix(name, "geosite-")).FontSize(fsBody).Font(monoFont).Grow(1).SingleLine()
			if n := ruleset.Size(a.eng.SetsDir(), name); n > 0 {
				ui.Text(c, sizeText(n)).FontSize(fsCaption).TextColor(a.k.text2)
			}
			switch {
			case held:
				a.pill(c, "已在应用里", a.k.accent)
			case a.button(c, btnSecondary, "plus", "添加").Disabled(st.Busy != "").Clicked():
				a.exitErr = errText(a.eng.AddSet(g.Name, name))
				*q = ""
			}
		})
	}
}

// sizeText is the size of a file as a person reads it.
func sizeText(n int64) string {
	switch {
	case n <= 0:
		return ""
	case n < 1<<20:
		return fmt.Sprintf("%d KB", max(1, n>>10))
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

// addFromConn is a row under a connection: the application its site is put
// in, or a new one, made under the exit chosen.
func (a *app) addFromConn(c *ui.Context, st engine.State, host string) {
	site := domains.Base(host)
	if _, ok := domains.One(site); !ok {
		return // an address, not a name
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "加入应用").FontSize(fsMeta).TextColor(a.k.text2).Width(64)
		ui.Row(c).Gap(6).Grow(1).Wrap().Children(func() {
			a.pill(c, site, a.k.accent)
			for _, g := range st.Groups {
				if a.button(c, btnSecondary, "", g.Name).Tooltip(g.Name + " ← " + site).Clicked() {
					if err := a.eng.AddDomains(g.Name, site); err != nil {
						a.exitErr = err.Error()
					} else {
						c.Toast(site + " 已加入 " + g.Name)
					}
				}
			}
			a.button(c, btnGhost, "plus", "新建应用").Menu(func(m *ui.Menu) {
				for _, t := range targets(st, "") {
					if m.Item("在 " + t.label + " 下新建").Chosen() {
						a.newAppFor(t.id, site)
					}
				}
			})
		})
	})
}

// newAppFor opens the exits page on an exit's「添加应用」, with a site written in.
func (a *app) newAppFor(exit, site string) {
	a.setPage(pageRules)
	a.rulesTab = tabExits
	a.exitOpen[exit] = true
	a.addingTo, a.appQuery, a.appErr = exit, "", ""
	a.customName, a.customText = "", site
}
