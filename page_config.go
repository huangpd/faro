package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"faro/internal/engine"
)

// The whole configuration goes to one file and comes back from one: to move
// to another Mac, or to keep a copy aside. The engine says what is in it
// (engine.ConfigFile); here are the buttons, the questions and the answers.

// configIO is how the file is chosen and how an answer comes back to the
// interface: the system's dialogs and main thread, or a test's own.
type configIO struct {
	save func(name string) (string, error) // where to write; "" when cancelled
	open func() (string, error)            // what to read; "" when cancelled
	post func(func())                      // runs on the main thread
}

func nativeConfigIO() configIO {
	filter := []mygo.FileFilter{{Name: "Faro 配置", Extensions: []string{"json"}}}
	return configIO{
		save: func(name string) (string, error) {
			return mygo.Dialog.Save(mygo.SaveDialogOptions{Title: "导出 Faro 配置", DefaultPath: name, Filters: filter, CreateDirectories: true})
		},
		open: func() (string, error) {
			paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "导入 Faro 配置", Filters: filter})
			if err != nil || len(paths) == 0 {
				return "", err
			}
			return paths[0], nil
		},
		post: mygo.App.WhenReady,
	}
}

// configState is what the configuration's part of the settings is in the
// middle of.
type configState struct {
	busy       bool   // a dialog is open, or a file being read
	exportAsk  bool   // asking before the file is written: it holds passwords
	importAsk  bool   // asking before the configuration is replaced
	importData []byte // the file being asked about, checked
	importWhat string // what it holds, in a sentence
	said       string // the last outcome
	err        string
}

// configSettings are the export and import buttons, and what they said.
func (a *app) configSettings(c *ui.Context) {
	s := &a.cfg
	a.group(c, "配置", func() {
		a.setting(c, "导出全部配置", func() {
			b := a.button(c, btnSecondary, "download", "导出…")
			if s.busy {
				b.Disabled(true)
			}
			if b.Clicked() && !s.busy {
				s.exportAsk = true
			}
		})
		ui.Divider(c)
		a.setting(c, "从文件导入配置", func() {
			b := a.button(c, btnSecondary, "upload", "导入…")
			if s.busy {
				b.Disabled(true)
			}
			if b.Clicked() && !s.busy {
				a.chooseImport()
			}
		})
	})
	if s.err != "" {
		a.banner(c, a.k.danger, "shield-alert", s.err)
	} else if s.said != "" {
		a.banner(c, a.k.ok, "check", s.said)
	}
	if s.exportAsk && ui.AlertDialog(c, &s.exportAsk, "导出配置？",
		"文件里有你的订阅链接和节点密码，拿到它的人可以直接使用你的节点。请自己保管，不要发给别人。", "取消", "导出") == 1 {
		a.exportConfig()
	}
	if s.importAsk && ui.AlertDialog(c, &s.importAsk, "导入并替换当前配置？",
		s.importWhat+"当前的订阅、出口、应用和设置会被替换；替换前会先备份当前配置。", "取消", "导入") == 1 {
		a.importConfig()
	}
}

// exportConfig asks where, then writes the file there.
func (a *app) exportConfig() {
	s := &a.cfg
	s.busy, s.said, s.err = true, "", ""
	name := "Faro-配置-" + time.Now().Format("20060102") + ".json"
	app := version() // read here, on the main thread
	go func() {
		path, err := a.cfgIO.save(name)
		var b []byte
		if err == nil && path != "" {
			if b, err = a.eng.ExportConfig(app); err == nil {
				err = os.WriteFile(path, b, 0o600) // it holds passwords: for this user only
			}
		}
		a.cfgIO.post(func() {
			s.busy = false
			switch {
			case err != nil:
				s.err = "导出失败：" + err.Error()
			case path != "":
				s.said = "已导出到 " + path
			}
			a.refresh()
		})
	}()
}

// chooseImport asks for the file, reads it and checks it, then asks before
// putting it in place. Nothing changes until that is answered.
func (a *app) chooseImport() {
	s := &a.cfg
	s.busy, s.said, s.err = true, "", ""
	go func() {
		path, err := a.cfgIO.open()
		var b []byte
		var cfg *engine.ConfigFile
		if err == nil && path != "" {
			if b, err = os.ReadFile(path); err == nil {
				cfg, err = engine.ParseConfig(b)
			}
		}
		a.cfgIO.post(func() {
			s.busy = false
			switch {
			case err != nil:
				s.err = "不能导入：" + err.Error()
			case cfg != nil:
				s.importData, s.importWhat, s.importAsk = b, configSummary(cfg), true
			}
			a.refresh()
		})
	}()
}

// importConfig puts the checked file in place.
func (a *app) importConfig() {
	s := &a.cfg
	res, err := a.eng.ImportConfig(s.importData)
	s.importData = nil
	if err != nil {
		s.err = err.Error()
		return
	}
	said := fmt.Sprintf("已导入：%d 个订阅、%d 个节点、%d 个应用。原配置已备份到 %s", res.Subscriptions, res.Nodes, res.Groups, res.Backup)
	if res.PortKept != 0 {
		said += fmt.Sprintf("。文件里的端口在这台 Mac 上已被占用，继续使用 %d", res.PortKept)
	}
	if len(res.Fetching) > 0 {
		said += "。正在下载规则集：" + strings.Join(res.Fetching, "、")
	}
	s.said = said
	a.rulesEdit, a.rulesErrs = a.eng.State().RulesText, nil // the rules page shows the new text, not the old draft
}

// configSummary is what a file holds, in a sentence for the question.
func configSummary(cfg *engine.ConfigFile) string {
	nodes := 0
	for _, s := range cfg.Subscriptions {
		nodes += len(s.Nodes)
	}
	from := ""
	if cfg.App != "" {
		from = "Faro " + cfg.App + " "
	}
	return fmt.Sprintf("这个文件由 %s于 %s 导出，包含 %d 个订阅、%d 个节点、%d 个应用。",
		from, cfg.Exported.Local().Format("2006-01-02 15:04"), len(cfg.Subscriptions), nodes, len(cfg.Groups))
}
