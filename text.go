package main

import (
	"fmt"
	"strings"
	"time"

	"faro/internal/engine"
	"faro/internal/singcfg"
)

const fastLabel = "最快"

func rateText(bps int64) string {
	f := float64(bps)
	switch {
	case f >= 1<<20:
		return fmt.Sprintf("%.1f MB/s", f/(1<<20))
	case f >= 1<<10:
		return fmt.Sprintf("%.1f KB/s", f/(1<<10))
	}
	return fmt.Sprintf("%d B/s", bps)
}

// titleEvery is how often the speed in the menu bar may change. Faster than
// this the figure only flickers, and the item beside it is moved by every change.
const titleEvery = 2 * time.Second

// menuRate is a speed for the menu bar: always MB/s, and always four characters
// of figure (0.00 to 9.99, 10.0 to 99.9), which in the monospaced title is a
// width that does not change, so that nothing beside it moves.
func menuRate(bps float64) string {
	mb := bps / (1 << 20)
	switch {
	case mb < 9.995:
		return fmt.Sprintf("%.2f MB/s", mb)
	case mb < 99.95:
		return fmt.Sprintf("%.1f MB/s", mb)
	}
	return fmt.Sprintf("%.0f MB/s", mb)
}

// menuTitle is what the menu bar says beside the icon: nothing while the core
// is not running, and else the speed, kept for titleEvery before it changes,
// as the mean of the last two seconds. cur is what it says now, and since when.
func menuTitle(st engine.State, cur string, since, now time.Time) string {
	if st.Status != engine.Running {
		return ""
	}
	if cur != "" && now.Sub(since) < titleEvery {
		return cur
	}
	h := st.History
	if len(h) > 2 {
		h = h[len(h)-2:]
	}
	if len(h) == 0 {
		return menuRate(float64(st.Rate.Down))
	}
	var sum float64
	for _, t := range h {
		sum += float64(t.Down)
	}
	return menuRate(sum / float64(len(h)))
}

func bytesText(n int64) string {
	f := float64(n)
	switch {
	case f >= 1<<30:
		return fmt.Sprintf("%.1f GB", f/(1<<30))
	case f >= 1<<20:
		return fmt.Sprintf("%.0f MB", f/(1<<20))
	}
	return fmt.Sprintf("%.0f KB", f/(1<<10))
}

func statusText(st engine.State) string {
	switch st.Status {
	case engine.Running:
		return "运行中"
	case engine.Starting:
		return "正在启动"
	}
	return "未运行"
}

func nodeLabel(st engine.State) string {
	if sel := st.Choices[singcfg.TagProxy]; sel != "" {
		return sel
	}
	return "自动"
}

func durationText(since time.Time) string {
	d := time.Since(since).Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

// shorten cuts a long name in the middle, where nodes and hosts differ.
func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func contains(s, q string) bool {
	return q == "" || strings.Contains(strings.ToLower(s), strings.ToLower(q))
}
