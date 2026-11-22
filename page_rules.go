package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"faro/internal/domains"
	"faro/internal/engine"
	"faro/internal/rules"
	"faro/internal/rulesdoc"
	"faro/internal/ruleset"
)

func (a *app) rulesPage(c *ui.Context, st engine.State) {
	a.header(c, "规则", "", func() {
		// Faro's rules are its own language, which an AI tool has to be told. This
		// copies what it needs to know, and what this setup is; the person pastes it
		// where they like. Nothing is sent from here.
		if a.button(c, btnSecondary, "sparkles", "复制给 AI 的提示词").Clicked() {
			a.copyText(c, a.aiPrompt(st), "已复制")
		}
		ui.Segmented(c, &a.rulesTab, "我的域名", "规则文本").Label("规则页")
	})
	a.errorBanner(c, st)
	if st.Plan != nil {
		for _, w := range st.Plan.Warnings {
			a.banner(c, a.k.warn, "shield-alert", w)
		}
	}
	for _, n := range st.Notes {
		a.banner(c, a.k.info, "shield-alert", n)
	}

	if a.rulesTab == 0 {
		a.domainsPage(c, st)
		return
	}

	// The rules text is the system's: it is shut until it is opened on purpose,
	// and the config it makes is not shown unless asked for.
	ui.Row(c).Grow(1).Gap(16).AlignItems(ui.Stretch).Children(func() {
		a.rulesEditor(c, a.rulesOpen)
		if a.showConfig {
			ui.Column(c).Grow(1).Children(func() { a.configPreview(c, st) })
		}
	})
	for _, e := range a.rulesErrs {
		ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
			icon(c, "x", 14, a.k.danger)
			ui.Text(c, e.Error()).FontSize(fsMeta).TextColor(a.k.danger).Selectable()
		})
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if a.rulesOpen {
			if a.button(c, btnPrimary, "check", "应用").Clicked() && st.Busy == "" {
				a.rulesErrs = a.eng.ApplyRules(a.rulesEdit)
			}
			if a.button(c, btnSecondary, "lock", "锁定").Clicked() {
				a.rulesOpen = false
			}
			if a.rulesEdit != st.RulesText {
				a.pill(c, "改动尚未生效", a.k.warn)
			}
		} else if a.button(c, btnSecondary, "lock-open", "解锁编辑").Clicked() {
			a.unlockAsk = true
		}
		ui.Box(c).Grow(1)
		label := "查看生成的配置"
		if a.showConfig {
			label = "隐藏生成的配置"
		}
		if a.button(c, btnGhost, "terminal", label).Clicked() {
			a.showConfig = !a.showConfig
		}
	})
	if ui.AlertDialog(c, &a.unlockAsk, "编辑规则文本？", "写错会让流量走错出口，应用前请先核对。", "取消", "解锁") == 1 {
		a.rulesOpen = true
	}
}

// configPreview shows the config the rules in force make, in a code panel:
// dark in either appearance, keys in one color and values in others.
func (a *app) configPreview(c *ui.Context, st engine.State) {
	if a.previewFor != st.RulesText+"|"+fmt.Sprint(len(st.Nodes)) || a.previewText == "" {
		a.previewFor = st.RulesText + "|" + fmt.Sprint(len(st.Nodes))
		out, err := a.eng.Preview()
		if err != nil {
			a.previewText = "// " + err.Error()
		} else {
			a.previewText = out
		}
		a.previewLines = strings.Split(a.previewText, "\n")
	}
	panel := ui.Column(c).Grow(1).Radius(14).Background(ui.Hex("#05080F")).Border(1, ui.RGBA(255, 255, 255, 0.10)).Clip()
	panel.Children(func() {
		ui.Row(c).Padding(8, 14).Gap(8).AlignItems(ui.Center).Background(ui.RGBA(255, 255, 255, 0.06)).Children(func() {
			icon(c, "terminal", 13, ui.Hex("#94A3B8"))
			ui.Text(c, "sing-box").FontSize(fsCaption).FontWeight(600).TextColor(ui.Hex("#94A3B8"))
		})
		ui.List(c, &a.previewList, len(a.previewLines), func(i int) {
			a.codeLine(c, a.previewLines[i])
		}).Grow(1).Padding(8, 0)
	})
}

// codeLine draws one line of JSON, the key apart from what follows it.
func (a *app) codeLine(c *ui.Context, line string) {
	indent := len(line) - len(strings.TrimLeft(line, " "))
	body := strings.TrimLeft(line, " ")
	ui.Row(c).Padding(0, 14, 0, 14+float32(indent)*4).Children(func() {
		key, rest, isKey := strings.Cut(body, `": `)
		if isKey && strings.HasPrefix(key, `"`) {
			ui.Row(c).Gap(6).Children(func() { // the key, its colon, and the value, apart
				mono(ui.Text(c, key+`":`).FontSize(fsMeta).TextColor(ui.Hex("#FB923C"))).SingleLine()
				mono(ui.Text(c, rest).FontSize(fsMeta).TextColor(codeValueColor(rest))).SingleLine()
			})
			return
		}
		mono(ui.Text(c, body).FontSize(fsMeta).TextColor(codeValueColor(body))).SingleLine()
	})
}

func codeValueColor(v string) ui.Color {
	v = strings.TrimRight(strings.TrimSpace(v), ",")
	switch {
	case strings.HasPrefix(v, `"`):
		return ui.Hex("#86EFAC") // a string
	case v == "true" || v == "false" || v == "null":
		return ui.Hex("#C4B5FD")
	case len(v) > 0 && (v[0] >= '0' && v[0] <= '9'):
		return ui.Hex("#7DD3FC")
	case strings.HasPrefix(v, "//"):
		return ui.Hex("#64748B")
	}
	return ui.Hex("#E2E8F0") // braces and brackets
}

// aiPrompt is the text for an AI tool: the language, and this person's setup.
func (a *app) aiPrompt(st engine.State) string {
	var names []string
	for _, n := range st.Nodes {
		if st.Plan == nil || !st.Plan.IsNotice(n.Tag) {
			names = append(names, n.Tag)
		}
	}
	var regions []string
	if st.Plan != nil {
		regions = st.Plan.Regions()
	} else {
		regions = rules.RegionIDs()
	}
	var sets []string
	for name := range ruleset.Paths(filepath.Join(st.DataDir, "rulesets")) {
		sets = append(sets, name)
	}
	sort.Strings(sets)
	prompt := rulesdoc.Prompt(rulesdoc.Context{Rules: st.RulesText, Regions: regions, NodeNames: names, RuleSets: sets})
	// The person's own groups of sites are checked before the text, so a rule
	// written to go with them has to know what they hold.
	if head, _ := domains.Head(st.Groups); head != "" {
		prompt += "\n\nSites this person keeps in their own list, outside the rules above. They are checked BEFORE the rules above, so they win; do not repeat them:\n\n" + head
	}
	return prompt
}
