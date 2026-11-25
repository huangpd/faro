// Package core runs sing-box as a separate process and supervises it.
//
// macOS has no PDEATHSIG, so a crashed app would leave the core running:
// the pid goes into a file, and the next launch ends a stale core first.
package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"faro/internal/clashapi"
	"faro/internal/singcfg"
)

// logLines is how many lines the window can show; logFileSize and logFiles
// bound the log on disk to about 15 MB.
const (
	logLines    = 2000
	logFileSize = 5 << 20
	logFiles    = 2 // kept besides the one being written
)

// ansi matches the terminal colors a program may put in its output.
var ansi = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// zone matches the time zone the core writes before every line ("+0800 "): the
// same on every line, and the same as the clock of the Mac the person reads it on.
var zone = regexp.MustCompile(`^[+-]\d{4} `)

type Core struct {
	bin, dir string

	mu      sync.Mutex
	cmd     *exec.Cmd
	done    chan struct{}
	set     singcfg.Settings
	logs    []string
	onLog   func()
	lastErr error
	notes   []string
}

// New finds the sing-box binary (in the app's resources, resDir, first) and
// the data directory. onLog is called
// (from another goroutine) when a log line arrives.
func New(resDir string, onLog func()) (*Core, error) {
	bin, err := findBinary(resDir)
	if err != nil {
		return nil, err
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(base, "Faro")
	migrateOldDir(filepath.Join(base, "MyBox"), dir) // the name it had before it was Faro
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	c := &Core{bin: bin, dir: dir, onLog: onLog}
	c.reapStale()
	return c, nil
}

// migrateOldDir copies what the app kept under its earlier name (the
// subscriptions, the rules, the settings) into the new directory, once: only
// when the new one does not exist yet. The old one is left as it is, so that a
// copy of the old app still run beside this one keeps working. What is
// transient (the core's pid, the proxy backup, downloaded rule sets, the
// generated config) is not copied: it is remade.
func migrateOldDir(from, to string) {
	if _, err := os.Stat(to); err == nil {
		return
	}
	entries, err := os.ReadDir(from)
	if err != nil {
		return
	}
	if err := os.MkdirAll(to, 0o700); err != nil {
		return
	}
	for _, e := range entries {
		switch e.Name() {
		case "subscriptions.json", "rules.txt", "prefs.json":
		default:
			continue
		}
		if b, err := os.ReadFile(filepath.Join(from, e.Name())); err == nil {
			_ = os.WriteFile(filepath.Join(to, e.Name()), b, 0o600)
		}
	}
}

func findBinary(resDir string) (string, error) {
	var cands []string
	if p := os.Getenv("FARO_CORE"); p != "" {
		cands = append(cands, p)
	}
	if resDir != "" {
		cands = append(cands, filepath.Join(resDir, "bin", "sing-box"))
	}
	if wd, err := os.Getwd(); err == nil {
		cands = append(cands, filepath.Join(wd, "bin/sing-box"))
	}
	for _, p := range cands {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", errors.New("sing-box not found: run scripts/fetch-singbox.sh, or set FARO_CORE")
}

// Dir is the data directory.
func (c *Core) Dir() string { return c.dir }

// CheckPort is the port of a route group's check entrance in this run, 0 for none.
func (c *Core) CheckPort(group string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.set.CheckPorts[group]
}

// Bin is the core program, which can also read a rule set out.
func (c *Core) Bin() string { return c.bin }

func (c *Core) pidFile() string { return filepath.Join(c.dir, "core.pid") }

// reapStale ends a core a crashed earlier run left behind. It checks the
// process is really sing-box before signalling a recycled pid.
func (c *Core) reapStale() {
	b, err := os.ReadFile(c.pidFile())
	if err != nil {
		return
	}
	defer os.Remove(c.pidFile())
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 {
		return
	}
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil || !strings.Contains(string(out), "sing-box") {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	time.Sleep(300 * time.Millisecond)
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

// Start checks the config, then runs the core. A check error comes back
// as the error, with sing-box's own message. mixed is the port of the local
// proxy, 0 for any free one; it is an error if that port is taken.
func (c *Core) Start(in singcfg.Input, mixed int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd != nil {
		return errors.New("already running")
	}
	set, err := singcfg.NewSettings(mixed)
	if err != nil {
		return err
	}
	if in.Plan != nil && len(in.Nodes) > 0 {
		for _, r := range in.Plan.Routes {
			if len(r.Exit) > 0 && len(r.Nodes) > 0 {
				if err := set.AddCheck(r.Group()); err != nil {
					return err
				}
			}
		}
	}
	cfg, notes, err := singcfg.Build(set, in)
	if err != nil {
		return err
	}
	c.notes = notes
	path := filepath.Join(c.dir, "config.json")
	if err := os.WriteFile(path, cfg, 0o600); err != nil {
		return err
	}
	if out, err := exec.Command(c.bin, "check", "-c", path).CombinedOutput(); err != nil {
		return fmt.Errorf("config check: %s", strings.TrimSpace(string(out)))
	}

	cmd := exec.Command(c.bin, "run", "-c", path, "-D", c.dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		return err
	}
	_ = os.WriteFile(c.pidFile(), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)

	c.cmd, c.set, c.lastErr = cmd, set, nil
	done := make(chan struct{})
	c.done = done
	go c.pump(pr)
	go func() {
		err := cmd.Wait()
		pw.Close()
		c.mu.Lock()
		if c.cmd == cmd {
			c.cmd = nil
			if err != nil && !bySignal(err) { // a stop is a signal: no error
				c.lastErr = err
			}
		}
		c.mu.Unlock()
		os.Remove(c.pidFile())
		close(done)
		c.notify()
	}()
	return nil
}

// bySignal says whether the process ended by a signal, from its exit status
// rather than the words of the error.
func bySignal(err error) bool {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return false
	}
	ws, ok := ee.Sys().(syscall.WaitStatus)
	return ok && ws.Signaled()
}

// pump keeps the last lines of the core's output in a ring.
func (c *Core) pump(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20) // a long line is kept, up to 1 MiB
	file := newLogFile(filepath.Join(c.dir, "logs", "core.log"), logFileSize, logFiles)
	defer file.Close()
	for sc.Scan() {
		line := zone.ReplaceAllString(ansi.ReplaceAllString(sc.Text(), ""), "")
		file.Write(line)
		c.mu.Lock()
		c.logs = append(c.logs, line)
		if n := len(c.logs); n > logLines {
			c.logs = append(c.logs[:0], c.logs[n-logLines:]...)
		}
		c.mu.Unlock()
		c.notify()
	}
	// A line longer still stops the scanner: what follows is read and dropped,
	// or the core would block on its next write to a pipe nobody reads.
	_, _ = io.Copy(io.Discard, r)
}

func (c *Core) notify() {
	if c.onLog != nil {
		c.onLog()
	}
}

// Stop sends SIGTERM, waits briefly, then SIGKILL.
func (c *Core) Stop() {
	c.mu.Lock()
	cmd, done := c.cmd, c.done
	c.mu.Unlock()
	if cmd == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		// Killed, it is gone; done also waits for its output to be read, and the
		// one who stops it must not wait on that for ever.
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
}

func (c *Core) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cmd != nil
}

// Notes are what the config left out, as a rule set that is not installed.
func (c *Core) Notes() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.notes...)
}

func (c *Core) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

func (c *Core) Logs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.logs...)
}

// MixedPort is the local proxy port of the running core.
func (c *Core) MixedPort() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.set.MixedPort
}

// API returns a controller client for the running core.
func (c *Core) API() *clashapi.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return clashapi.New(c.set.APIPort, c.set.Secret)
}

// WaitReady polls /version until the controller answers.
func (c *Core) WaitReady(ctx context.Context) (clashapi.Version, error) {
	api := c.API()
	for {
		v, err := api.Version(ctx)
		if err == nil {
			return v, nil
		}
		select {
		case <-ctx.Done():
			return v, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
		if !c.Running() {
			return v, errors.New("core exited")
		}
	}
}
