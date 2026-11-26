package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"faro/internal/clashapi"
	"faro/internal/domains"
	"faro/internal/engine"
)

func (a *app) connsPage(c *ui.Context, st engine.State) {
	conns := a.filteredConns(st)
	a.header(c, "连接", countText(len(conns), "个连接"), func() {
		ui.SearchField(c, &a.connQuery).Placeholder("搜索主机、进程、出口").Label("搜索连接").Width(240)
	})
	a.errorBanner(c, st)

	if st.Status != engine.Running {
		a.empty(c, "waypoints", "核心未运行", "", nil)
		return
	}
	if len(conns) == 0 {
		a.empty(c, "waypoints", "没有连接", "", nil)
		return
	}

	// The table.
	a.card(c).Grow(1).Padding(0).Children(func() {
		ui.Row(c).Padding(8, 16).Gap(12).Border(1, ui.Transparent).Children(func() {
			a.col(c, "主机", 0, true, false)
			a.col(c, "进程", 110, false, false)
			a.col(c, "出口", 170, false, false)
			a.col(c, "下载", 78, false, true)
			a.col(c, "上传", 78, false, true)
			a.col(c, "时长", 48, false, true)
		})
		ui.Divider(c)
		// Keyed by connection, not place: the rows are sorted anew each second.
		// ItemKey only takes when set before Rows, not on a list given its rows.
		ui.List(c, &a.connList, len(conns)).ItemKey(func(i int) any { return conns[i].ID }).Grow(1).Rows(func(lr ui.ListRow) {
			c := lr.Context
			k := conns[lr.Index]
			on := k.ID == a.connSel
			row := ui.ButtonBase(c).Padding(7, 16).Gap(12).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(k.Host)
			switch {
			case on:
				row.Background(a.k.accent.Alpha(0.10))
			case row.Hovered():
				row.Background(a.k.surface2.Alpha(0.6))
			}
			row.Children(func() {
				host := k.Host
				if k.Port != "" && k.Port != "0" {
					host += ":" + k.Port
				}
				mono(ui.Text(c, host).FontSize(fsMeta)).SingleLine().Grow(1).Ellipsis("…").Tooltip(host)
				ui.Text(c, k.Process).FontSize(fsMeta).TextColor(a.k.text2).SingleLine().Width(110).Ellipsis("…")
				mono(ui.Text(c, exitOf(k)).FontSize(fsMeta)).SingleLine().Width(170).Ellipsis("…")
				mono(ui.Text(c, bytesText(k.Download)).FontSize(fsMeta).TextColor(a.k.text2)).SingleLine().Width(78).TextAlign(ui.End)
				mono(ui.Text(c, bytesText(k.Upload)).FontSize(fsMeta).TextColor(a.k.text2)).SingleLine().Width(78).TextAlign(ui.End)
				mono(ui.Text(c, durationText(k.Start)).FontSize(fsMeta).TextColor(a.k.text2)).SingleLine().Width(48).TextAlign(ui.End)
			})
			if row.Clicked() {
				if on {
					a.connSel = ""
				} else {
					a.connSel = k.ID
				}
			}
		})
	})
	if k, ok := a.selectedConn(conns); ok {
		a.why(c, st, k)
	}
}

func (a *app) col(c *ui.Context, title string, w float32, grow, right bool) {
	t := ui.Text(c, title).FontSize(fsCaption).FontWeight(600).TextColor(a.k.text2).SingleLine()
	if grow {
		t.Grow(1)
	} else {
		t.Width(w)
	}
	if right {
		t.TextAlign(ui.End) // figures line up with their heading
	}
}

func (a *app) filteredConns(st engine.State) []clashapi.Conn {
	var out []clashapi.Conn
	for _, k := range st.Conns {
		if contains(k.Host+" "+k.Process+" "+exitOf(k), a.connQuery) {
			out = append(out, k)
		}
	}
	return out
}

func (a *app) selectedConn(conns []clashapi.Conn) (clashapi.Conn, bool) {
	for _, k := range conns {
		if k.ID == a.connSel {
			return k, true
		}
	}
	return clashapi.Conn{}, false
}

// exitOf is where a connection leaves from: the node at the end of its chain.
func exitOf(k clashapi.Conn) string {
	if len(k.Chain) == 0 {
		return "—"
	}
	return k.Chain[0]
}

// why answers "why did this go there": the rule the core matched, and the way
// from the group to the exit. It is the core's own account, not a guess.
func (a *app) why(c *ui.Context, st engine.State, k clashapi.Conn) {
	a.card(c).Gap(10).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			icon(c, "sparkles", 15, a.k.info)
			ui.Text(c, "为什么走了这里").FontSize(fsHeading).FontWeight(600).Grow(1)
			if a.button(c, btnDanger, "x", "断开").Clicked() {
				a.eng.CloseConnection(k.ID)
				a.connSel = ""
			}
		})
		a.whyRow(c, "命中规则", ruleText(k.Rule))
		if k.Payload != "" {
			a.whyRow(c, "匹配内容", k.Payload)
		}
		// The chain, from the group asked for to the exit that carried it.
		var chain []string
		for i := len(k.Chain) - 1; i >= 0; i-- {
			chain = append(chain, k.Chain[i])
		}
		a.whyRow(c, "路径", strings.Join(chain, "  →  "))
		a.whyRow(c, "目的", fmt.Sprintf("%s %s %s", strings.ToUpper(k.Network), k.DestIP, k.Port))
		if held := a.heldBy(st, k.Host); held != "" {
			a.whyRow(c, "所在规则集", held)
		}
		a.addFromConn(c, st, k.Host)
	})
}

func (a *app) whyRow(c *ui.Context, label, value string) {
	ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
		ui.Text(c, label).FontSize(fsMeta).TextColor(a.k.text2).Width(64)
		mono(ui.Text(c, value).FontSize(fsMeta)).Grow(1).Selectable()
	})
}

// ruleText says what the core's account of a match means. "final" is the
// core's word for: no rule named it, so it went where the rest goes.
func ruleText(rule string) string {
	switch rule {
	case "", "final":
		return "没有规则点名，走了 proxy"
	}
	return rule
}

// heldBy says which rule sets the site is in, and which group each is kept in.
// It only knows the sets that are here, those that ship and those that were added.
func (a *app) heldBy(st engine.State, host string) string {
	if _, ok := domains.One(host); !ok {
		return "" // an address, not a name
	}
	var out []string
	for _, name := range a.eng.SetsHolding(host) {
		item := strings.TrimPrefix(name, "geosite-")
		for _, g := range st.Groups {
			if slices.Contains(g.Sets, name) {
				item += "（应用 " + g.Name + "）"
			}
		}
		out = append(out, item)
	}
	return strings.Join(out, "、")
}
