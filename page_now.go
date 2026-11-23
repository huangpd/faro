package main

import (
	"fmt"
	"sort"

	"github.com/egoist/mygo/ui"

	"faro/internal/engine"
	"faro/internal/rules"
	"faro/internal/singcfg"
)

// nowPage answers the three questions that matter: where does each kind of
// traffic leave from, is anything leaking, and can I cut it. Each kind is a
// lane: what it is, the gate on it, the exit it uses.
func (a *app) nowPage(c *ui.Context, st engine.State) {
	a.header(c, "现在", "", func() {
		if st.Status == engine.Running {
			if a.button(c, btnSecondary, "zap", "测速").Clicked() {
				a.eng.TestAll()
			}
		}
	})
	a.errorBanner(c, st)
	a.busyLine(c, st)

	ui.Scroll(c).Grow(1).Gap(16).Children(func() {
		a.overviewCards(c, st)

		if len(st.Nodes) == 0 {
			a.empty(c, "rss", "还没有节点", "", func() {
				if a.button(c, btnPrimary, "plus", "添加订阅").Clicked() {
					a.setPage(pageSubs)
				}
			})
			return
		}
		if st.Plan == nil && len(st.PlanErrors) > 0 {
			a.banner(c, a.k.warn, "shield-alert", "规则有错误，没有生效："+st.PlanErrors[0].Error())
		}
		ui.Text(c, "出口").FontSize(fsHeading).FontWeight(600)
		if st.Plan != nil {
			for i, r := range st.Plan.Routes {
				a.lane(c, st, r, a.k.lanes[i%len(a.k.lanes)])
			}
		}
		a.mainLane(c, st)
	})
}

// overviewCards: the traffic of the last minute, and the two switches that say
// how this Mac's traffic reaches Faro.
func (a *app) overviewCards(c *ui.Context, st engine.State) {
	ui.Row(c).Gap(16).AlignItems(ui.Stretch).Children(func() {
		a.card(c).Grow(2).Gap(10).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "流量").FontSize(fsMeta).TextColor(a.k.text2).Grow(1)
				ui.Text(c, "最近一分钟").FontSize(fsCaption).TextColor(a.k.text2)
			})
			ui.Row(c).Gap(28).Children(func() {
				a.figure(c, "arrow-down", "下载", rateText(st.Rate.Down))
				a.figure(c, "arrow-up", "上传", rateText(st.Rate.Up))
			})
			var down []float64
			for _, h := range st.History {
				down = append(down, float64(h.Down))
			}
			a.sparkline(c, down, a.k.info, 60, 44)
		})
		a.card(c).Grow(1).Gap(12).Children(func() {
			ui.Text(c, "接入").FontSize(fsMeta).TextColor(a.k.text2)
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "系统代理").FontSize(fsBody).FontWeight(500).Grow(1)
				on := st.SystemProxy
				if ui.Switch(c, &on).Label("启用系统代理").Changed() {
					a.eng.SetSystemProxy(on)
				}
			})
			ui.Divider(c)
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, "本地代理").FontSize(fsMeta).TextColor(a.k.text2)
				mono(ui.Text(c, fmt.Sprintf("127.0.0.1:%d", st.MixedPort)).FontSize(fsBody)).Selectable()
			})
		})
	})
}

func (a *app) figure(c *ui.Context, iconName, label, value string) {
	ui.Column(c).Gap(2).Children(func() {
		ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
			icon(c, iconName, 12, a.k.text2)
			ui.Text(c, label).FontSize(fsMeta).TextColor(a.k.text2)
		})
		mono(ui.Text(c, value).FontSize(fsFigure).FontWeight(500)).SingleLine()
	})
}

// lane is one kind of traffic and its way out.
func (a *app) lane(c *ui.Context, st engine.State, r rules.Route, hue ui.Color) {
	blocked := len(r.Nodes) == 0
	edge := a.k.border
	if r.Closed || blocked {
		edge = a.k.danger.Alpha(0.45)
	}
	card := ui.Row(c).Background(a.k.surface).Border(1, edge).Radius(16).Gap(0).AlignItems(ui.Stretch).Clip()
	card.Children(func() {
		ui.Box(c).Width(4).Background(hue) // the route's hue: always beside its name
		ui.Column(c).Grow(1).Padding(14, 16).Gap(10).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, r.ID).FontSize(fsHeading).FontWeight(600)
					ui.Text(c, laneKind(r)).FontSize(fsMeta).TextColor(a.k.text2)
				})
				if r.Gate && !blocked {
					a.gate(c, r)
				}
			})
			if blocked {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					icon(c, "shield-alert", 14, a.k.danger)
					ui.Text(c, "没有符合的节点，发往它的流量会被阻断，不会换别的出口").FontSize(fsBody).TextColor(a.k.danger)
				})
				return
			}
			a.laneExit(c, st, r)
			if a.expanded[r.ID] {
				a.nodeChips(c, st, r.Nodes, r.Chosen, r.Fast, func(tag string) { a.eng.Choose(r.ID, tag) })
			}
		})
		if !blocked {
			mode := globeLive
			if r.Closed {
				mode = globeClosed
			}
			ui.Column(c).Padding(8, 14, 8, 0).Justify(ui.Center).Children(func() {
				a.laneGlobe(c, r.ID, hue, laneExitPlace(st, r), mode)
			})
		}
	})
}

func laneKind(r rules.Route) string {
	switch r.Kind {
	case "region":
		return fmt.Sprintf("%s 节点 · %d 个", r.Arg, len(r.Nodes))
	case "like":
		return "与 " + r.Like + " 同一出口"
	}
	return fmt.Sprintf("%d 个节点", len(r.Nodes))
}

// gate is the switch that cuts a lane: its words say which way it is.
func (a *app) gate(c *ui.Context, r rules.Route) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if r.Closed {
			icon(c, "lock", 14, a.k.danger)
			ui.Text(c, "已切断").FontSize(fsBody).FontWeight(600).TextColor(a.k.danger)
		} else {
			icon(c, "lock-open", 14, a.k.text2)
			ui.Text(c, "放行").FontSize(fsBody).TextColor(a.k.text2)
		}
		open := !r.Closed
		if ui.Switch(c, &open).Label("放行 " + r.ID).Changed() {
			a.eng.SetGate(r.ID, !open)
		}
	})
}

// laneExit shows where the lane leaves from now, and lets you change it.
func (a *app) laneExit(c *ui.Context, st engine.State, r rules.Route) {
	node := r.Chosen
	if node == "" {
		node = fastLabel
	}
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "→").FontSize(fsBody).TextColor(a.k.text2)
		mono(ui.Text(c, shorten(node, 34)).FontSize(fsBody)).SingleLine().Tooltip(node)
		a.delayPill(c, st, r.Chosen)
		if r.IPv4 {
			a.pill(c, "IPv4", a.k.info)
		}
		if r.Fast {
			a.pill(c, "跟随最快", a.k.info)
		}
		if r.Closed {
			a.pill(c, "已切断", a.k.danger)
		}
		ui.Spacer(c)
		if r.Kind != "like" {
			label := "更换"
			if a.expanded[r.ID] {
				label = "收起"
			}
			if a.button(c, btnGhost, "", label).Clicked() {
				a.expanded[r.ID] = !a.expanded[r.ID]
			}
		}
	})
}

// mainLane is everything the rules do not name.
func (a *app) mainLane(c *ui.Context, st engine.State) {
	ui.Row(c).Background(a.k.surface).Border(1, a.k.border).Radius(16).AlignItems(ui.Stretch).Clip().Children(func() {
		ui.Box(c).Width(4).Background(a.k.text2.Alpha(0.5))
		ui.Column(c).Grow(1).Padding(14, 16).Gap(10).Children(func() {
			ui.Text(c, "proxy").FontSize(fsHeading).FontWeight(600)
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				cur := st.Choices[singcfg.TagProxy]
				ui.Text(c, "→").FontSize(fsBody).TextColor(a.k.text2)
				name := "自动选最快"
				if cur != "" {
					name = shorten(cur, 34)
				}
				mono(ui.Text(c, name).FontSize(fsBody)).SingleLine().Tooltip(name)
				a.delayPill(c, st, cur)
				ui.Spacer(c)
				label := "更换"
				if a.expanded[singcfg.TagProxy] {
					label = "收起"
				}
				if a.button(c, btnGhost, "", label).Clicked() {
					a.expanded[singcfg.TagProxy] = !a.expanded[singcfg.TagProxy]
				}
			})
			if a.expanded[singcfg.TagProxy] {
				a.groupedChips(c, st, cur(st), func(tag string) { a.eng.Choose(singcfg.TagProxy, tag) })
			}
		})
		ui.Column(c).Padding(8, 14, 8, 0).Justify(ui.Center).Children(func() {
			a.laneGlobe(c, singcfg.TagProxy, a.k.accent, mainExitPlace(st), globeLive)
		})
	})
}

func cur(st engine.State) string { return st.Choices[singcfg.TagProxy] }

// delayPill is how fast an exit answered, in numbers and a word.
func (a *app) delayPill(c *ui.Context, st engine.State, tag string) {
	if tag == "" {
		return
	}
	d, ok := st.Delays[tag]
	switch {
	case st.Testing[tag]:
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(12, 12).Label("正在测试")
			ui.Text(c, "测试中").FontSize(fsCaption).TextColor(a.k.text2)
		})
	case !ok:
		return
	case d < 0:
		a.pill(c, "超时", a.k.danger)
	case d < 250:
		a.pill(c, fmt.Sprintf("%d ms", d), a.k.ok)
	case d < 600:
		a.pill(c, fmt.Sprintf("%d ms", d), a.k.warn)
	default:
		a.pill(c, fmt.Sprintf("%d ms", d), a.k.danger)
	}
}

// nodeChips lets you pick one of a route's nodes: a tidy grid, in natural order,
// the chosen one outlined. With auto, a first chip chooses the automatic one.
func (a *app) nodeChips(c *ui.Context, st engine.State, tags []string, chosen string, auto bool, pick func(tag string)) {
	ui.Divider(c)
	sorted := append([]string(nil), tags...)
	sort.SliceStable(sorted, func(i, j int) bool { return engine.NaturalLess(sorted[i], sorted[j]) })
	prefix := commonPrefix(sorted)
	a.chipGrid(c, func() {
		if auto {
			a.autoChip(c, chosen == "", pick)
		}
		for _, t := range sorted {
			a.chip(c, st, t, prefix, t == chosen, pick)
		}
	})
}

// groupedChips lets you pick any node: the countries one under another, each with
// its heading and count, the nodes in a grid under it. What a provider lists as
// nodes but is only a message (usage, expiry) is said once, apart, and cannot be
// picked: choosing it would send traffic nowhere.
func (a *app) groupedChips(c *ui.Context, st engine.State, chosen string, pick func(tag string)) {
	ui.Divider(c)
	a.chipGrid(c, func() { a.autoChip(c, chosen == "", pick) })
	for _, g := range st.NodeGroups {
		tags := make([]string, len(g.Nodes))
		for i, n := range g.Nodes {
			tags[i] = n.Tag
		}
		prefix := commonPrefix(tags)
		ui.Column(c).Gap(8).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, g.Label).FontSize(fsMeta).FontWeight(600).TextColor(a.k.text2)
				ui.Text(c, fmt.Sprint(len(g.Nodes))).FontSize(fsCaption).TextColor(a.k.text2)
			})
			a.chipGrid(c, func() {
				for _, t := range tags {
					a.chip(c, st, t, prefix, t == chosen, pick)
				}
			})
		})
	}
	for _, n := range st.Notices {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			icon(c, "shield-alert", 13, a.k.text2)
			ui.Text(c, n).FontSize(fsMeta).TextColor(a.k.text2).Selectable()
		})
	}
}

// chipMin is the least a node's chip is wide; the grid has as many columns as
// fit, and shares the room left, so no row ends short of the card's edge.
const chipMin = 190

// chipColumns is how many chips fit across the card in a window of this width:
// the window less the sidebar (216), the page's margins (56) and the card's
// padding and the lane's edge (4 + 32 + 2).
func chipColumns(c *ui.Context) int {
	w, _ := c.Size()
	room := w - 216 - 56 - 38
	n := int((room + 8) / (chipMin + 8))
	return max(n, 1)
}

// chipGrid lays chips out in equal columns across the card.
func (a *app) chipGrid(c *ui.Context, children func()) {
	ui.Grid(c).Columns(chipColumns(c)).Gap(8).Children(children)
}

func (a *app) autoChip(c *ui.Context, on bool, pick func(string)) {
	b := ui.ButtonBase(c).Height(32).Padding(0, 10).Radius(9).Gap(8).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(fastLabel)
	a.chipSurface(b, on)
	b.Children(func() {
		icon(c, "zap", 13, a.k.info)
		ui.Text(c, "自动选最快").FontSize(fsMeta).FontWeight(500).Grow(1).SingleLine()
		if on {
			icon(c, "check", 13, a.k.accent)
		}
	})
	if b.Clicked() {
		pick("")
	}
}

// chipSurface is the look of a chip: outlined in the brand color when chosen.
func (a *app) chipSurface(b ui.Element, on bool) {
	switch {
	case on:
		b.Background(a.k.accent.Alpha(0.14)).Border(1, a.k.accent)
	case b.Hovered():
		b.Background(a.k.surface2).Border(1, a.k.border)
	default:
		b.Background(a.k.surface2.Alpha(0.5)).Border(1, a.k.border)
	}
}

// chip is one node: its name (without what its country already says), the
// multiplier apart, and how it answered. Clicking it chooses it AND tests it:
// there is no button for that, the choice is the test.
func (a *app) chip(c *ui.Context, st engine.State, tag, prefix string, on bool, pick func(tag string)) {
	name, rate := chipLabel(tag, prefix)
	b := ui.ButtonBase(c).Height(32).Padding(0, 10).Radius(9).Gap(8).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(tag).Tooltip(tag)
	a.chipSurface(b, on)
	b.Children(func() {
		if on {
			icon(c, "check", 13, a.k.accent)
		}
		mono(ui.Text(c, name).FontSize(fsMeta)).SingleLine().Grow(1).Ellipsis("…")
		if rate != "" {
			ui.Text(c, "×"+rate).FontSize(fsCaption).TextColor(a.k.text2).SingleLine()
		}
		a.testState(c, st, tag)
	})
	if b.Clicked() {
		pick(tag)
		a.eng.TestNode(tag)
	}
}

// testState is the end of a chip: turning while it is being tested, then the
// answer in milliseconds, or that it did not answer. Nothing before the first test.
func (a *app) testState(c *ui.Context, st engine.State, tag string) {
	switch d, ok := st.Delays[tag]; {
	case st.Testing[tag]:
		ui.Spinner(c).Size(13, 13).Label("正在测试")
	case !ok:
	case d < 0:
		mono(ui.Text(c, "不通").FontSize(fsCaption).FontWeight(600).TextColor(a.k.danger)).SingleLine()
	default:
		col := a.k.ok
		if d >= 600 {
			col = a.k.danger
		} else if d >= 250 {
			col = a.k.warn
		}
		mono(ui.Text(c, fmt.Sprintf("%d", d)).FontSize(fsCaption).FontWeight(600).TextColor(col)).SingleLine()
	}
}
