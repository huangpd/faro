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
	tabDomains = iota
	tabOrder
	tabText
)

// orderPage lists the rules in force as the core checks them, top to bottom,
// the first that matches deciding. Each says where it sends, what it is for and
// what it matches; a value that a rule above takes first, and sends elsewhere,
// is said under it. It is made from the groups and the rules text, and is only
// to read: what is changed is changed there.
func (a *app) orderPage(c *ui.Context, st engine.State) {
	key := fmt.Sprint(st.RulesText, st.Groups, st.GroupErrors, len(st.Nodes))
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
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "流量从上往下比对，第一条匹配的决定去向。这里只能看；要改，去「我的域名」或「规则文本」。").FontSize(fsMeta).TextColor(a.k.text2).Grow(1)
		if n > 0 {
			a.pill(c, fmt.Sprintf("%d 处被上面的规则挡住", n), a.k.warn)
		}
	})
	hues := map[string]ui.Color{}
	for i, r := range st.Plan.Routes {
		hues[r.ID] = a.k.lanes[i%len(a.k.lanes)]
	}
	ui.Scroll(c).Grow(1).Children(func() {
		a.card(c).Padding(6, 0).Gap(0).Children(func() {
			a.orderRow(c, hues, "", "direct", "", "内置", "局域网与私有地址", nil, "")
			for i, s := range a.order {
				ui.Divider(c)
				where := ""
				if s.Line > 0 {
					where = fmt.Sprintf("第 %d 行", s.Line)
				}
				label := s.Label
				if label == "" {
					label = "规则文本"
				}
				a.orderRow(c, hues, fmt.Sprint(i+1), s.Target, s.Route, label, matchText(s), a.shadowLines(s), where)
			}
			ui.Divider(c)
			a.orderRow(c, hues, "", rules.TargetProxy, "", "最后", "其余所有流量", nil, "")
		})
	})
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
			mono(ui.Text(c, num).FontSize(fsMeta).TextColor(a.k.text2)).Width(22)
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
