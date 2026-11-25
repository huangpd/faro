package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"github.com/egoist/mygo/ui"

	"faro/internal/engine"
	"faro/internal/singcfg"
)

func (a *app) settingsPage(c *ui.Context, st engine.State) {
	a.header(c, "设置", "", nil)
	a.errorBanner(c, st)
	a.busyLine(c, st)

	// One line to a setting: its name, and its control. No sentences.
	ui.Scroll(c).Grow(1).Gap(20).Children(func() {
		a.group(c, "网络", func() {
			a.setting(c, "系统代理", func() {
				on := st.SystemProxy
				if ui.Switch(c, &on).Label("启用系统代理").Changed() {
					a.eng.SetSystemProxy(on)
				}
			})
			ui.Divider(c)
			a.portSetting(c, st)
			ui.Divider(c)
			a.setting(c, "命令行工具", func() {
				if a.button(c, btnSecondary, "terminal", "复制终端命令").Clicked() {
					a.copyText(c, shellProxyCommand(st.MixedPort), "已复制终端命令")
				}
			})
		})
		a.group(c, "运行方式", func() {
			a.setting(c, "轻量模式", func() {
				on := st.LightMode
				if ui.Switch(c, &on).Label("启用轻量模式").Changed() {
					a.setLightMode(on)
				}
			})
		})
		a.group(c, "DNS", func() {
			a.dnsPicker(c, "国内网站", "domestic", st.DNS.Domestic, func(id string) { a.setDNS(id, st.DNS.Foreign) })
			ui.Divider(c)
			a.dnsPicker(c, "其他网站", "foreign", st.DNS.Foreign, func(id string) { a.setDNS(st.DNS.Domestic, id) })
			if a.dnsErr != "" { // its own: the port's is cleared each frame it is not being edited
				a.banner(c, a.k.danger, "shield-alert", a.dnsErr)
			}
		})
		a.group(c, "规则集", func() {
			text, stale := ruleSetsAge(st.RuleSetsChecked, time.Now())
			a.setting(c, text, func() {
				if a.button(c, btnSecondary, "refresh-cw", "立即更新").Clicked() && st.Busy == "" {
					a.eng.UpdateRuleSets()
				}
			}, stale)
		})
		a.configSettings(c)
		a.group(c, "核心", func() {
			v := "未运行"
			if st.Version != "" {
				v = st.Version
			}
			a.setting(c, "sing-box", func() { mono(ui.Text(c, v).FontSize(fsBody)) })
			ui.Divider(c)
			a.setting(c, "数据目录", func() {
				mono(ui.Text(c, st.DataDir).FontSize(fsMeta).TextColor(a.k.text2)).Selectable().SingleLine().TruncateMiddle().MaxWidth(420)
			})
		})
		a.group(c, "关于", func() {
			a.setting(c, "Faro", func() { mono(ui.Text(c, version()).FontSize(fsBody).TextColor(a.k.text2)) })
		})
	})
}

func (a *app) group(c *ui.Context, title string, body func()) {
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, title).FontSize(fsMeta).FontWeight(600).TextColor(a.k.text2).Padding(0, 4)
		a.card(c).Gap(12).Children(body)
	})
}

// setting is a name and its control. A name may be marked as a warning.
func (a *app) setting(c *ui.Context, title string, control func(), warn ...bool) {
	ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
		t := ui.Text(c, title).FontSize(fsBody).FontWeight(500).Grow(1)
		if len(warn) > 0 && warn[0] {
			t.TextColor(a.k.warn)
		}
		control()
	})
}

// portSetting is the local proxy's port, which can be changed: a field, and
// the button that applies it, with the reason when it is refused.
func (a *app) portSetting(c *ui.Context, st engine.State) {
	if !a.portEditing {
		a.portText, a.portErr = strconv.Itoa(st.MixedPort), ""
	}
	a.setting(c, "本地代理端口", func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			in := ui.TextInput(c, &a.portText).Label("本地代理端口").Width(86).Font(monoFont).TextAlign(ui.End)
			if in.Changed() {
				a.portEditing = a.portText != strconv.Itoa(st.MixedPort)
				a.portErr = ""
			}
			apply := a.button(c, btnSecondary, "check", "应用")
			if a.portText == strconv.Itoa(st.MixedPort) {
				apply.Disabled(true)
			}
			if (apply.Clicked() || in.Submitted()) && a.portText != strconv.Itoa(st.MixedPort) {
				n, err := strconv.Atoi(strings.TrimSpace(a.portText))
				if err != nil {
					a.portErr = "请输入数字"
				} else if err := a.eng.SetMixedPort(n); err != nil {
					a.portErr = err.Error()
				} else {
					a.portEditing, a.portErr = false, ""
					c.Toast("端口已改为 " + strconv.Itoa(n))
				}
			}
		})
	})
	if a.portErr != "" {
		a.banner(c, a.k.danger, "shield-alert", a.portErr)
	}
}

// shellProxyCommand is what a person pastes into a terminal to point its
// programs at Faro: both spellings of the variables, as tools differ.
func shellProxyCommand(port int) string {
	http := fmt.Sprintf("http://127.0.0.1:%d", port)
	// socks5h, not socks5: with the h the proxy looks the name up. Without it a tool
	// looks it up itself, here, and the one who answers is the network's resolver.
	socks := fmt.Sprintf("socks5h://127.0.0.1:%d", port)
	return fmt.Sprintf("export http_proxy=%s https_proxy=%s all_proxy=%s HTTP_PROXY=%s HTTPS_PROXY=%s ALL_PROXY=%s", http, http, socks, http, http, socks)
}

// copyText puts text on the clipboard and says so.
func (a *app) copyText(c *ui.Context, text, said string) {
	mygo.Clipboard.WriteText(text)
	c.Toast(said)
}

// ruleSetsAge says how long ago the rule sets were last made current, and
// whether that is long enough to be worth a warning (more than three days: the
// automatic update has not been getting through).
func ruleSetsAge(checked, now time.Time) (text string, stale bool) {
	if checked.IsZero() {
		return "规则集：还没有", true
	}
	d := now.Sub(checked)
	switch {
	case d < time.Minute:
		return "规则集：刚刚更新", false
	case d < time.Hour:
		return fmt.Sprintf("规则集：%d 分钟前更新", int(d.Minutes())), false
	case d < 24*time.Hour:
		return fmt.Sprintf("规则集：%d 小时前更新", int(d.Hours())), false
	}
	days := int(d.Hours() / 24)
	if days > 3 {
		return fmt.Sprintf("规则集：%d 天没有更新成功", days), true
	}
	return fmt.Sprintf("规则集：%d 天前更新", days), false
}

// dnsPicker is one side's DNS server: its name, and the choice.
func (a *app) dnsPicker(c *ui.Context, title, side, current string, set func(id string)) {
	opts := singcfg.DNSOptions(side)
	labels := make([]string, len(opts))
	cur := ""
	for i, o := range opts {
		labels[i] = o.Label
		if o.ID == current || (current == "" && i == 0) {
			cur = o.Label
		}
	}
	a.setting(c, title, func() {
		p := a.dnsSel[side]
		if p == nil {
			p = new(string)
			a.dnsSel[side] = p
		}
		*p = cur
		if ui.Select(c, p, labels).Label(title + "的 DNS").Width(150).Changed() {
			for _, o := range opts {
				if o.Label == *p {
					set(o.ID)
				}
			}
		}
	})
}

// setDNS chooses both servers; the core restarts to use them, and the person is told.
func (a *app) setDNS(domestic, foreign string) {
	if domestic == "" {
		domestic = singcfg.DNSOptions("domestic")[0].ID
	}
	if foreign == "" {
		foreign = singcfg.DNSOptions("foreign")[0].ID
	}
	a.dnsErr = errText(a.eng.SetDNS(domestic, foreign))
}
