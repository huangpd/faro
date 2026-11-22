package main

import (
	"github.com/egoist/mygo/ui"
)

// card is the one container for a unit of content: a surface, a hairline, a
// corner. There is no shadow: a card is told from the window by its edge.
func (a *app) card(c *ui.Context) ui.Element {
	e := ui.Column(c).Background(a.k.surface).Border(1, a.k.border).Radius(16).Padding(18)
	if a.k.dark { // a hairline of light along the top edge, as glass has
		e.InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.07))
	}
	return e
}

// pill is a short status: its color is the tint, but the words carry the meaning.
func (a *app) pill(c *ui.Context, text string, fg ui.Color) ui.Element {
	return ui.Row(c).Padding(2, 8).Radius(999).Background(fg.Alpha(0.14)).Children(func() {
		ui.Text(c, text).FontSize(fsCaption).FontWeight(600).TextColor(fg).SingleLine()
	})
}

// mono sets what is data in the data font, with figures that keep their width.
func mono(e ui.Element) ui.Element { return e.Font(monoFont) }

type btnKind int

const (
	btnSecondary btnKind = iota
	btnPrimary
	btnGhost
	btnDanger
)

// button is a control with an icon and a label, in one of four weights. A
// screen has one primary button; the rest are secondary or ghost.
func (a *app) button(c *ui.Context, kind btnKind, iconName, label string) ui.Element {
	bg, fg, bd := a.k.surface2, a.k.text, a.k.border
	switch kind {
	case btnPrimary:
		bg, fg, bd = a.k.accent, a.k.onAccent, ui.Transparent
	case btnGhost:
		bg, bd = ui.Transparent, ui.Transparent
	case btnDanger:
		bg, fg, bd = a.k.danger.Alpha(0.12), a.k.danger, a.k.danger.Alpha(0.3)
	}
	b := ui.ButtonBase(c).Padding(0, 12).Height(30).Radius(8).Border(1, bd).Gap(6).AlignItems(ui.Center).Cursor(ui.CursorPointer)
	hover := b.Hovered()
	switch {
	case b.Pressed():
		b.Background(bg.Mix(a.k.text, 0.16))
	case hover:
		b.Background(bg.Mix(a.k.text, 0.08))
	default:
		b.Background(bg)
	}
	return b.Children(func() {
		if iconName != "" {
			icon(c, iconName, 14, fg)
		}
		if label != "" {
			ui.Text(c, label).FontSize(fsBody).FontWeight(500).TextColor(fg).SingleLine()
		}
	})
}

// iconButton is a button of an icon alone; it needs a label for those who do
// not see it.
func (a *app) iconButton(c *ui.Context, iconName, label string) ui.Element {
	b := ui.ButtonBase(c).Size(30, 30).Radius(8).AlignItems(ui.Center).Justify(ui.Center).Cursor(ui.CursorPointer).Label(label).Tooltip(label)
	if b.Hovered() {
		b.Background(a.k.surface2)
	}
	return b.Children(func() { icon(c, iconName, 15, a.k.text2) })
}

// header is the top of a page: its title, a line under it, and what the page
// does at the right.
func (a *app) header(c *ui.Context, title, sub string, right func()) {
	ui.Row(c).AlignItems(ui.Center).Gap(12).DragWindow().Children(func() {
		ui.Column(c).Gap(2).Grow(1).Children(func() {
			ui.Text(c, title).FontSize(fsTitle).FontWeight(600)
			if sub != "" {
				ui.Text(c, sub).FontSize(fsMeta).TextColor(a.k.text2)
			}
		})
		if right != nil {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(right)
		}
	})
}

// empty is what a page shows when it has nothing yet: what is missing and what
// to do about it.
func (a *app) empty(c *ui.Context, iconName, title, hint string, action func()) {
	ui.Column(c).AlignItems(ui.Center).Gap(10).Padding(48, 24).Children(func() {
		ui.Box(c).Size(48, 48).Radius(14).Background(a.k.surface2).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			icon(c, iconName, 22, a.k.text2)
		})
		ui.Text(c, title).FontSize(fsHeading).FontWeight(600)
		if hint != "" {
			ui.Text(c, hint).FontSize(fsBody).TextColor(a.k.text2).TextAlign(ui.Center).MaxWidth(360)
		}
		if action != nil {
			ui.Box(c).Height(4)
			action()
		}
	})
}

// banner is a message that needs reading: an error, or a rule that is not in force.
func (a *app) banner(c *ui.Context, fg ui.Color, iconName, text string) {
	ui.Row(c).Gap(10).AlignItems(ui.Start).Padding(10, 12).Radius(10).Background(fg.Alpha(0.10)).Border(1, fg.Alpha(0.30)).Children(func() {
		icon(c, iconName, 16, fg)
		ui.Text(c, text).FontSize(fsBody).TextColor(fg).Grow(1).Selectable()
	})
}

// sparkline draws the last values as bars; the newest is at the right.
func (a *app) sparkline(c *ui.Context, vals []float64, color ui.Color, n int, h float32) {
	peak := 1.0
	for _, v := range vals {
		if v > peak {
			peak = v
		}
	}
	ui.Row(c).Height(h).AlignItems(ui.End).Gap(2).Children(func() {
		for i := 0; i < n; i++ {
			j := i - (n - len(vals)) // right-aligned: older slots are empty
			bar := float32(2)
			alpha := float32(0.18)
			if j >= 0 {
				bar = float32(vals[j]/peak) * (h - 2)
				if bar < 2 {
					bar = 2
				}
				alpha = 0.35 + 0.65*float32(i)/float32(n) // fades toward the past
			}
			ui.Box(c).Grow(1).Height(bar).Radius(1.5).Background(color.Alpha(alpha))
		}
	})
}
