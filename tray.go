package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo"

	"faro/internal/engine"
	"faro/internal/singcfg"
)

// menuSignature is everything the menu shows, as one string: the menu is
// made again only when it changes, not with every second's traffic sample.
func menuSignature(st engine.State) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%s|%v|%v|", st.Status, st.Version, st.SystemProxy, st.LightMode)
	if st.Plan != nil {
		for _, r := range st.Plan.Routes {
			fmt.Fprintf(&b, "%s,%v,%v,%q,%s,%s;", r.ID, r.Gate, r.Closed, r.Chosen, r.Like, strings.Join(r.Nodes, ","))
		}
	}
	fmt.Fprintf(&b, "|%q|", st.Choices[singcfg.TagProxy])
	for _, g := range st.NodeGroups { // the countries and their nodes, in the order shown
		b.WriteString(g.Region + ":")
		for _, n := range g.Nodes {
			b.WriteString(n.Tag)
			b.WriteByte(',')
		}
		b.WriteByte(';')
	}
	return b.String()
}

// trayItems is the menu: the state, the switches that matter while working
// (the proxy, the gates), and the nodes, so that the window need not open.
func (a *app) trayItems(st engine.State) []*mygo.MenuItem {
	var items []*mygo.MenuItem
	state := statusText(st)
	if st.Status == engine.Running && st.SystemProxy {
		state += " · 系统代理已开"
	}
	items = append(items, &mygo.MenuItem{Label: state, Disabled: true}, mygo.Separator())

	items = append(items, &mygo.MenuItem{Label: "系统代理", Type: mygo.MenuItemCheckbox, Checked: st.SystemProxy,
		Click: func(m *mygo.MenuItem, _ *mygo.Window) { a.eng.SetSystemProxy(m.Checked) }})

	routes := st.Plan
	// The gates: a checked item is a route that is cut.
	var gates []*mygo.MenuItem
	if routes != nil {
		for _, r := range routes.Routes {
			if !r.Gate || len(r.Nodes) == 0 {
				continue
			}
			id := r.ID
			gates = append(gates, &mygo.MenuItem{Label: "切断 " + id, Type: mygo.MenuItemCheckbox, Checked: r.Closed,
				ToolTip: "切断 " + id,
				Click:   func(m *mygo.MenuItem, _ *mygo.Window) { a.eng.SetGate(id, m.Checked) }})
		}
	}
	if len(gates) > 0 {
		items = append(items, mygo.Separator())
		items = append(items, gates...)
	}

	// Nodes: one submenu per route, then the main group.
	var nodes []*mygo.MenuItem
	if routes != nil {
		for _, r := range routes.Routes {
			if r.Kind == "like" || len(r.Nodes) == 0 {
				continue
			}
			id := r.ID
			var sub []*mygo.MenuItem
			if r.Fast {
				sub = append(sub, &mygo.MenuItem{Label: fastLabel, Type: mygo.MenuItemRadio, Checked: r.Chosen == "",
					Click: func(*mygo.MenuItem, *mygo.Window) { a.eng.Choose(id, "") }})
			}
			for _, n := range r.Nodes {
				n := n
				sub = append(sub, &mygo.MenuItem{Label: n, Type: mygo.MenuItemRadio, Checked: r.Chosen == n,
					Click: func(*mygo.MenuItem, *mygo.Window) { a.eng.Choose(id, n); a.eng.TestNode(n) }})
			}
			label := id
			if r.Chosen != "" {
				label += " · " + r.Chosen
			}
			nodes = append(nodes, &mygo.MenuItem{Label: label, Submenu: sub})
		}
	}
	if len(st.Nodes) > 0 {
		cur := st.Choices[singcfg.TagProxy]
		// Auto, then one submenu a country: forty nodes in one list is not a menu.
		sub := []*mygo.MenuItem{{Label: "自动", Type: mygo.MenuItemRadio, Checked: cur == "",
			Click: func(*mygo.MenuItem, *mygo.Window) { a.eng.Choose(singcfg.TagProxy, "") }}}
		for _, g := range st.NodeGroups {
			var in []*mygo.MenuItem
			for _, n := range g.Nodes {
				tag := n.Tag
				in = append(in, &mygo.MenuItem{Label: tag, Type: mygo.MenuItemRadio, Checked: cur == tag,
					Click: func(*mygo.MenuItem, *mygo.Window) {
						a.eng.Choose(singcfg.TagProxy, tag)
						a.eng.TestNode(tag) // choosing is testing, here as in the window
					}})
			}
			label := g.Label
			for _, n := range g.Nodes {
				if n.Tag == cur {
					label += " ●" // where the current one is
				}
			}
			if len(sub) == 1 {
				sub = append(sub, mygo.Separator())
			}
			sub = append(sub, &mygo.MenuItem{Label: label, Submenu: in})
		}
		nodes = append(nodes, &mygo.MenuItem{Label: "proxy · " + nodeLabel(st), Submenu: sub})
	}
	if len(nodes) > 0 {
		items = append(items, mygo.Separator())
		items = append(items, nodes...)
	}

	items = append(items, mygo.Separator(),
		&mygo.MenuItem{Label: "轻量模式", Type: mygo.MenuItemCheckbox, Checked: st.LightMode,
			Click: func(m *mygo.MenuItem, _ *mygo.Window) { a.setLightMode(m.Checked) }},
		&mygo.MenuItem{Label: "打开 Faro", Click: func(*mygo.MenuItem, *mygo.Window) { a.openWindow() }},
		&mygo.MenuItem{Role: mygo.RoleQuit})
	return items
}
