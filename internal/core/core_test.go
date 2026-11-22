package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"faro/internal/clashapi"
	"faro/internal/singcfg"
)

// Runs the real binary from bin/ (scripts/fetch-singbox.sh).
func TestStartControlStop(t *testing.T) {
	bin, _ := filepath.Abs("../../bin/sing-box")
	if _, err := os.Stat(bin); err != nil {
		t.Skip("no bin/sing-box")
	}
	t.Setenv("FARO_CORE", bin)
	t.Setenv("HOME", t.TempDir()) // keep the data dir out of the real one
	c, err := New("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(singcfg.Input{}, 0); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v, err := c.WaitReady(ctx)
	if err != nil {
		t.Fatalf("not ready: %v\nlogs: %q", err, c.Logs())
	}
	t.Logf("controller version %q", v.Version)

	got := make(chan clashapi.Traffic, 1)
	sctx, scancel := context.WithCancel(ctx)
	go c.API().StreamTraffic(sctx, func(tr clashapi.Traffic) {
		select {
		case got <- tr:
		default:
		}
	})
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("no traffic sample")
	}
	scancel()

	pid := c.cmd.Process.Pid
	c.Stop()
	if c.Running() {
		t.Fatal("still running after Stop")
	}
	if err := exec.Command("kill", "-0", strconv.Itoa(pid)).Run(); err == nil {
		t.Fatalf("pid %d still alive", pid)
	}
}

func TestReapStale(t *testing.T) {
	t.Setenv("FARO_CORE", "/bin/ls")
	t.Setenv("HOME", t.TempDir())
	c, err := New("", nil)
	if err != nil {
		t.Fatal(err)
	}
	// A pid file naming a live non-sing-box process must not kill it.
	sleep := exec.Command("sleep", "30")
	if err := sleep.Start(); err != nil {
		t.Fatal(err)
	}
	defer sleep.Process.Kill()
	os.WriteFile(c.pidFile(), []byte(strconv.Itoa(sleep.Process.Pid)), 0o600)
	c.reapStale()
	if err := exec.Command("kill", "-0", strconv.Itoa(sleep.Process.Pid)).Run(); err != nil {
		t.Fatal("reapStale killed an unrelated process")
	}
}

// With nodes in the config, the controller lists the groups, switches the
// selector and tests a node (here one that cannot be reached).
func TestNodesSelectAndDelay(t *testing.T) {
	bin, _ := filepath.Abs("../../bin/sing-box")
	if _, err := os.Stat(bin); err != nil {
		t.Skip("no bin/sing-box")
	}
	t.Setenv("FARO_CORE", bin)
	t.Setenv("HOME", t.TempDir())
	c, err := New("", nil)
	if err != nil {
		t.Fatal(err)
	}
	node := func(port int) map[string]any {
		return map[string]any{"type": "shadowsocks", "server": "127.0.0.1", "server_port": port, "method": "aes-256-gcm", "password": "p"}
	}
	nodes := []singcfg.Outbound{{Tag: "node a", Out: node(1)}, {Tag: "节点 b/c", Out: node(2)}}
	if err := c.Start(singcfg.Input{Nodes: nodes, Choices: map[string]string{"proxy": "节点 b/c"}}, 0); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := c.WaitReady(ctx); err != nil {
		t.Fatalf("not ready: %v\n%q", err, c.Logs())
	}
	api := c.API()
	ps, err := api.Proxies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if g := ps["proxy"]; g.Now != "节点 b/c" || len(g.All) != 3 || g.All[0] != "auto" {
		t.Fatalf("proxy group: %+v", g)
	}
	if err := api.Select(ctx, "proxy", "node a"); err != nil {
		t.Fatal(err)
	}
	if ps, _ = api.Proxies(ctx); ps["proxy"].Now != "node a" {
		t.Errorf("select did not stick: %+v", ps["proxy"])
	}
	if err := api.Select(ctx, "proxy", "nope"); err == nil {
		t.Error("selecting an unknown node succeeded")
	}
	if d, err := api.Delay(ctx, "节点 b/c", singcfg.ProbeURL); err == nil {
		t.Errorf("unreachable node tested at %d ms", d)
	}
}

// A program may colour its output for a terminal; the log holds the words only.
func TestLogHasNoTerminalColors(t *testing.T) {
	t.Setenv("FARO_CORE", "/bin/ls")
	t.Setenv("HOME", t.TempDir())
	c, err := New("", nil)
	if err != nil {
		t.Fatal(err)
	}
	c.pump(strings.NewReader("+0800 2026-11-22 01:21:21 \x1b[36mINFO\x1b[0m[0000] started\n\x1b[31mFATAL\x1b[0m boom\n"))
	logs := c.Logs()
	if len(logs) != 2 || logs[0] != "2026-11-22 01:21:21 INFO[0000] started" || logs[1] != "FATAL boom" {
		t.Errorf("logs %q", logs)
	}
}

// The app was called MyBox before it was Faro: what the person had is kept.
func TestMigratesTheOldDataDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FARO_CORE", "/bin/ls")
	old := filepath.Join(home, "Library", "Application Support", "MyBox")
	os.MkdirAll(old, 0o700)
	os.WriteFile(filepath.Join(old, "subscriptions.json"), []byte(`{"subs":[]}`), 0o600)
	os.WriteFile(filepath.Join(old, "rules.txt"), []byte("direct set:geosite-cn\n"), 0o600)
	os.WriteFile(filepath.Join(old, "prefs.json"), []byte(`{"mixedPort":7000}`), 0o600)
	os.WriteFile(filepath.Join(old, "core.pid"), []byte("123"), 0o600)
	os.WriteFile(filepath.Join(old, "config.json"), []byte("{}"), 0o600)

	c, err := New("", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"subscriptions.json", "rules.txt", "prefs.json"} {
		if _, err := os.Stat(filepath.Join(c.Dir(), name)); err != nil {
			t.Errorf("%s was not carried over", name)
		}
	}
	for _, name := range []string{"core.pid", "config.json"} {
		if _, err := os.Stat(filepath.Join(c.Dir(), name)); err == nil {
			t.Errorf("%s is transient and was copied", name)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(c.Dir(), "prefs.json")); string(b) != `{"mixedPort":7000}` {
		t.Errorf("prefs %q", b)
	}
	if _, err := os.Stat(filepath.Join(old, "subscriptions.json")); err != nil {
		t.Error("the old directory was disturbed")
	}

	// Once the new directory exists, the old one is not read again: changes are kept.
	os.WriteFile(filepath.Join(c.Dir(), "rules.txt"), []byte("changed\n"), 0o600)
	os.WriteFile(filepath.Join(old, "rules.txt"), []byte("older\n"), 0o600)
	c2, _ := New("", nil)
	if b, _ := os.ReadFile(filepath.Join(c2.Dir(), "rules.txt")); string(b) != "changed\n" {
		t.Errorf("the old directory overwrote newer rules: %q", b)
	}
}
