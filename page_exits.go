package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"faro/internal/applib"
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
	group  bool   // a group (an application), not a heading of the rules text
	app    string // the library entry it came from
	values []string
	shadow []string
}

func (a *app) exitsPage(c *ui.Context, st engine.State) {
	key := fmt.Sprintf("%d|%s|%v|%v", st.PlanRev, st.RulesText, st.Groups, st.GroupErrors) // a new plan for new nodes, choices, rules or groups
	if a.orderFor != key {
		a.orderFor, a.order = key, a.eng.Order()
	}
	a.groups = st.Groups
	if a.exitErr != "" {
		a.banner(c, a.k.danger, "shield-alert", a.exitErr)
	}
	for _, g := range st.Groups {
		if msg := st.GroupErrors[g.Name]; msg != "" {
			a.banner(c, a.k.danger, "shield-alert", g.Name+"："+msg)
		}
	}
	if st.Plan == nil {
		a.empty(c, "list-checks", "规则没有生效", "先在「规则文本」里改正错误", nil)
		return
	}
	a.priorityHint(c, st)
	ui.Scroll(c).Grow(1).Gap(12).Children(func() {
		// In the order they are written; each keeps the hue it has on the「现在」page.
		routes := make([]int, len(st.Plan.Routes))
		for i := range routes {
			routes[i] = i
		}
		sort.SliceStable(routes, func(x, y int) bool { return st.Plan.Routes[routes[x]].Line < st.Plan.Routes[routes[y]].Line })
		for _, i := range routes {
			c.Key("exit:" + st.Plan.Routes[i].ID) // its fields stay with it when exits come or go
			a.exitCard(c, st, st.Plan.Routes[i], a.k.lanes[i%len(a.k.lanes)])
		}
		a.plainExitCard(c, st, rules.TargetProxy, a.k.text2.Alpha(0.5))
		a.plainExitCard(c, st, rules.TargetDirect, a.k.ok.Alpha(0.6))
		// auto and block only when something is sent to them: an application can
		// be, and one whose exit is gone is blocked; it is shown, to be moved.
		for _, t := range []struct {
			id  string
			hue ui.Color
		}{{rules.TargetAuto, a.k.text2.Alpha(0.5)}, {rules.TargetBlock, a.k.danger.Alpha(0.6)}} {
			if len(a.sections(t.id)) > 0 {
				a.plainExitCard(c, st, t.id, t.hue)
			}
		}
		a.newExitCard(c, st)
	})
	a.removeAppDialog(c)
	if a.delExitAsk && ui.AlertDialog(c, &a.delExitAsk, "删除出口 "+a.delExit+"？", "规则文本里它的那一行会被删掉。", "取消", "删除") == 1 {
		if errs := a.eng.DeleteRoute(a.delExit); errs != nil {
			a.exitErr = errs[0].Msg
		} else {
			a.exitErr = ""
			delete(a.exitEdit, a.delExit)
		}
	}
}

// sections gathers the steps that send to target by their heading, in order.
func (a *app) sections(target string) []section {
	var out []section
	at := map[string]int{}
	for _, s := range a.order {
		if s.Target != target && s.Route != target {
			continue
		}
		if s.Guard != "" {
			continue // a route's guards are said on its card, as pills, not as an application of block
		}
		label := s.Label
		if s.Group != "" {
			label = s.Group
		} else if label == "" {
			label = "规则文本"
		}
		key := s.Group + "\x00" + label // an application and a heading never share a row
		i, ok := at[key]
		if !ok {
			i = len(out)
			at[key] = i
			sec := section{label: label, group: s.Group != ""}
			for _, g := range a.groups {
				if s.Group != "" && g.Name == s.Group {
					sec.app = g.App
				}
			}
			out = append(out, sec)
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
func (a *app) guards(id string) (udp, v6 bool) { return a.guardsOf(id, true) }

// guardsOf is guards, counting the blocks the route's own noudp and noipv6
// make only with own.
func (a *app) guardsOf(id string, own bool) (udp, v6 bool) {
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
		if s.Guard == id && own {
			udp = udp || s.Network == "udp"
			v6 = v6 || s.IPVersion == 6
			continue
		}
		if s.Target != rules.TargetBlock || s.Route != "" || s.Guard != "" {
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
			a.sectionRows(c, st, secs)
			a.addAppPanel(c, st, r.ID)
		})
	})
}

// plainExitCard is proxy or direct: no settings of their own here, only what
// is sent to them.
func (a *app) plainExitCard(c *ui.Context, st engine.State, id string, hue ui.Color) {
	open := a.exitOpen[id]
	secs := a.sections(id)
	node, name := "其余所有流量", id
	switch id {
	case rules.TargetDirect:
		node, name = "不经代理", "直连"
	case rules.TargetAuto:
		node = "所有节点中最快的"
	case rules.TargetBlock:
		node, name = "不允许连接", "阻断"
	default:
		if cur := st.Choices[singcfg.TagProxy]; cur != "" {
			node = shorten(cur, 34) + " · 其余所有流量"
		}
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
			if open {
				ui.Divider(c)
				a.sectionRows(c, st, secs)
				a.addAppPanel(c, st, id)
			}
		})
	})
}

func (a *app) sectionRows(c *ui.Context, st engine.State, secs []section) {
	if len(secs) == 0 {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			icon(c, "shield-alert", 13, a.k.warn)
			ui.Text(c, "还没有流量走这条出口").FontSize(fsMeta).TextColor(a.k.text2)
		})
		return
	}
	for _, s := range secs {
		c.Key("section:" + s.label)
		ui.Column(c).Gap(4).Children(func() {
			if s.group {
				a.appRow(c, st, s)
			} else {
				// A part of the rules text: shown, to be changed in the text.
				ui.Row(c).Height(32).Gap(10).Padding(0, 4).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Width(30)
					a.appIcon(c, s)
					ui.Text(c, s.label).FontSize(fsBody).FontWeight(500).Width(170).SingleLine().Ellipsis("…").Tooltip(s.label)
					mono(ui.Text(c, valuesLine(s.values)).FontSize(fsMeta).TextColor(a.k.text2)).Grow(1).SingleLine().Ellipsis("…").Tooltip(strings.Join(s.values, "\n"))
					a.pill(c, "规则文本", a.k.text2)
					ui.Box(c).Size(49, 28) // keeps the rows' ends in line with the applications'
				})
			}
			for _, sh := range s.shadow {
				ui.Row(c).Gap(6).Padding(0, 0, 0, 44).AlignItems(ui.Center).Children(func() {
					icon(c, "shield-alert", 12, a.k.warn)
					ui.Text(c, sh).FontSize(fsCaption).TextColor(a.k.warn).Selectable()
				})
			}
		})
	}
}

// appIcon is the small square in front of a row: the library's icon for one
// of its applications, a list for one's own, a page for the rules text.
func (a *app) appIcon(c *ui.Context, s section) {
	ic := "scroll-text"
	if s.group {
		ic = "list-checks"
	}
	if lib, ok := applib.Find(s.app); ok {
		ic = lib.Icon
	}
	ui.Box(c).Size(24, 24).Radius(7).Background(a.k.surface2).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).Children(func() {
		icon(c, ic, 13, a.k.text)
	})
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
				for _, g := range []struct {
					name string
					on   *bool
				}{{"仅 IPv4", &d.rl.IPv4}, {"阻断 UDP", &d.rl.NoUDP}, {"阻断 IPv6", &d.rl.NoIPv6}} {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Switch(c, g.on).Label(g.name)
						ui.Text(c, g.name).FontSize(fsBody)
					})
				}
			})
			// Blocks written in the rules text hold too, whatever the switches say.
			if udp, v6 := a.guardsOf(r.ID, false); (udp && !d.rl.NoUDP) || (v6 && !d.rl.NoIPv6) {
				ui.Text(c, "规则文本里另有阻断行").FontSize(fsCaption).TextColor(a.k.text2).Tooltip("开关只管这条出口自己生成的阻断；规则文本里写的阻断行照样生效")
			}
		})
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
		if a.button(c, btnGhost, "trash-2", "删除出口").Label("删除出口 " + r.ID).Clicked() {
			a.delExit, a.delExitAsk = r.ID, true
		}
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

// addAppPanel is the end of an open exit card: a button that opens the
// library's search here, with a form for one written by hand under it.
func (a *app) addAppPanel(c *ui.Context, st engine.State, exit string) {
	if a.addingTo != exit {
		ui.Row(c).Children(func() {
			if a.button(c, btnGhost, "plus", "添加应用").Clicked() {
				a.addingTo, a.appQuery, a.appErr = exit, "", ""
				a.customName, a.customText = "", ""
			}
		})
		return
	}
	where := map[string]string{} // library id -> the exit it is on
	for _, g := range st.Groups {
		if g.App != "" {
			where[g.App] = g.Target
		}
	}
	ui.Column(c).Gap(6).Padding(10, 12).Radius(12).Background(a.k.surface2.Alpha(0.5)).Border(1, a.k.border).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			icon(c, "search", 14, a.k.text2)
			ui.TextInput(c, &a.appQuery).Label("在规则库中搜索").Placeholder("搜索应用、域名或进程").Grow(1)
			if a.button(c, btnGhost, "", "收起").Clicked() {
				a.addingTo = ""
			}
		})
		found := applib.Search(a.appQuery)
		if len(found) == 0 {
			ui.Text(c, "规则库里没有，可以在下面自己写").FontSize(fsMeta).TextColor(a.k.text2)
		}
		shown := found[:min(len(found), 8)]
		for _, lib := range shown {
			ui.Row(c).Height(32).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(24, 24).Radius(7).Background(a.k.surface).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).Children(func() {
					icon(c, lib.Icon, 13, a.k.text)
				})
				ui.Text(c, lib.Name).FontSize(fsBody).FontWeight(500).Width(110).SingleLine()
				ui.Text(c, lib.About).FontSize(fsCaption).TextColor(a.k.text2).Width(150).SingleLine().Ellipsis("…")
				mono(ui.Text(c, strings.Join(lib.Rules[:min(len(lib.Rules), 3)], " · ")).FontSize(fsCaption).TextColor(a.k.text2)).Grow(1).SingleLine().Ellipsis("…").Tooltip(strings.Join(lib.Rules, "\n"))
				if t, ok := where[lib.ID]; ok {
					ui.Text(c, "已在 "+targetName(t)).FontSize(fsCaption).TextColor(a.k.text2)
				} else if a.button(c, btnSecondary, "plus", "加到 "+targetName(exit)).Label("把 " + lib.Name + " 加到 " + exit).Clicked() {
					a.appErr = errText(a.eng.AddApp(exit, lib.ID))
				}
			})
		}
		if more := len(found) - len(shown); more > 0 {
			ui.Text(c, fmt.Sprintf("还有 %d 个，输入名字搜索", more)).FontSize(fsCaption).TextColor(a.k.text2)
		}
		ui.Divider(c)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c, &a.customName).Label("自定义应用的名字").Placeholder("名字").Width(120)
			mono(ui.TextInput(c, &a.customText).Label("自定义应用的内容").Placeholder("example.com  process:App  cidr:203.0.113.0/24").Grow(1))
			if a.button(c, btnSecondary, "plus", "自己写").Clicked() {
				if err := a.eng.AddCustomApp(exit, a.customName, a.customText); err != nil {
					a.appErr = err.Error()
				} else {
					a.customName, a.customText, a.appErr = "", "", ""
				}
			}
		})
		if a.appErr != "" {
			ui.Text(c, a.appErr).FontSize(fsMeta).TextColor(a.k.danger).Selectable()
		}
	})
}

// removeAppDialog asks before an application is taken off its exit.
func (a *app) removeAppDialog(c *ui.Context) {
	if !a.removeAsk {
		return
	}
	if ui.AlertDialog(c, &a.removeAsk, "移除 "+a.removeApp+"？", "它的流量会改由下面的规则决定，通常是 proxy。", "取消", "移除") == 1 {
		a.exitErr = errText(a.eng.DeleteGroup(a.removeApp)) // shown above the exits: the add panel may be closed
	}
}

// newExitForm is a new exit while it is being written.
type newExitForm struct {
	open                    bool
	id, kind, arg           string // kind: byName or byRegion
	gate, ipv4, noUDP, noV6 bool
	errs                    []rules.Error
}

const (
	byName   = "名称匹配"
	byRegion = "地区"
)

// newExitCard is a button at the end of the exits, and the form it opens: a
// name, which nodes, and the guards. The rest is in the exit's settings once
// it is made.
func (a *app) newExitCard(c *ui.Context, st engine.State) {
	f := &a.newExit
	if !f.open {
		ui.Row(c).Children(func() {
			if a.button(c, btnSecondary, "plus", "新建出口").Clicked() {
				*f = newExitForm{open: true, kind: byName, gate: true, ipv4: true}
			}
		})
		return
	}
	row := func(label string, body func()) {
		ui.Row(c).MinHeight(34).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(fsMeta).TextColor(a.k.text2).Width(72)
			body()
		})
	}
	// The name is checked as it is typed: what is wrong with it is said under
	// the field at once, and the exit cannot be made until it is right.
	nameErr := ""
	if id := strings.TrimSpace(f.id); id != "" {
		nameErr = rules.CheckRouteID(id)
		if nameErr == "" && st.Plan != nil {
			for _, r := range st.Plan.Routes {
				if r.ID == id {
					nameErr = "已经有叫 " + id + " 的出口"
				}
			}
		}
	}
	a.card(c).Gap(6).Children(func() {
		ui.Text(c, "新建出口").FontSize(fsHeading).FontWeight(600)
		row("名字", func() {
			mono(ui.TextInput(c, &f.id).Label("出口名").Placeholder("中文、小写字母、数字和 -，如 工作、work").Width(240))
		})
		if nameErr != "" {
			ui.Row(c).Gap(6).Padding(0, 0, 0, 84).AlignItems(ui.Center).Children(func() {
				icon(c, "shield-alert", 13, a.k.danger)
				ui.Text(c, nameErr).FontSize(fsMeta).TextColor(a.k.danger)
			})
		}
		row("节点", func() {
			ui.Select(c, &f.kind, []string{byName, byRegion}).Label("节点的选法").Width(120)
			if f.kind == byRegion {
				regions := rules.RegionIDs()
				if st.Plan != nil {
					regions = st.Plan.Regions()
				}
				if !containsStr(regions, f.arg) {
					f.arg = regions[0]
				}
				ui.Select(c, &f.arg, regions).Label("地区").Width(100)
				return
			}
			mono(ui.TextInput(c, &f.arg).Label("节点名称的正则").Placeholder("节点名称的开头，如 ^UK-2-").Width(240))
			if re, err := regexp.Compile(f.arg); err == nil && f.arg != "" {
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
			}
		})
		row("守卫", func() {
			ui.Row(c).Gap(20).AlignItems(ui.Center).Children(func() {
				for _, g := range []struct {
					name string
					on   *bool
				}{{"熔断门", &f.gate}, {"仅 IPv4", &f.ipv4}, {"阻断 UDP", &f.noUDP}, {"阻断 IPv6", &f.noV6}} {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Switch(c, g.on).Label("新出口 " + g.name)
						ui.Text(c, g.name).FontSize(fsBody)
					})
				}
			})
		})
		for _, e := range f.errs {
			ui.Text(c, e.Msg).FontSize(fsMeta).TextColor(a.k.danger).Selectable()
		}
		ui.Row(c).Gap(8).Padding(6, 0, 0, 0).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "出口范围和核对在建好后的「出口设置」里加。").FontSize(fsCaption).TextColor(a.k.text2).Grow(1)
			if a.button(c, btnSecondary, "", "取消").Clicked() {
				f.open = false
			}
			done := a.button(c, btnPrimary, "check", "建好")
			if nameErr != "" || strings.TrimSpace(f.id) == "" {
				done.Disabled(true)
			}
			if done.Clicked() && nameErr == "" && strings.TrimSpace(f.id) != "" {
				kind := "regex"
				if f.kind == byRegion {
					kind = "region"
				}
				rl := rules.RouteLine{ID: strings.TrimSpace(f.id), Kind: kind, Arg: strings.TrimSpace(f.arg),
					Gate: f.gate, IPv4: f.ipv4, NoUDP: f.noUDP, NoIPv6: f.noV6}
				if errs := a.eng.AddRoute(rl); errs != nil {
					f.errs = errs
				} else {
					a.exitOpen[rl.ID] = true
					*f = newExitForm{}
				}
			}
		})
	})
}
