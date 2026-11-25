package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"faro/internal/engine"
	"faro/internal/rules"
	"faro/internal/singcfg"
)

// The exits tab: each exit a card, folded to a line or open, with what leaves
// by it and, on its card, its settings. An exit's settings are only on the
// exit: nothing is inherited. They are written to its line in the rules text.

// exitDraft is an exit's settings while they are being changed.
type exitDraft struct {
	rl       rules.RouteLine
	newExit  string
	every    string // a label of checkEvery
	span     string // "全天" or "指定时段"
	from, to string
	errs     []rules.Error
}

// checkEvery are the times an exit can be checked at, as written and as said.
var checkEvery = [][2]string{{"30s", "每 30 秒"}, {"1m", "每 1 分钟"}, {"2m", "每 2 分钟"}, {"5m", "每 5 分钟"}, {"15m", "每 15 分钟"}, {"30m", "每 30 分钟"}, {"1h", "每 1 小时"}}

const (
	allDay  = "全天"
	someDay = "指定时段"
)

func everyLabel(v string) string {
	if v == "" {
		v = "2m"
	}
	for _, e := range checkEvery {
		if e[0] == v {
			return e[1]
		}
	}
	return "每 " + v
}

func everyValue(label string) string {
	for _, e := range checkEvery {
		if e[1] == label {
			if e[0] == "2m" {
				return "" // the default is not written
			}
			return e[0]
		}
	}
	return strings.TrimPrefix(label, "每 ")
}

// section is the rules of one exit that share a heading: what is called an
// application on the card.
type section struct {
	label  string
	group  bool // from「我的域名」, not the rules text
	values []string
	shadow []string
}

func (a *app) exitsPage(c *ui.Context, st engine.State) {
	key := fmt.Sprint(st.RulesText, st.Groups, st.GroupErrors, len(st.Nodes))
	if a.orderFor != key {
		a.orderFor, a.order = key, a.eng.Order()
	}
	if st.Plan == nil {
		a.empty(c, "list-checks", "规则没有生效", "先在「规则文本」里改正错误", nil)
		return
	}
	ui.Scroll(c).Grow(1).Gap(12).Children(func() {
		// In the order they are written; each keeps the hue it has on the「现在」page.
		routes := make([]int, len(st.Plan.Routes))
		for i := range routes {
			routes[i] = i
		}
		sort.SliceStable(routes, func(x, y int) bool { return st.Plan.Routes[routes[x]].Line < st.Plan.Routes[routes[y]].Line })
		for _, i := range routes {
			a.exitCard(c, st, st.Plan.Routes[i], a.k.lanes[i%len(a.k.lanes)])
		}
		a.plainExitCard(c, st, rules.TargetProxy, a.k.text2.Alpha(0.5))
		a.plainExitCard(c, st, rules.TargetDirect, a.k.ok.Alpha(0.6))
	})
}

// sections gathers the steps that send to target by their heading, in order.
func (a *app) sections(target string) []section {
	var out []section
	at := map[string]int{}
	for _, s := range a.order {
		if s.Target != target && s.Route != target {
			continue
		}
		label := s.Label
		if label == "" {
			label = "规则文本"
		}
		i, ok := at[label]
		if !ok {
			i = len(out)
			at[label] = i
			name, group := strings.CutPrefix(label, "分组 · ")
			out = append(out, section{label: name, group: group})
		}
		for _, m := range s.Matchers {
			for _, v := range m.Values {
				out[i].values = append(out[i].values, valueText(m.Kind, v))
			}
		}
		out[i].shadow = append(out[i].shadow, a.shadowLines(s)...)
	}
	return out
}

func valueText(kind, v string) string {
	switch kind {
	case "process_name":
		return "进程 " + v
	case "process_path_regex":
		return "路径 " + v
	case "rule_set", "rule_set_ip":
		return "规则集 " + v
	case "domain_keyword":
		return "关键词 " + v
	case "port":
		return "端口 " + v
	}
	return v
}

// guards says what else holds an exit: blocks written for its traffic, over
// UDP and over IPv6, found among the block rules that share a value with it.
func (a *app) guards(id string) (udp, v6 bool) {
	mine := map[string]bool{}
	for _, s := range a.order {
		if s.Target == id {
			for _, m := range s.Matchers {
				for _, v := range m.Values {
					mine[v] = true
				}
			}
		}
	}
	for _, s := range a.order {
		if s.Target != rules.TargetBlock || s.Route != "" {
			continue
		}
		for _, m := range s.Matchers {
			for _, v := range m.Values {
				if mine[v] {
					udp = udp || s.Network == "udp"
					v6 = v6 || s.IPVersion == 6
				}
			}
		}
	}
	return udp, v6
}

func (a *app) guardPills(c *ui.Context, r rules.Route) {
	udp, v6 := a.guards(r.ID)
	if r.Gate {
		a.pill(c, "熔断门", a.k.info)
	}
	if r.IPv4 {
		a.pill(c, "仅 IPv4", a.k.info)
	}
	if udp {
		a.pill(c, "阻断 UDP", a.k.info)
	}
	if v6 {
		a.pill(c, "阻断 IPv6", a.k.info)
	}
	if len(r.Exit) > 0 {
		a.pill(c, "出口须在 "+strings.Join(r.Exit, "、"), a.k.ok)
	}
}

func (a *app) exitNode(st engine.State, r rules.Route) string {
	switch {
	case r.Kind == "like":
		return "跟随 " + r.Like
	case len(r.Nodes) == 0:
		return "没有符合的节点"
	case r.Chosen == "":
		return fastLabel
	}
	return shorten(r.Chosen, 34)
}

// exitCard is a route: a line that folds and unfolds it, its guards, its
// settings when open, and what is sent to it.
func (a *app) exitCard(c *ui.Context, st engine.State, r rules.Route, hue ui.Color) {
	open := a.exitOpen[r.ID]
	secs := a.sections(r.ID)
	edge := a.k.border
	if len(r.Nodes) == 0 {
		edge = a.k.danger.Alpha(0.45)
	}
	ui.Row(c).Background(a.k.surface).Border(1, edge).Radius(16).AlignItems(ui.Stretch).Clip().Children(func() {
		ui.Box(c).Width(4).Background(hue)
		ui.Column(c).Grow(1).Padding(12, 16).Gap(10).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				head := ui.ButtonBase(c).Grow(1).Gap(10).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(r.ID)
				head.Children(func() {
					fold := "chevron-right"
					if open {
						fold = "chevron-down"
					}
					icon(c, fold, 14, a.k.text2)
					ui.Text(c, r.ID).FontSize(fsHeading).FontWeight(600)
					ui.Text(c, "→").FontSize(fsBody).TextColor(a.k.text2)
					node := mono(ui.Text(c, a.exitNode(st, r)).FontSize(fsBody)).SingleLine()
					if len(r.Nodes) == 0 {
						node.TextColor(a.k.danger)
					}
					if !open {
						a.guardPills(c, r)
					}
					ui.Spacer(c)
					if !open {
						if len(secs) == 0 {
							a.pill(c, "没有流量", a.k.warn)
						} else {
							ui.Text(c, fmt.Sprintf("%d 项", len(secs))).FontSize(fsMeta).TextColor(a.k.text2)
						}
					}
				})
				if head.Clicked() {
					a.exitOpen[r.ID] = !open
				}
				if open {
					label := "出口设置"
					if a.exitEdit[r.ID] != nil {
						label = "收起设置"
					}
					if a.button(c, btnGhost, "settings", label).Clicked() {
						if a.exitEdit[r.ID] != nil {
							delete(a.exitEdit, r.ID)
						} else {
							a.startExitEdit(st, r.ID)
						}
					}
				}
			})
			if !open {
				return
			}
			if d := a.exitEdit[r.ID]; d != nil {
				a.exitSettings(c, st, r, d)
			} else {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() { a.guardPills(c, r) })
			}
			ui.Divider(c)
			a.sectionRows(c, secs)
		})
	})
}

// plainExitCard is proxy or direct: no settings of their own here, only what
// is sent to them.
func (a *app) plainExitCard(c *ui.Context, st engine.State, id string, hue ui.Color) {
	open := a.exitOpen[id]
	secs := a.sections(id)
	node, name := "其余所有流量", id
	if id == rules.TargetDirect {
		node, name = "不经代理", "直连"
	} else if cur := st.Choices[singcfg.TagProxy]; cur != "" {
		node = shorten(cur, 34) + " · 其余所有流量"
	}
	ui.Row(c).Background(a.k.surface).Border(1, a.k.border).Radius(16).AlignItems(ui.Stretch).Clip().Children(func() {
		ui.Box(c).Width(4).Background(hue)
		ui.Column(c).Grow(1).Padding(12, 16).Gap(10).Children(func() {
			head := ui.ButtonBase(c).Gap(10).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(name)
			head.Children(func() {
				fold := "chevron-right"
				if open {
					fold = "chevron-down"
				}
				icon(c, fold, 14, a.k.text2)
				ui.Text(c, name).FontSize(fsHeading).FontWeight(600)
				ui.Text(c, "→").FontSize(fsBody).TextColor(a.k.text2)
				mono(ui.Text(c, node).FontSize(fsBody)).SingleLine()
				ui.Spacer(c)
				if !open {
					ui.Text(c, fmt.Sprintf("%d 项", len(secs))).FontSize(fsMeta).TextColor(a.k.text2)
				}
			})
			if head.Clicked() {
				a.exitOpen[id] = !open
			}
			if open && len(secs) > 0 {
				ui.Divider(c)
				a.sectionRows(c, secs)
			}
		})
	})
}

func (a *app) sectionRows(c *ui.Context, secs []section) {
	if len(secs) == 0 {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			icon(c, "shield-alert", 13, a.k.warn)
			ui.Text(c, "还没有流量走这条出口").FontSize(fsMeta).TextColor(a.k.text2)
		})
		return
	}
	for _, s := range secs {
		ui.Column(c).Gap(4).Children(func() {
			ui.Row(c).Height(28).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, s.label).FontSize(fsBody).FontWeight(500).Width(220).SingleLine().Ellipsis("…").Tooltip(s.label)
				what := strings.Join(s.values[:min(len(s.values), 4)], " · ")
				if len(s.values) > 4 {
					what += fmt.Sprintf(" 等 %d 项", len(s.values))
				}
				mono(ui.Text(c, what).FontSize(fsMeta).TextColor(a.k.text2)).Grow(1).SingleLine().Ellipsis("…").Tooltip(strings.Join(s.values, "\n"))
				if s.group {
					a.pill(c, "我的域名", a.k.accent)
				} else {
					a.pill(c, "规则文本", a.k.text2)
				}
			})
			for _, sh := range s.shadow {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					icon(c, "shield-alert", 12, a.k.warn)
					ui.Text(c, sh).FontSize(fsCaption).TextColor(a.k.warn).Selectable()
				})
			}
		})
	}
}

func (a *app) startExitEdit(st engine.State, id string) {
	rl, ok := rules.ReadRoute(st.RulesText, id)
	if !ok {
		return
	}
	d := &exitDraft{rl: rl, every: everyLabel(rl.Check), span: allDay, from: "09:00", to: "23:30"}
	if f, t, ok := strings.Cut(rl.Hours, "-"); ok {
		d.span, d.from, d.to = someDay, f, t
	}
	a.exitEdit[id] = d
}

// exitSettings is an exit's settings, open on its card, saved together.
func (a *app) exitSettings(c *ui.Context, st engine.State, r rules.Route, d *exitDraft) {
	row := func(label string, body func()) {
		ui.Row(c).MinHeight(34).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(fsMeta).TextColor(a.k.text2).Width(72)
			body()
		})
	}
	ui.Column(c).Gap(4).Padding(10, 14).Radius(12).Background(a.k.surface2.Alpha(0.5)).Border(1, a.k.border).Children(func() {
		row("节点", func() {
			switch d.rl.Kind {
			case "regex":
				ui.Text(c, "名称匹配").FontSize(fsMeta)
				mono(ui.TextInput(c, &d.rl.Arg).Label(r.ID + " 的节点名称").Width(240))
				re, err := regexp.Compile(d.rl.Arg)
				if err != nil {
					ui.Text(c, "表达式写错了").FontSize(fsCaption).TextColor(a.k.danger)
					break
				}
				n := 0
				for _, nd := range st.Nodes {
					if re.MatchString(nd.Tag) {
						n++
					}
				}
				col := a.k.text2
				if n == 0 {
					col = a.k.danger
				}
				ui.Text(c, fmt.Sprintf("符合的 %d 个", n)).FontSize(fsCaption).TextColor(col)
			case "region":
				ui.Text(c, "地区 "+d.rl.Arg).FontSize(fsBody)
				ui.Text(c, "地区在「规则文本」里改").FontSize(fsCaption).TextColor(a.k.text2)
			case "like":
				ui.Text(c, "跟随 "+d.rl.Arg+"，与它同一节点").FontSize(fsBody)
			}
		})
		row("守卫", func() {
			ui.Row(c).Gap(20).AlignItems(ui.Center).Children(func() {
				gate := d.rl.Gate || len(d.rl.Exit) > 0
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					sw := ui.Switch(c, &gate).Label("熔断门")
					if len(d.rl.Exit) > 0 {
						sw.Disabled(true).Tooltip("有出口范围时总是开着：核对不通过就靠它切断")
					} else if sw.Changed() {
						d.rl.Gate = gate
					}
					ui.Text(c, "熔断门").FontSize(fsBody)
				})
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Switch(c, &d.rl.IPv4).Label("仅 IPv4")
					ui.Text(c, "仅 IPv4").FontSize(fsBody)
				})
			})
			if udp, v6 := a.guards(r.ID); udp || v6 {
				ui.Text(c, "阻断 UDP / IPv6 写在「规则文本」里").FontSize(fsCaption).TextColor(a.k.text2)
			}
		})
		if d.rl.Kind == "like" {
			a.exitSettingsFoot(c, r, d)
			return
		}
		ui.Divider(c)
		row("出口范围", func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Wrap().Grow(1).Children(func() {
				for i, x := range d.rl.Exit {
					ui.Row(c).Padding(3, 6, 3, 10).Gap(6).Radius(8).Background(a.k.surface).Border(1, a.k.border).AlignItems(ui.Center).Children(func() {
						mono(ui.Text(c, x).FontSize(fsMeta))
						if a.iconButton(c, "x", "删除 "+x).Clicked() {
							d.rl.Exit = append(d.rl.Exit[:i:i], d.rl.Exit[i+1:]...)
						}
					})
				}
				mono(ui.TextInput(c, &d.newExit).Label("新的出口网段").Placeholder("如 203.0.113.0/24").Width(170))
				if a.button(c, btnGhost, "plus", "添加网段").Clicked() && strings.TrimSpace(d.newExit) != "" {
					d.rl.Exit = append(d.rl.Exit, strings.TrimSpace(d.newExit))
					d.newExit = ""
				}
			})
		})
		if len(d.rl.Exit) == 0 {
			row("", func() {
				ui.Text(c, "没有出口范围就不核对出口 IP").FontSize(fsCaption).TextColor(a.k.text2)
			})
		} else {
			row("核对间隔", func() {
				labels := make([]string, len(checkEvery))
				for i, e := range checkEvery {
					labels[i] = e[1]
				}
				if !containsStr(labels, d.every) {
					labels = append(labels, d.every)
				}
				ui.Select(c, &d.every, labels).Label("核对间隔").Width(140)
				ui.Text(c, "启动时和换节点时总会先核对一次").FontSize(fsCaption).TextColor(a.k.text2)
			})
			row("核对时段", func() {
				ui.Select(c, &d.span, []string{allDay, someDay}).Label("核对时段").Width(140)
				if d.span == someDay {
					mono(ui.TextInput(c, &d.from).Label("开始").Width(72))
					ui.Text(c, "到").FontSize(fsMeta).TextColor(a.k.text2)
					mono(ui.TextInput(c, &d.to).Label("结束").Width(72))
					ui.Text(c, "时段外沿用上一次的结果").FontSize(fsCaption).TextColor(a.k.text2)
				}
			})
			row("上次核对", func() {
				x, ok := st.Exits[r.ID]
				switch {
				case st.Status != engine.Running:
					ui.Text(c, "内核没有运行").FontSize(fsCaption).TextColor(a.k.text2)
				case !ok || x.At.IsZero():
					ui.Text(c, "还没有核对").FontSize(fsCaption).TextColor(a.k.text2)
				case x.OK:
					a.pill(c, x.IP+" · 在范围内", a.k.ok)
				case x.Err != "":
					a.pill(c, "无法确认，已切断", a.k.danger).Tooltip(x.Err)
				default:
					a.pill(c, x.IP+" · 不在范围，已切断", a.k.danger)
				}
				if ok && !x.At.IsZero() {
					ui.Text(c, agoText(x.At)).FontSize(fsCaption).TextColor(a.k.text2)
				}
				ui.Spacer(c)
				if st.Status == engine.Running && a.button(c, btnSecondary, "refresh-cw", "现在核对").Clicked() {
					a.eng.CheckExits()
				}
			})
		}
		a.exitSettingsFoot(c, r, d)
	})
}

// exitSettingsFoot saves the settings to the route's line, or leaves them.
func (a *app) exitSettingsFoot(c *ui.Context, r rules.Route, d *exitDraft) {
	for _, e := range d.errs {
		ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
			icon(c, "x", 14, a.k.danger)
			ui.Text(c, e.Msg).FontSize(fsMeta).TextColor(a.k.danger).Selectable()
		})
	}
	ui.Row(c).Gap(8).Padding(6, 0, 0, 0).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "保存后改写这条出口在规则文本里的那一行，内核会重启。").FontSize(fsCaption).TextColor(a.k.text2).Grow(1)
		if a.button(c, btnSecondary, "", "取消").Clicked() {
			delete(a.exitEdit, r.ID)
		}
		if a.button(c, btnPrimary, "check", "保存").Clicked() {
			next := d.rl
			next.Check = everyValue(d.every)
			next.Hours = ""
			if d.span == someDay {
				next.Hours = strings.TrimSpace(d.from) + "-" + strings.TrimSpace(d.to)
			}
			if errs := a.eng.EditRoute(r.ID, func(rl *rules.RouteLine) { *rl = next }); errs != nil {
				d.errs = errs
			} else {
				delete(a.exitEdit, r.ID)
			}
		}
	})
}

// agoText says how long ago t was, roughly.
func agoText(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	}
	return fmt.Sprintf("%d 小时前", int(d.Hours()))
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
