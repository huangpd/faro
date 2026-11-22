package main

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"faro/internal/engine"
)

// level of a core log line: 0 info/other, 1 warning, 2 error.
func levelOf(l string) int {
	switch {
	case strings.Contains(l, "ERROR"), strings.Contains(l, "FATAL"), strings.Contains(l, "PANIC"):
		return 2
	case strings.Contains(l, "WARN"):
		return 1
	}
	return 0
}

func (a *app) logsPage(c *ui.Context, st engine.State) {
	var lines []string
	for _, l := range a.eng.Logs() {
		lv := levelOf(l)
		switch a.logLevel {
		case 2:
			if lv < 1 {
				continue
			}
		case 3:
			if lv < 2 {
				continue
			}
		}
		if contains(l, a.logQuery) {
			lines = append(lines, l)
		}
	}
	a.header(c, "日志", countText(len(lines), "行"), func() {
		ui.SearchField(c, &a.logQuery).Placeholder("搜索日志").Label("搜索日志").Width(220)
		ui.Segmented(c, &a.logLevel, "全部", "信息", "警告", "错误").Label("日志级别")
	})
	a.errorBanner(c, st)

	if len(lines) == 0 {
		a.empty(c, "scroll-text", "没有日志", "", nil)
		return
	}
	a.card(c).Grow(1).Padding(4).Children(func() {
		a.logList.FollowEnd = true // stays at the newest, until you scroll up
		ui.List(c, &a.logList, len(lines), func(i int) {
			l := lines[i]
			fg := a.k.text2
			switch levelOf(l) {
			case 2:
				fg = a.k.danger
			case 1:
				fg = a.k.warn
			}
			mono(ui.Text(c, l).FontSize(fsMeta).TextColor(fg)).Padding(2, 12).Selectable().Wrap()
		}).Grow(1)
	})
}
