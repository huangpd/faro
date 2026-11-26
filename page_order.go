package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"faro/internal/engine"
	"faro/internal/rules"
)

// The tabs of the rules page.
const (
	tabExits = iota // the exits, and the applications sent to each
	tabOrder
	tabText
)

// orderPage lists the rules in force as the core checks them, top to bottom,
// the first that matches deciding. Each says where it sends, what it is for and
// what it matches; a value that a rule above takes first, and sends elsewhere,
// is said under it. It is made from the groups and the rules text, and is only
// to read: what is changed is changed there.
func (a *app) orderPage(c *ui.Context, st engine.State) {
	key := fmt.Sprintf("%d|%s|%v|%v", st.PlanRev, st.RulesText, st.Groups, st.GroupErrors) // a new plan for new nodes, choices, rules or groups
	if a.orderFor != key {
		a.orderFor, a.order = key, a.eng.Order()
	}
	if st.Plan == nil {
		a.empty(c, "list-checks", "规则没有生效", "先在「规则文本」里改正错误", nil)
		return
	}
	n := 0
	for _, s := range a.order {
		n += len(s.Shadowed)
	}
	if n > 0 {
		ui.Row(c).Children(func() { a.pill(c, fmt.Sprintf("%d 处被上面的规则挡住", n), a.k.warn) })
	}
	hues := map[string]ui.Color{}
	for i, r := range st.Plan.Routes {
		hues[r.ID] = a.k.lanes[i%len(a.k.lanes)]
	}
	ui.Scroll(c).Grow(1).Children(func() {
		a.card(c).Padding(6, 0).Gap(0).Children(func() {
			a.orderRow(c, hues, "", "direct", "", "内置", "局域网与私有地址", nil, "")
			for _, b := range orderBlocks(a.order) {
				ui.Divider(c)
				c.Key("order:" + b.key)
				a.blockRow(c, hues, b)
			}
			ui.Divider(c)
			a.orderRow(c, hues, "", rules.TargetProxy, "", "最后", "其余所有流量", nil, "")
		})
	})
}

// An orderBlock is steps that are one thing to a person: the guards of one
// route, or the rules of one group, side by side. The core needs a rule for
// each kind of value (the kinds of one rule must all match, a group's are
// "or") and a guard for each of them; they are shown as one, and opened to
// see each.
type orderBlock struct {
	key   string
	first int // the index of its first step
	steps []engine.Step
}

// orderBlocks gathers the steps into blocks: the guards of a route together,
// the steps of a group together, each step of the rules text alone.
func orderBlocks(steps []engine.Step) []orderBlock {
	var out []orderBlock
	for i, s := range steps {
		key := fmt.Sprintf("step:%d", i)
		switch {
		case s.Guard != "":
			key = "guard:" + s.Guard
		case s.Group != "":
			key = "group:" + s.Group
		}
		if n := len(out); n > 0 && out[n-1].key == key {
			out[n-1].steps = append(out[n-1].steps, s)
			continue
		}
		out = append(out, orderBlock{key: key, first: i, steps: []engine.Step{s}})
	}
	return out
}

// blockRow is a block in a row: its steps' numbers, where it sends, what it is
// and what it holds, and the values out of reach, which are never folded away.
// A block of more than one step opens to show them.
func (a *app) blockRow(c *ui.Context, hues map[string]ui.Color, b orderBlock) {
	s := b.steps[0]
	num := fmt.Sprint(b.first + 1)
	if len(b.steps) > 1 {
		num = fmt.Sprintf("%d–%d", b.first+1, b.first+len(b.steps))
	}
	label := s.Label
	if label == "" {
		label = "规则文本"
	}
	where := ""
	if s.Line > 0 {
		where = fmt.Sprintf("第 %d 行", s.Line)
	}
	var shadows []string
	for _, x := range b.steps {
		shadows = append(shadows, a.shadowLines(x)...)
	}
	if len(b.steps) == 1 {
		a.orderRow(c, hues, num, s.Target, s.Route, label, matchText(s), shadows, where)
		return
	}
	open := a.orderOpen[b.key]
	row := ui.ButtonBase(c).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(label)
	if row.Hovered() {
		row.Background(a.k.surface2.Alpha(0.5))
	}
	row.Children(func() {
		ui.Column(c).Grow(1).Children(func() {
			a.orderRow(c, hues, num, s.Target, s.Route, label, blockText(b), shadows, where)
		})
		chevron := "chevron-right"
		if open {
			chevron = "chevron-down"
		}
		ui.Box(c).Padding(0, 16, 0, 0).Children(func() { icon(c, chevron, 14, a.k.text2) })
	})
	if row.Clicked() {
		a.orderOpen[b.key] = !open
	}
	if open {
		// Each step under the row's columns: its number, the group a guard stands
		// in front of, and what it matches.
		ui.Column(c).Padding(0, 0, 6, 0).Children(func() {
			for i, x := range b.steps {
				ui.Row(c).Gap(12).Padding(0, 16).Height(22).AlignItems(ui.Center).Children(func() {
					mono(ui.Text(c, fmt.Sprint(b.first+i+1)).FontSize(fsCaption).TextColor(a.k.text2)).Width(46).SingleLine()
					ui.Box(c).Width(96)
					ui.Text(c, x.GuardOf).FontSize(fsCaption).TextColor(a.k.text2).Width(200).SingleLine().Ellipsis("…")
					mono(ui.Text(c, matchText(x)).FontSize(fsCaption).TextColor(a.k.text2)).Grow(1).SingleLine().Ellipsis("…").Tooltip(matchText(x))
				})
			}
		})
	}
}

// blockText says what a block of steps holds, in a line. A route's guards:
// what they refuse, and the groups they stand in front of. A group's steps:
// how many values of each kind, rule sets by name.
func blockText(b orderBlock) string {
	if b.steps[0].Guard != "" {
		var refuse, of []string
		seen := map[string]bool{}
		for _, s := range b.steps {
			w := ""
			switch {
			case s.Network != "":
				w = strings.ToUpper(s.Network)
			case s.IPVersion != 0:
				w = fmt.Sprintf("IPv%d", s.IPVersion)
			}
			if w != "" && !seen["n:"+w] {
				seen["n:"+w] = true
				refuse = append(refuse, w)
			}
			if s.GuardOf != "" && !seen["g:"+s.GuardOf] {
				seen["g:"+s.GuardOf] = true
				of = append(of, s.GuardOf)
			}
		}
		text := "拒绝 " + strings.Join(refuse, " 和 ")
		if len(of) > 0 {
			text += " · 守住 " + strings.Join(of, "、")
		}
		return text
	}
	// A kind with a value or two says them; more, how many.
	plural := map[string]string{
		"domain_suffix": "个域名", "domain": "个完整域名", "domain_keyword": "个关键词", "domain_regex": "个正则",
		"ip_cidr": "个 IP 段", "process_name": "个进程", "process_path_regex": "个路径", "port": "个端口",
	}
	single := map[string]string{
		"domain": "完整 ", "domain_keyword": "关键词 ", "domain_regex": "正则 ", "rule_set": "规则集 ",
		"rule_set_ip": "规则集 ", "process_name": "进程 ", "process_path_regex": "路径 ", "port": "端口 ",
	}
	values := map[string][]string{}
	var kinds []string
	for _, s := range b.steps {
		for _, m := range s.Matchers {
			if _, ok := values[m.Kind]; !ok {
				kinds = append(kinds, m.Kind)
			}
			values[m.Kind] = append(values[m.Kind], m.Values...)
		}
	}
	var parts []string
	for _, k := range kinds {
		vs := values[k]
		if len(vs) <= 2 || k == "rule_set" || k == "rule_set_ip" { // a rule set is said by name
			parts = append(parts, single[k]+strings.Join(vs, "、"))
		} else {
			parts = append(parts, fmt.Sprintf("%d %s", len(vs), plural[k]))
		}
	}
	return strings.Join(parts, " · ")
}

// shadowLines says, for each value of a step out of reach, which step takes it.
func (a *app) shadowLines(s engine.Step) []string {
	var out []string
	for _, x := range s.Shadowed {
		by := a.order[x.By]
		out = append(out, fmt.Sprintf("%s 到不了这里：第 %d 条先匹配，去了 %s", x.Value, x.By+1, targetName(by.Target)))
	}
	return out
}

// orderRow is one step. route is set when the step blocks because the route it
// was written for has no node: it says both.
func (a *app) orderRow(c *ui.Context, hues map[string]ui.Color, num, target, route, label, what string, shadows []string, where string) {
	ui.Column(c).Padding(8, 16).Gap(4).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			mono(ui.Text(c, num).FontSize(fsMeta).TextColor(a.k.text2)).Width(46).SingleLine()
			ui.Box(c).Width(96).Children(func() {
				if route != "" {
					a.pill(c, route+" · 无节点", a.k.danger).Tooltip(route + " 没有符合的节点，发往它的流量被阻断")
				} else {
					a.pill(c, targetName(target), targetHue(a, hues, target))
				}
			})
			ui.Text(c, label).FontSize(fsBody).FontWeight(500).Width(200).SingleLine().Ellipsis("…").Tooltip(label)
			mono(ui.Text(c, what).FontSize(fsMeta).TextColor(a.k.text2)).Grow(1).SingleLine().Ellipsis("…").Tooltip(what)
			if where != "" {
				ui.Text(c, where).FontSize(fsCaption).TextColor(a.k.text2).SingleLine()
			}
		})
		for _, sh := range shadows {
			ui.Row(c).Gap(6).Padding(0, 0, 0, 130).AlignItems(ui.Center).Children(func() {
				icon(c, "shield-alert", 12, a.k.warn)
				ui.Text(c, sh).FontSize(fsCaption).TextColor(a.k.warn).Selectable()
			})
		}
	})
}

func targetName(t string) string {
	switch t {
	case rules.TargetBlock:
		return "阻断"
	case rules.TargetDirect:
		return "直连"
	}
	return t
}

func targetHue(a *app, hues map[string]ui.Color, t string) ui.Color {
	switch t {
	case rules.TargetBlock:
		return a.k.danger
	case rules.TargetDirect:
		return a.k.ok
	case rules.TargetProxy, rules.TargetAuto:
		return a.k.text2
	}
	if h, ok := hues[t]; ok {
		return h
	}
	return a.k.info
}

// matchText says what a step matches, in a line: its flags, then its values,
// each marked with its kind where the kind is not a site.
func matchText(s engine.Step) string {
	var parts []string
	if s.Network != "" {
		parts = append(parts, strings.ToUpper(s.Network))
	}
	if s.IPVersion != 0 {
		parts = append(parts, fmt.Sprintf("IPv%d", s.IPVersion))
	}
	kind := map[string]string{
		"domain": "完整 ", "domain_keyword": "关键词 ", "domain_regex": "正则 ", "rule_set": "规则集 ",
		"rule_set_ip": "规则集 ", "process_name": "进程 ", "process_path_regex": "路径 ", "port": "端口 ",
	}
	for _, m := range s.Matchers {
		for _, v := range m.Values {
			parts = append(parts, kind[m.Kind]+v)
		}
	}
	return strings.Join(parts, " · ")
}
