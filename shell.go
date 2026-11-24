package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"faro/internal/engine"
)

type page int

const (
	pageNow page = iota
	pageSubs
	pageConns
	pageRules
	pageLogs
	pageSettings
)

var navItems = []struct {
	page page
	name string
	icon string
}{
	{pageNow, "现在", "activity"},
	{pageSubs, "订阅", "rss"},
	{pageConns, "连接", "waypoints"},
	{pageRules, "规则", "list-checks"},
	{pageLogs, "日志", "scroll-text"},
	{pageSettings, "设置", "settings"},
}

// setPage goes to a page. The connections are listed only while that page is
// open: nobody pays for a list nobody looks at.
func (a *app) setPage(p page) {
	if p == a.page {
		return
	}
	if a.page == pageConns {
		a.eng.WatchConnections(false)
	}
	if p == pageConns {
		a.eng.WatchConnections(true)
	}
	a.page = p
}

// view is the whole window: a sidebar that is always the same, and the page.
func (a *app) view(c *ui.Context) {
	st := a.eng.State()
	a.k = tokensFor(c.Theme().Dark) // the system's appearance, before the theme is replaced
	c.SetTheme(a.k.theme())
	material := c.Vibrancy()
	if material {
		c.Root().Background(ui.Transparent)
	}
	bar := c.TitleBar()

	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		side := ui.Column(c).Width(216).Padding(0, 12, 12, 12).Gap(0)
		if !material {
			side.Background(a.k.sidebar)
		}
		side.Children(func() { a.sidebar(c, st, bar) })
		ui.Box(c).Width(1).Background(a.k.border)
		ui.Column(c).Grow(1).Background(a.k.bg).Children(func() { a.content(c, st) })
	})
}

func (a *app) sidebar(c *ui.Context, st engine.State, bar ui.TitleBar) {
	// Under the window's own controls, which sit in the top left.
	ui.Box(c).Height(max(bar.Height, 28)).DragWindow()
	ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(2, 8, 16, 8).DragWindow().Children(func() {
		ui.Box(c).Size(36, 36).Radius(10).Border(1, ui.RGBA(255, 255, 255, 0.10)).Clip().Children(func() { ui.Image(c, logo).Size(34, 34) })
		ui.Column(c).Gap(0).Children(func() {
			ui.Text(c, "Faro").FontSize(fsHeading).FontWeight(600)
			ui.Text(c, "v"+version).FontSize(fsCaption).TextColor(a.k.text2)
		})
	})

	ui.Column(c).Gap(2).Children(func() {
		for _, n := range navItems {
			a.navItem(c, n.page, n.name, n.icon, st)
		}
	})

	ui.Spacer(c)
	a.ambient(c, st)
}

// navItem is one place to go. The chosen one is filled with the brand color,
// as a source list's selection is, and its weight changes: not color alone.
func (a *app) navItem(c *ui.Context, p page, name, iconName string, st engine.State) {
	on := a.page == p
	b := ui.ButtonBase(c).Height(34).Padding(0, 12).Radius(9).Gap(10).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(name)
	switch {
	case on:
		b.Background(a.k.accent)
	case b.Hovered():
		b.Background(a.k.surface2.Alpha(0.6))
	}
	b.Children(func() {
		fg, w := a.k.text2, 400
		if on {
			fg, w = a.k.onAccent, 600
		}
		icon(c, iconName, 16, fg)
		ui.Text(c, name).FontSize(fsBody).FontWeight(w).TextColor(fg).Grow(1)
		// A gate that is shut is the one thing worth a mark wherever you are.
		if p == pageNow && a.anyGateShut(st) {
			mark := a.k.danger
			if on {
				mark = a.k.onAccent
			}
			ui.Box(c).Size(7, 7).Radius(4).Background(mark)
		}
	})
	if b.Clicked() {
		a.setPage(p)
	}
}

func (a *app) anyGateShut(st engine.State) bool {
	if st.Plan == nil {
		return false
	}
	for _, r := range st.Plan.Routes {
		if r.Closed {
			return true
		}
	}
	return false
}

// ambient is at the foot of the sidebar on every page: the speed, and whether
// Faro is working.
func (a *app) ambient(c *ui.Context, st engine.State) {
	ui.Column(c).Gap(10).Padding(14).Radius(14).Background(a.k.surface).Border(1, a.k.border).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			dot := a.k.text2
			label := "未运行"
			switch st.Status {
			case engine.Running:
				dot, label = a.k.ok, "运行中"
			case engine.Starting:
				dot, label = a.k.warn, "正在启动"
			}
			ui.Box(c).Size(8, 8).Radius(4).Background(dot)
			ui.Text(c, label).FontSize(fsBody).FontWeight(500)
		})
		ui.Row(c).Gap(10).Children(func() {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Grow(1).Children(func() {
				icon(c, "arrow-down", 12, a.k.text2)
				mono(ui.Text(c, rateText(st.Rate.Down)).FontSize(fsMeta)).SingleLine()
			})
			ui.Row(c).Gap(4).AlignItems(ui.Center).Grow(1).Children(func() {
				icon(c, "arrow-up", 12, a.k.text2)
				mono(ui.Text(c, rateText(st.Rate.Up)).FontSize(fsMeta)).SingleLine()
			})
		})
	})
}

// content is the page, under the title bar, with the error that is on top of everything.
func (a *app) content(c *ui.Context, st engine.State) {
	body := ui.Column(c).Grow(1).Padding(0, 28, 20, 28).Gap(16)
	body.Children(func() {
		ui.Box(c).Height(14).DragWindow()
		switch a.page {
		case pageSubs:
			a.subsPage(c, st)
		case pageConns:
			a.connsPage(c, st)
		case pageRules:
			a.rulesPage(c, st)
		case pageLogs:
			a.logsPage(c, st)
		case pageSettings:
			a.settingsPage(c, st)
		default:
			a.nowPage(c, st)
		}
	})
}

// errorBanner shows what went wrong, once, under the header of any page.
func (a *app) errorBanner(c *ui.Context, st engine.State) {
	if st.Err != "" {
		a.banner(c, a.k.danger, "shield-alert", st.Err)
	}
}

func (a *app) busyLine(c *ui.Context, st engine.State) {
	if st.Busy != "" {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			icon(c, "refresh-cw", 13, a.k.text2)
			ui.Text(c, st.Busy).FontSize(fsMeta).TextColor(a.k.text2)
		})
	}
}

func countText(n int, unit string) string { return fmt.Sprintf("%d %s", n, unit) }
