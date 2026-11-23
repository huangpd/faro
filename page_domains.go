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

// The person's own sites, in groups: each group a name, a place its sites go
// to, and the sites. They are checked before the rules text, so that what is
// added day to day decides, whatever the text says further down.

// target is a place a group can send its sites: how it is written in the
// rules, and what it is called on the screen.
type target struct{ id, label string }

// targets lists the places, with the rules' own routes after the fixed ones.
// current is added when it is not there (a route that has gone), so that a
// group that points at it still says so.
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

func targetLabel(ts []target, id string) string {
	for _, t := range ts {
		if t.id == id {
			return t.label
		}
	}
	return id
}

func labelsOf(ts []target) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.label
	}
	return out
}

// targetPicker is a drop-down of places; the choice is written to *id.
func (a *app) targetPicker(c *ui.Context, key string, ts []target, id *string, label string) (changed bool) {
	sel := a.domSel[key]
	if sel == nil {
		sel = new(string)
		a.domSel[key] = sel
	}
	*sel = targetLabel(ts, *id)
	if ui.Select(c, sel, labelsOf(ts)).Label(label).Width(130).Changed() {
		for _, t := range ts {
			if t.label == *sel {
				*id = t.id
				return true
			}
		}
	}
	return false
}

func (a *app) domainsPage(c *ui.Context, st engine.State) {
	if a.domErr != "" {
		a.banner(c, a.k.danger, "shield-alert", a.domErr)
	}
	for _, g := range st.Groups {
		if msg := st.GroupErrors[g.Name]; msg != "" {
			a.banner(c, a.k.warn, "shield-alert", g.Name+"：没有生效，"+msg)
		}
	}
	if len(st.Groups) == 0 && !a.newGroup.open {
		a.empty(c, "list-checks", "还没有分组", "", func() {
			if a.button(c, btnPrimary, "plus", "新建分组").Clicked() {
				a.newGroup.open = true
			}
		})
		return
	}
	ui.Scroll(c).Grow(1).Gap(12).Children(func() {
		for i, g := range st.Groups {
			a.groupCard(c, st, i, g)
		}
		a.newGroupForm(c, st)
	})

	switch ui.AlertDialog(c, &a.delAsk, "删除分组 "+a.delName+"？", "组里的域名会一起删除。", "取消", "删除") {
	case 1:
		a.domErr = errText(a.eng.DeleteGroup(a.delName))
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (a *app) groupCard(c *ui.Context, st engine.State, i int, g domains.Group) {
	ts := targets(st, g.Target)
	a.card(c).Gap(10).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, g.Name).FontSize(fsHeading).FontWeight(600).Grow(1)
			ui.Text(c, "→").FontSize(fsBody).TextColor(a.k.text2)
			to := g.Target
			if a.targetPicker(c, "g:"+g.Name, ts, &to, g.Name+"的去向") {
				a.domErr = errText(a.eng.SetGroupTarget(g.Name, to))
			}
			a.iconButton(c, "ellipsis", g.Name+"的更多操作").Menu(func(m *ui.Menu) {
				if m.Item("上移").Disabled(i == 0).Chosen() {
					a.domErr = errText(a.eng.MoveGroup(g.Name, -1))
				}
				if m.Item("下移").Disabled(i == len(st.Groups)-1).Chosen() {
					a.domErr = errText(a.eng.MoveGroup(g.Name, 1))
				}
				m.Separator()
				if m.Item("删除分组").Chosen() {
					a.delName, a.delAsk = g.Name, true
				}
			})
		})
		// The sites are chips; what is typed or pasted is added on Enter or a comma.
		tok := a.domTokens[g.Name]
		if tok == nil {
			tok = new([]string)
			a.domTokens[g.Name] = tok
		}
		*tok = append((*tok)[:0], g.Domains...)
		if ui.TokenField(c, tok, []string{}).Label(g.Name + "的域名").Placeholder("添加域名").Changed() {
			a.domErr = errText(a.eng.SetGroupDomains(g.Name, *tok))
		}
		a.groupSets(c, st, g)
		a.groupOverlaps(c, st, g)
	})
}

// groupOverlaps says where this group and another claim the same sites while
// sending them to different places: the one checked first decides them.
func (a *app) groupOverlaps(c *ui.Context, st engine.State, g domains.Group) {
	for _, o := range st.Overlaps {
		var text string
		switch g.Name {
		case o.Winner:
			text = fmt.Sprintf("与「%s」重叠 %d 处，如 %s；本组优先", o.Other, o.Count, o.Example)
		case o.Other:
			text = fmt.Sprintf("与「%s」重叠 %d 处，如 %s；「%s」优先", o.Winner, o.Count, o.Example, o.Winner)
		default:
			continue
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			icon(c, "shield-alert", 14, a.k.warn)
			ui.Text(c, text).FontSize(fsMeta).TextColor(a.k.warn).Selectable()
		})
	}
}

// groupSets shows the rule sets a group holds, and a search to add more: what
// is typed finds names in the list, and one click puts a set in the group.
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
						a.domErr = errText(a.eng.RemoveSet(g.Name, name))
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
		ui.TextInput(c, q).Label(g.Name + "的规则集搜索").Placeholder("搜索规则集").Grow(1)
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
				a.pill(c, "已在组里", a.k.accent)
			case a.button(c, btnSecondary, "plus", "添加").Disabled(st.Busy != "").Clicked():
				a.domErr = errText(a.eng.AddSet(g.Name, name))
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

// newGroupForm is a button that opens a card for the name and the place; sites
// that came from a connection are put in the group at once.
func (a *app) newGroupForm(c *ui.Context, st engine.State) {
	if !a.newGroup.open {
		ui.Row(c).Children(func() { // as wide as its label, not the page
			if a.button(c, btnSecondary, "plus", "新建分组").Clicked() {
				a.newGroup.open = true
			}
		})
		return
	}
	ts := targets(st, "")
	if a.newGroup.target == "" {
		a.newGroup.target = ts[0].id
	}
	a.card(c).Gap(10).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			name := ui.TextInput(c, &a.newGroup.name).Label("分组名").Placeholder("分组名，例如 AI 应用").Grow(1)
			a.targetPicker(c, "new", ts, &a.newGroup.target, "新分组的去向")
			create := a.button(c, btnPrimary, "check", "创建")
			if create.Clicked() || name.Submitted() {
				if err := a.eng.NewGroup(a.newGroup.name, a.newGroup.target, a.newGroup.sites); err != nil {
					a.domErr = err.Error()
				} else {
					a.domErr = ""
					a.newGroup = newGroupDraft{}
				}
			}
			if a.button(c, btnGhost, "", "取消").Clicked() {
				a.newGroup = newGroupDraft{}
			}
		})
		if a.newGroup.sites != "" {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "加入").FontSize(fsMeta).TextColor(a.k.text2)
				a.pill(c, a.newGroup.sites, a.k.accent)
			})
		}
	})
}

// newGroupDraft is a group that is being made.
type newGroupDraft struct {
	open   bool
	name   string
	target string
	sites  string // sites to put in it when made, as from a connection
}

// addFromConn is a row under a connection: which group the site is put in.
func (a *app) addFromConn(c *ui.Context, st engine.State, host string) {
	site := domains.Base(host)
	if _, ok := domains.One(site); !ok {
		return // an address, not a name
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "加入分组").FontSize(fsMeta).TextColor(a.k.text2).Width(64)
		ui.Row(c).Gap(6).Grow(1).Wrap().Children(func() {
			a.pill(c, site, a.k.accent)
			for _, g := range st.Groups {
				if a.button(c, btnSecondary, "", g.Name).Tooltip(g.Name + " ← " + site).Clicked() {
					if err := a.eng.AddDomains(g.Name, site); err != nil {
						a.domErr = err.Error()
					} else {
						c.Toast(site + " 已加入 " + g.Name)
					}
				}
			}
			if a.button(c, btnGhost, "plus", "新建分组").Clicked() {
				a.newGroup = newGroupDraft{open: true, sites: site}
				a.rulesTab = 0
				a.setPage(pageRules)
			}
		})
	})
}
