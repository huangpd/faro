// Package rulesdoc makes the text a person pastes into an AI tool to have it
// write rules in Faro's own language.
//
// Faro's rules are not sing-box's and not Clash's, so an AI that has not been
// told the language writes the wrong one. The prompt carries the language
// (spec.txt, whose examples the tests compile, so it cannot drift from the
// real one), what this person has (their routes, regions, a few node names)
// and a place for what they want. Nothing is sent anywhere: the person copies
// it, and pastes it where they choose.
package rulesdoc

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed spec.txt
var spec string

// Spec is the language reference.
func Spec() string { return spec }

// Context is what the prompt says about this person's setup. It holds names
// only: no server, no address, no key, no subscription link.
type Context struct {
	Rules     string   // the rules in force
	Regions   []string // the regions the language knows
	NodeNames []string // node names, as the subscription gives them
	RuleSets  []string // the rule sets that are installed: the only ones set: can name
	Apps      []App    // the applications on the exits, in the order they are checked
}

// App is an application as the prompt shows it: the exit it is on, its name,
// and what it holds, as the「自己写」field takes it.
type App struct {
	Exit, Name string
	Items      []string // example.com, or kind:value; values with spaces unquoted
}

// Prompt returns the whole text to paste into an AI tool.
func Prompt(c Context) string {
	var b strings.Builder
	b.WriteString("你是 Faro（一个 sing-box 客户端）的规则助手。请严格使用下面的“Faro 规则语法”，帮我改规则。\n")
	b.WriteString("Faro 里有两处可以写：出口上的「应用」，和「规则文本」。回答分两部分，各放一个代码块，没有的部分就省略：\n")
	b.WriteString("A. 应用（优先用它）：每行一个，格式 `出口 | 名字 | 条目 条目 …`。条目是域名（example.com，含它的子域名），")
	b.WriteString("或 full: keyword: cidr: process: path: set: 加值；值里有空格就加双引号。改已有的应用，写同名的一行并给出它完整的新内容。\n")
	b.WriteString("B. 规则文本：只在应用做不到时才用，比如只限 udp/tcp/ipv6、几个条件必须同时满足、阻断（block）、新建或修改 route。只输出规则文本里要新增或修改的行，并说明放在哪一行之后。\n")
	b.WriteString("不要解释；不要写成 Clash 或 sing-box 的 JSON 格式；不要修改我已有的路由名；拿不准的地方，用注释（# 开头）写在对应行上方。\n")
	b.WriteString("生效顺序：先是局域网与私有地址直连，然后是各出口 noudp/noipv6 生成的阻断，然后是下面列出的应用（按列出的顺序），然后是规则文本（从上往下），都没匹配的走 proxy。")
	b.WriteString("第一个匹配的决定去向，所以规则文本挡不住应用已经匹配的流量。\n\n")
	b.WriteString("=== Faro 规则语法 ===\n")
	b.WriteString(strings.TrimSpace(spec))
	b.WriteString("\n\n=== 我的情况 ===\n")

	if len(c.Regions) > 0 {
		fmt.Fprintf(&b, "可用的地区: %s\n", strings.Join(c.Regions, " "))
	}
	if len(c.RuleSets) > 0 {
		fmt.Fprintf(&b, "Rule sets（set: 只能用这些，不要自己编）: %s\n", strings.Join(c.RuleSets, " "))
	} else {
		b.WriteString("Rule sets: 没有安装任何规则集，不要使用 set:。\n")
	}
	if routes := routeLines(c.Rules); len(routes) > 0 {
		b.WriteString("我已有的路由:\n")
		for _, r := range routes {
			b.WriteString("    " + r + "\n")
		}
	} else {
		b.WriteString("我还没有定义路由。\n")
	}
	if names := sample(c.NodeNames, 12); len(names) > 0 {
		b.WriteString("节点名字的样子（只有名字，用来判断地区；一共 " + fmt.Sprint(len(c.NodeNames)) + " 个）:\n")
		for _, n := range names {
			b.WriteString("    " + n + "\n")
		}
	}
	if len(c.Apps) > 0 {
		b.WriteString("\n我的应用（按检查顺序；`出口 | 名字 | 条目`）:\n```\n")
		for _, a := range c.Apps {
			items := make([]string, len(a.Items))
			for i, it := range a.Items {
				if k, v, ok := strings.Cut(it, ":"); ok && strings.ContainsAny(v, " \t") {
					it = k + `:"` + v + `"`
				}
				items[i] = it
			}
			fmt.Fprintf(&b, "%s | %s | %s\n", a.Exit, a.Name, strings.Join(items, " "))
		}
		b.WriteString("```\n")
	}
	if strings.TrimSpace(c.Rules) != "" {
		b.WriteString("\n我现在的规则文本（在应用之后检查）:\n```\n")
		b.WriteString(strings.TrimRight(c.Rules, "\n"))
		b.WriteString("\n```\n")
	}
	b.WriteString("\n=== 我的需求 ===\n（在这里写你想要什么，用自然语言就行。例如：让某个网站走英国的出口；把某个程序的流量直接放行；）\n")
	return b.String()
}

// routeLines are the "route ..." statements of a rules text, comments removed.
func routeLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(stripComment(line))
		if strings.HasPrefix(line, "route ") {
			out = append(out, line)
		}
	}
	return out
}

func stripComment(line string) string {
	in := false
	for i, r := range line {
		switch {
		case r == '"':
			in = !in
		case r == '#' && !in && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
			return line[:i]
		}
	}
	return line
}

// sample picks up to n names spread over the list, so that the countries a
// subscription has all show.
func sample(names []string, n int) []string {
	if len(names) <= n {
		return names
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, names[i*len(names)/n])
	}
	return out
}
