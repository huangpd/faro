package main

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"faro/internal/engine"
	"faro/internal/sub"
)

func (a *app) subsPage(c *ui.Context, st engine.State) {
	a.header(c, "订阅", countText(len(st.Subs), "个订阅")+" · "+countText(len(st.Nodes), "个节点"), nil)
	a.errorBanner(c, st)

	// Adding is the page's one primary action.
	a.card(c).Gap(10).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			icon(c, "plus", 15, a.k.text2)
			in := ui.TextInput(c, &a.subURL).Placeholder("订阅链接，或本地文件路径").Label("订阅链接或文件").Grow(1)
			add := a.button(c, btnPrimary, "", "添加").Clicked()
			if (in.Submitted() || add) && st.Busy == "" && a.subURL != "" {
				a.eng.AddSubscription(a.subURL)
				a.subURL = ""
			}
		})
	})
	a.busyLine(c, st)

	ui.Scroll(c).Grow(1).Gap(12).Children(func() {
		if len(st.Subs) == 0 {
			a.empty(c, "rss", "还没有订阅", "", nil)
			return
		}
		for _, s := range st.Subs {
			c.Key("sub:" + s.ID)
			a.subCard(c, st, s)
		}
	})
}

func (a *app) subCard(c *ui.Context, st engine.State, s sub.Subscription) {
	a.card(c).Gap(12).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, s.Name).FontSize(fsHeading).FontWeight(600).SingleLine()
				ui.Text(c, fmt.Sprintf("%d 个节点 · 更新于 %s", len(s.Nodes), s.Updated.Format("01-02 15:04"))).
					FontSize(fsMeta).TextColor(a.k.text2)
			})
			if a.button(c, btnSecondary, "refresh-cw", "更新").Clicked() && st.Busy == "" {
				a.eng.RefreshSubscription(s.ID)
			}
			if a.iconButton(c, "trash-2", "删除订阅").Clicked() && st.Busy == "" {
				a.eng.RemoveSubscription(s.ID)
			}
		})
		if s.Total > 0 {
			frac := float64(s.Used) / float64(s.Total)
			ui.Column(c).Gap(6).Children(func() {
				ui.Row(c).Children(func() {
					ui.Text(c, "已用流量").FontSize(fsMeta).TextColor(a.k.text2).Grow(1)
					mono(ui.Text(c, fmt.Sprintf("%s / %s", bytesText(s.Used), bytesText(s.Total))).FontSize(fsMeta))
				})
				ui.Progress(c, frac)
			})
		}
		if s.Expire > 0 {
			until := time.Unix(s.Expire, 0)
			days := int(time.Until(until).Hours() / 24)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "到期").FontSize(fsMeta).TextColor(a.k.text2)
				mono(ui.Text(c, until.Format("2006-01-02")).FontSize(fsMeta))
				if days < 14 {
					a.pill(c, fmt.Sprintf("还剩 %d 天", max(days, 0)), a.k.warn)
				}
			})
		}
	})
}
