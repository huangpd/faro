// Package sysproxy points macOS's system proxy at the local core, and
// puts it back.
//
// It changes the user's real network settings, so it is careful: what each
// network service had is written to a backup file before anything changes;
// Restore puts it back, and only where the setting still is the one this
// package made, so what the user changed since stays; Recover finishes a
// restore a crash left undone.
package sysproxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// Runner runs networksetup with args and returns its output.
type Runner func(args ...string) (string, error)

// ExecRunner runs the real networksetup.
func ExecRunner(args ...string) (string, error) {
	out, err := exec.Command("/usr/sbin/networksetup", args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("networksetup %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// OnlyRunner is ExecRunner limited to one network service: the others are
// neither listed nor touched. For trying the app without disturbing the
// service that carries the user's traffic.
func OnlyRunner(svc string) Runner {
	return func(args ...string) (string, error) {
		switch {
		case args[0] == "-listallnetworkservices":
			return "An asterisk (*) denotes that a network service is disabled.\n" + svc + "\n", nil
		case len(args) > 1 && args[1] != svc:
			return "", fmt.Errorf("refusing to touch %q: limited to %q", args[1], svc)
		}
		return ExecRunner(args...)
	}
}

// proxy is one of a service's three proxy settings.
type proxy struct {
	Enabled bool   `json:"enabled"`
	Server  string `json:"server"`
	Port    int    `json:"port"`
}

type service struct {
	Name   string   `json:"name"`
	Web    proxy    `json:"web"`
	Secure proxy    `json:"secure"`
	Socks  proxy    `json:"socks"`
	Bypass []string `json:"bypass"`
}

type backup struct {
	Host     string    `json:"host"`
	Port     int       `json:"port"`
	Services []service `json:"services"`
}

// Manager applies and reverts the system proxy.
type Manager struct {
	run  Runner
	path string // the backup file; it exists while the proxy is ours

	mu sync.Mutex
}

func New(run Runner, backupPath string) *Manager {
	return &Manager{run: run, path: backupPath}
}

// The kinds of setting, with the networksetup words for each.
var kinds = []struct {
	name, get, set, state string
	pick                  func(*service) *proxy
}{
	{"web", "-getwebproxy", "-setwebproxy", "-setwebproxystate", func(s *service) *proxy { return &s.Web }},
	{"secure", "-getsecurewebproxy", "-setsecurewebproxy", "-setsecurewebproxystate", func(s *service) *proxy { return &s.Secure }},
	{"socks", "-getsocksfirewallproxy", "-setsocksfirewallproxy", "-setsocksfirewallproxystate", func(s *service) *proxy { return &s.Socks }},
}

// ourBypass is kept off the proxy: local names and private networks.
var ourBypass = []string{"127.0.0.1", "localhost", "*.local", "169.254.0.0/16", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}

// Enable points every enabled network service at host:port. A failure
// part-way puts back what it changed.
func (m *Manager) Enable(host string, port int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := os.Stat(m.path); err == nil {
		if err := m.restore(); err != nil { // an earlier run's, or the core's old port
			return err
		}
	}
	names, err := m.services()
	if err != nil {
		return err
	}
	b := backup{Host: host, Port: port}
	for _, n := range names {
		s, err := m.read(n)
		if err != nil {
			return err
		}
		b.Services = append(b.Services, s)
	}
	if len(b.Services) == 0 {
		return errors.New("no network services")
	}
	// The backup comes first: if we die half-way, Recover has it.
	if err := m.write(b); err != nil {
		return err
	}
	for _, s := range b.Services {
		if err := m.apply(s, host, port); err != nil {
			_ = m.restore()
			return err
		}
	}
	return nil
}

// Disable puts the settings back. With nothing to undo it does nothing.
func (m *Manager) Disable() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.restore()
}

// Recover is Disable for the start of the app: a backup left by a run that
// ended without restoring is undone.
func (m *Manager) Recover() error { return m.Disable() }

// Active reports whether the system proxy is ours right now.
func (m *Manager) Active() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err := os.Stat(m.path)
	return err == nil
}

func (m *Manager) apply(s service, host string, port int) error {
	p := strconv.Itoa(port)
	for _, k := range kinds {
		if _, err := m.run(k.set, s.Name, host, p); err != nil { // also turns it on
			return err
		}
	}
	args := append([]string{"-setproxybypassdomains", s.Name}, mergeBypass(s.Bypass)...)
	_, err := m.run(args...)
	return err
}

func mergeBypass(existing []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range append(append([]string{}, ourBypass...), existing...) {
		if d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

// restore puts back each setting that is still ours. It keeps the backup
// if something failed, so a later call can finish.
func (m *Manager) restore() error {
	raw, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var b backup
	if err := json.Unmarshal(raw, &b); err != nil {
		os.Remove(m.path) // unreadable: nothing to go by
		return fmt.Errorf("proxy backup unreadable: %w", err)
	}
	current, err := m.services()
	if err != nil {
		return err
	}
	exists := map[string]bool{}
	for _, n := range current {
		exists[n] = true
	}
	var errs []error
	for _, saved := range b.Services {
		if !exists[saved.Name] {
			continue // the service is gone
		}
		now, err := m.read(saved.Name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, k := range kinds {
			cur, was := k.pick(&now), k.pick(&saved)
			// Ours, or half put back by an earlier try that failed after writing
			// the old address and before setting the state.
			ours := cur.Enabled && cur.Server == b.Host && cur.Port == b.Port
			half := cur.Enabled && cur.Server == was.Server && cur.Port == was.Port
			if !ours && !half {
				continue // changed since: the user's now
			}
			// The old address, empty included: this turns the setting on, and
			// the state below turns it off again if it was.
			if _, err := m.run(k.set, saved.Name, was.Server, strconv.Itoa(was.Port)); err != nil {
				errs = append(errs, err)
				continue
			}
			st := "off"
			if was.Enabled {
				st = "on"
			}
			if _, err := m.run(k.state, saved.Name, st); err != nil {
				errs = append(errs, err)
			}
		}
		if sameList(now.Bypass, mergeBypass(saved.Bypass)) {
			args := []string{"-setproxybypassdomains", saved.Name}
			if len(saved.Bypass) == 0 {
				args = append(args, "Empty")
			} else {
				args = append(args, saved.Bypass...)
			}
			if _, err := m.run(args...); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	return os.Remove(m.path)
}

func sameList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (m *Manager) write(b backup) error {
	raw, err := json.MarshalIndent(b, "", " ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

// services lists the enabled network services.
func (m *Manager) services() ([]string, error) {
	out, err := m.run("-listallnetworkservices")
	if err != nil {
		return nil, err
	}
	var names []string
	for i, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if i == 0 && strings.HasPrefix(line, "An asterisk") || line == "" || strings.HasPrefix(line, "*") {
			continue // the note, and the disabled ones
		}
		names = append(names, line)
	}
	return names, nil
}

func (m *Manager) read(name string) (service, error) {
	s := service{Name: name}
	for _, k := range kinds {
		out, err := m.run(k.get, name)
		if err != nil {
			return s, err
		}
		*k.pick(&s) = parseProxy(out)
	}
	out, err := m.run("-getproxybypassdomains", name)
	if err != nil {
		return s, err
	}
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "There aren't any") {
			s.Bypass = append(s.Bypass, l)
		}
	}
	return s, nil
}

// parseProxy reads the output of networksetup -get…proxy.
func parseProxy(out string) proxy {
	var p proxy
	for _, l := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "Enabled":
			p.Enabled = strings.EqualFold(v, "yes")
		case "Server":
			p.Server = v
		case "Port":
			p.Port, _ = strconv.Atoi(v)
		}
	}
	return p
}
