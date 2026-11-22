package main

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// commonPrefix is the longest start that every name has, cut where a word ends:
// in a country's group, "日本-" or "Tokyo ". Nothing in common, or a group of
// one, is "": a lone name is shown whole.
func commonPrefix(names []string) string {
	if len(names) < 2 {
		return ""
	}
	p := names[0]
	for _, n := range names[1:] {
		for !strings.HasPrefix(n, p) {
			_, size := utf8.DecodeLastRuneInString(p)
			p = p[:len(p)-size]
			if p == "" {
				return ""
			}
		}
	}
	// Only up to a separator: not half a word ("日本-TY-1", "日本-TY-2" share "日本-TY-", not "日本-TY-").
	if i := strings.LastIndexAny(p, "-_ |·/"); i >= 0 {
		return p[:i+1]
	}
	return ""
}

// rateSuffix is a provider's note of a traffic multiplier at the end of a name:
// "-流量倍率:0.6", "|0.1x", " x0.5", " ×2". It is the same note on every node, so it is
// cut to "×0.6" and set apart from the name.
var rateSuffix = regexp.MustCompile(`[-| ]*(?:流量)?倍率[:：]\s*([0-9.]+)\s*$|[-| ]*([0-9.]+)\s*[xX×]\s*$|[-| ]+[xX×]\s*([0-9.]+)\s*$`)

// splitRate separates a name from the multiplier it ends with, "" if there is none.
func splitRate(name string) (base, rate string) {
	m := rateSuffix.FindStringSubmatchIndex(name)
	if m == nil {
		return name, ""
	}
	for i := 2; i < len(m); i += 2 {
		if m[i] >= 0 {
			return name[:m[0]], name[m[i]:m[i+1]]
		}
	}
	return name, ""
}

// chipLabel is how a node reads inside its country's group: the name without
// what the group already says, and its multiplier apart. A name that is all
// that (nothing left) is shown whole.
func chipLabel(tag, prefix string) (name, rate string) {
	base, rate := splitRate(tag)
	name = strings.TrimPrefix(base, prefix)
	if strings.TrimSpace(name) == "" {
		name = tag
	}
	return name, rate
}
