package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"faro/internal/domains"
	"faro/internal/ruleset"
	"faro/internal/singcfg"
	"faro/internal/sub"
)

// A configuration file is everything a person has set up, in one file, to
// carry to another Mac or keep aside: the rules text, the groups, the
// subscriptions with their nodes and the node chosen for each route, and the
// settings. Not in it: what this Mac has fetched or worked out (the rule sets,
// the logs, the config the core is run with), and what it keeps of the system
// (the proxy's backup, the window).
//
// It holds the subscriptions' addresses and the nodes' passwords: whoever has
// the file can use them.

// configFormat is the version of the file's layout. A file of a later one is
// refused, not half read.
const configFormat = 1

// ConfigFile is the file's content.
type ConfigFile struct {
	Faro          int                `json:"faro"` // the layout's version: configFormat
	App           string             `json:"app,omitempty"`
	Exported      time.Time          `json:"exported"`
	Rules         string             `json:"rules"`
	Groups        []domains.Group    `json:"groups"`
	Subscriptions []sub.Subscription `json:"subscriptions"`
	Choices       map[string]string  `json:"choices"`
	Settings      ConfigSettings     `json:"settings"`
}

// ConfigSettings are the settings that travel. RuleSetsChecked does not: it
// is about the files on this Mac.
type ConfigSettings struct {
	SystemProxy bool   `json:"systemProxy"`
	LightMode   bool   `json:"lightMode"`
	MixedPort   int    `json:"mixedPort"`
	DNSDomestic string `json:"dnsDomestic"`
	DNSForeign  string `json:"dnsForeign"`
}

// ImportResult says what an import put in place.
type ImportResult struct {
	Subscriptions, Nodes, Groups int
	Backup                       string   // the file the configuration before it was kept in
	Fetching                     []string // rule sets it uses that this Mac has not, being fetched
	PortKept                     int      // the file's port was taken here: this one, the old, is kept (0: it was not)
}

// ExportConfig is the configuration in force, as a file's content. app is
// the version of Faro that wrote it, for the reader.
func (e *Engine) ExportConfig(app string) ([]byte, error) {
	e.mu.Lock()
	cfg := e.configLocked()
	e.mu.Unlock()
	cfg.App = app
	return json.MarshalIndent(cfg, "", "  ")
}

func (e *Engine) configLocked() ConfigFile {
	subs, choices := e.store.Snapshot()
	return ConfigFile{
		Faro: configFormat, Exported: e.opts.Now(),
		Rules: e.rulesText, Groups: domains.Copy(e.groupsList),
		Subscriptions: subs, Choices: choices,
		Settings: ConfigSettings{
			SystemProxy: e.prefs.SystemProxy, LightMode: e.prefs.LightMode, MixedPort: e.prefs.MixedPort,
			DNSDomestic: e.prefs.DNSDomestic, DNSForeign: e.prefs.DNSForeign,
		},
	}
}

// ParseConfig reads a file's content and checks it whole: its layout, its
// subscriptions and groups, its settings, and that its rules and groups
// compile against its own nodes. Nothing is changed.
func ParseConfig(b []byte) (*ConfigFile, error) {
	var cfg ConfigFile
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("不是 Faro 的配置文件：%v", err)
	}
	switch {
	case cfg.Faro == 0:
		return nil, errors.New("不是 Faro 导出的配置文件")
	case cfg.Faro > configFormat:
		return nil, fmt.Errorf("这个文件来自更新版本的 Faro（格式 %d），请先升级 Faro", cfg.Faro)
	}
	ids := map[string]bool{}
	for i := range cfg.Subscriptions {
		s := &cfg.Subscriptions[i]
		if s.ID == "" || ids[s.ID] {
			s.ID = newConfigID()
		}
		ids[s.ID] = true
		for _, n := range s.Nodes {
			if t, _ := n.Out["type"].(string); t == "" {
				return nil, fmt.Errorf("订阅 %s 的节点 %q 不完整", s.Name, n.Name)
			}
		}
	}
	names := map[string]bool{}
	for _, g := range cfg.Groups {
		if strings.TrimSpace(g.Name) == "" {
			return nil, errors.New("有一个分组没有名字")
		}
		if names[g.Name] {
			return nil, fmt.Errorf("分组 %s 出现了两次", g.Name)
		}
		names[g.Name] = true
	}
	st := cfg.Settings
	if st.MixedPort != 0 && (st.MixedPort < 1024 || st.MixedPort > 65535) {
		return nil, fmt.Errorf("端口 %d 不在 1024 到 65535 之间", st.MixedPort)
	}
	if !dnsKnown("domestic", st.DNSDomestic) || !dnsKnown("foreign", st.DNSForeign) {
		return nil, fmt.Errorf("不认识的 DNS 服务器：%q 或 %q", st.DNSDomestic, st.DNSForeign)
	}
	if cfg.Rules == "" {
		return nil, errors.New("文件里没有规则文本")
	}
	if _, errs, _, _ := compileWithGroups(cfg.Groups, cfg.Rules, configTags(&cfg), cfg.Choices); errs != nil {
		return nil, fmt.Errorf("规则有错误：%s", errs[0].Error())
	}
	return &cfg, nil
}

// dnsKnown says whether id is one of a side's servers; "" is the first.
func dnsKnown(side, id string) bool {
	if id == "" {
		return true
	}
	for _, s := range singcfg.DNSOptions(side) {
		if s.ID == id {
			return true
		}
	}
	return false
}

// configTags are the node tags the file's subscriptions give, as the store
// would give them.
func configTags(cfg *ConfigFile) []string {
	st := &sub.Store{}
	for i := range cfg.Subscriptions {
		st.Subs = append(st.Subs, &cfg.Subscriptions[i])
	}
	outs := st.Outbounds()
	tags := make([]string, len(outs))
	for i, o := range outs {
		tags[i] = o.Tag
	}
	return tags
}

func newConfigID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ImportConfig puts a file's configuration in place of the one in force,
// once it is checked whole. The one in force is first kept in a file of its
// own (Backup), so that an import can be undone by importing that. A port
// the file names that something here already holds is not taken: the old
// one is kept, and said. Rule sets it uses that this Mac has not are fetched
// after, and a running core restarts with them.
func (e *Engine) ImportConfig(b []byte) (ImportResult, error) {
	cfg, err := ParseConfig(b)
	if err != nil {
		return ImportResult{}, err
	}
	backup, err := e.keepBackup()
	if err != nil {
		return ImportResult{}, fmt.Errorf("没能先备份当前配置，没有导入：%v", err)
	}
	res := ImportResult{Subscriptions: len(cfg.Subscriptions), Nodes: len(configTags(cfg)), Groups: len(cfg.Groups), Backup: backup}

	e.mu.Lock()
	cur := e.prefs.MixedPort
	e.mu.Unlock()
	if p := cfg.Settings.MixedPort; p == 0 {
		cfg.Settings.MixedPort = cur
	} else if p != cur {
		if err := singcfg.CheckPort(p); err != nil {
			res.PortKept, cfg.Settings.MixedPort = cur, cur
		}
	}

	if err := e.applyConfig(cfg); err != nil {
		// Half put in place: the backup goes back, as far as it can.
		if old, rerr := os.ReadFile(backup); rerr == nil {
			if prev, perr := ParseConfig(old); perr == nil {
				_ = e.applyConfig(prev)
			}
		}
		return ImportResult{}, fmt.Errorf("导入失败，已恢复原配置：%v", err)
	}
	res.Fetching = e.missingSets()
	if len(res.Fetching) > 0 {
		go e.fetchMissingSets(res.Fetching)
	}
	go e.refreshOverlaps()
	return res, nil
}

// keepBackup writes the configuration in force to the backups folder.
func (e *Engine) keepBackup() (string, error) {
	b, err := e.ExportConfig("")
	if err != nil {
		return "", err
	}
	dir := filepath.Join(e.core.Dir(), "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "导入前-"+e.opts.Now().Format("20060102-150405")+".json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// applyConfig writes a checked configuration to the files and puts it in
// force; a running core restarts on it.
func (e *Engine) applyConfig(cfg *ConfigFile) error {
	e.mu.Lock()
	dir := e.core.Dir()
	if err := e.store.Replace(cfg.Subscriptions, cfg.Choices); err != nil {
		e.mu.Unlock()
		return err
	}
	if err := domains.Save(filepath.Join(dir, "domains.json"), cfg.Groups); err != nil {
		e.mu.Unlock()
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, "rules.txt"), []byte(cfg.Rules)); err != nil {
		e.mu.Unlock()
		return err
	}
	st := cfg.Settings
	e.prefs.SystemProxy, e.prefs.LightMode, e.prefs.MixedPort = st.SystemProxy, st.LightMode, st.MixedPort
	e.prefs.DNSDomestic, e.prefs.DNSForeign = st.DNSDomestic, st.DNSForeign
	if err := e.prefs.Save(); err != nil {
		e.mu.Unlock()
		return err
	}
	e.rulesText, e.groupsList = cfg.Rules, domains.Copy(cfg.Groups)
	e.err = ""
	e.reloadLocked()
	running := e.status != Stopped
	e.mu.Unlock()
	e.changed()
	if running {
		// The proxy follows: Restart puts it back first, and the new run sets
		// it again only if the imported settings want it.
		e.Restart()
	}
	return nil
}

func writeFileAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// missingSets are the rule sets the rules in force use, by the groups or the
// text, that are not on this Mac.
func (e *Engine) missingSets() []string {
	e.mu.Lock()
	plan := e.plan
	e.mu.Unlock()
	if plan == nil {
		return nil
	}
	have := ruleset.Paths(e.setsDir)
	want := map[string]bool{}
	for _, r := range plan.Rules {
		for _, m := range r.Matchers {
			if m.Kind == "rule_set" || m.Kind == "rule_set_ip" {
				for _, v := range m.Values {
					if _, ok := have[v]; !ok {
						want[v] = true
					}
				}
			}
		}
	}
	out := make([]string, 0, len(want))
	for n := range want {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// fetchMissingSets fetches rule sets an import uses, then has a running core
// load them: one restart for all. What did not come is said; the rules work
// without it meanwhile, as when a set cannot be fetched.
func (e *Engine) fetchMissingSets(names []string) {
	e.setFetch.Lock()
	defer e.setFetch.Unlock()
	var got, failed []string
	mine := ""
	for _, name := range names {
		e.mu.Lock()
		port := e.proxyPortLocked()
		if e.busy == "" || e.busy == mine {
			mine = "正在取规则集 " + strings.TrimPrefix(name, "geosite-") + "…"
			e.busy = mine
		}
		e.mu.Unlock()
		e.changed()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		err := e.opts.FetchSet(ctx, e.setsDir, name, port)
		cancel()
		if err != nil {
			failed = append(failed, strings.TrimPrefix(name, "geosite-"))
		} else {
			got = append(got, name)
		}
	}
	e.mu.Lock()
	if mine != "" && e.busy == mine {
		e.busy = ""
	}
	if len(failed) > 0 {
		e.err = "导入的配置用到的规则集 " + strings.Join(failed, "、") + " 没取到，先不按它们分流"
	}
	running := e.status != Stopped
	e.mu.Unlock()
	e.changed()
	if len(got) > 0 {
		go e.refreshOverlaps()
		if running {
			e.Restart() // a set not on disk was left out of the config: it is in now
		}
	}
}
