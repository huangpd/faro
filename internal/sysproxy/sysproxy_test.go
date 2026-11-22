package sysproxy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fake is an in-memory networksetup.
type fake struct {
	svcs     map[string]*service
	order    []string
	disabled map[string]bool
	failOn   string // a command word that fails
	calls    int
}

func newFake() *fake {
	f := &fake{svcs: map[string]*service{}, disabled: map[string]bool{}}
	f.add("Wi-Fi")
	f.add("Ethernet")
	return f
}

func (f *fake) add(n string) { f.svcs[n] = &service{Name: n}; f.order = append(f.order, n) }

func (f *fake) kind(cmd string) (func(*service) *proxy, string) {
	for _, k := range kinds {
		switch cmd {
		case k.get:
			return k.pick, "get"
		case k.set:
			return k.pick, "set"
		case k.state:
			return k.pick, "state"
		}
	}
	return nil, ""
}

func (f *fake) run(args ...string) (string, error) {
	f.calls++
	cmd := args[0]
	if cmd == f.failOn {
		return "", errors.New("boom " + cmd)
	}
	if cmd == "-listallnetworkservices" {
		out := "An asterisk (*) denotes that a network service is disabled.\n"
		for _, n := range f.order {
			if f.disabled[n] {
				n = "*" + n
			}
			out += n + "\n"
		}
		return out, nil
	}
	s := f.svcs[args[1]]
	if s == nil {
		return "", fmt.Errorf("no service %q", args[1])
	}
	if cmd == "-getproxybypassdomains" {
		if len(s.Bypass) == 0 {
			return "There aren't any bypass domains set on this service.\n", nil
		}
		return strings.Join(s.Bypass, "\n") + "\n", nil
	}
	if cmd == "-setproxybypassdomains" {
		s.Bypass = nil
		for _, d := range args[2:] {
			if d != "Empty" {
				s.Bypass = append(s.Bypass, d)
			}
		}
		return "", nil
	}
	pick, op := f.kind(cmd)
	p := pick(s)
	switch op {
	case "get":
		en := "No"
		if p.Enabled {
			en = "Yes"
		}
		return fmt.Sprintf("Enabled: %s\nServer: %s\nPort: %d\nAuthenticated Proxy Enabled: 0\n", en, p.Server, p.Port), nil
	case "set":
		port, _ := strconv.Atoi(args[3])
		*p = proxy{Enabled: true, Server: args[2], Port: port}
	case "state":
		p.Enabled = args[2] == "on"
	}
	return "", nil
}

func setup(t *testing.T) (*Manager, *fake) {
	f := newFake()
	return New(f.run, filepath.Join(t.TempDir(), "sysproxy.json")), f
}

func TestEnableDisableRestoresEverything(t *testing.T) {
	m, f := setup(t)
	// The user already has a corporate web proxy (on) and a socks one (off), and a bypass list.
	f.svcs["Wi-Fi"].Web = proxy{true, "corp.example", 3128}
	f.svcs["Wi-Fi"].Socks = proxy{false, "old.example", 1080}
	f.svcs["Wi-Fi"].Bypass = []string{"*.corp.example"}
	state := func() string { return fmt.Sprint(*f.svcs["Wi-Fi"], *f.svcs["Ethernet"]) }
	before := state()

	if err := m.Enable("127.0.0.1", 7000); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"Wi-Fi", "Ethernet"} {
		s := f.svcs[n]
		for _, p := range []proxy{s.Web, s.Secure, s.Socks} {
			if p != (proxy{true, "127.0.0.1", 7000}) {
				t.Errorf("%s not pointed at us: %+v", n, p)
			}
		}
	}
	if b := f.svcs["Wi-Fi"].Bypass; b[0] != "127.0.0.1" || b[len(b)-1] != "*.corp.example" {
		t.Errorf("bypass %v", b)
	}
	if !m.Active() {
		t.Error("not active")
	}

	if err := m.Disable(); err != nil {
		t.Fatal(err)
	}
	if after := state(); after != before {
		t.Errorf("not restored:\nbefore %s\nafter  %s", before, after)
	}
	if m.Active() {
		t.Error("still active after Disable")
	}
	if err := m.Disable(); err != nil || f.calls == 0 { // idempotent
		t.Error(err)
	}
}

func TestDisabledServicesLeftAlone(t *testing.T) {
	m, f := setup(t)
	f.disabled["Ethernet"] = true
	if err := m.Enable("127.0.0.1", 7000); err != nil {
		t.Fatal(err)
	}
	if f.svcs["Ethernet"].Web.Enabled {
		t.Error("touched a disabled service")
	}
}

func TestRestoreKeepsWhatTheUserChanged(t *testing.T) {
	m, f := setup(t)
	m.Enable("127.0.0.1", 7000)
	// While we ran, the user pointed Wi-Fi's web proxy somewhere else.
	f.svcs["Wi-Fi"].Web = proxy{true, "mine.example", 8888}
	m.Disable()
	if got := f.svcs["Wi-Fi"].Web; got != (proxy{true, "mine.example", 8888}) {
		t.Errorf("clobbered the user's change: %+v", got)
	}
	if f.svcs["Wi-Fi"].Secure.Enabled {
		t.Error("the setting still ours was not restored")
	}
}

func TestCrashRecovery(t *testing.T) {
	f := newFake()
	path := filepath.Join(t.TempDir(), "sysproxy.json")
	New(f.run, path).Enable("127.0.0.1", 7000)
	// The app died: a new Manager, same file, finds the backup.
	m2 := New(f.run, path)
	if !m2.Active() {
		t.Fatal("backup not found")
	}
	if err := m2.Recover(); err != nil {
		t.Fatal(err)
	}
	if f.svcs["Wi-Fi"].Web.Enabled || m2.Active() {
		t.Error("recover did not undo")
	}
}

func TestEnableAgainWithNewPort(t *testing.T) {
	m, f := setup(t)
	f.svcs["Wi-Fi"].Web = proxy{true, "corp.example", 3128}
	m.Enable("127.0.0.1", 7000)
	m.Enable("127.0.0.1", 7001) // the core restarted on another port
	if got := f.svcs["Wi-Fi"].Web; got.Port != 7001 {
		t.Errorf("port %d", got.Port)
	}
	m.Disable()
	if got := f.svcs["Wi-Fi"].Web; got != (proxy{true, "corp.example", 3128}) {
		t.Errorf("original lost across a re-enable: %+v", got)
	}
}

func TestFailurePartwayRollsBack(t *testing.T) {
	m, f := setup(t)
	f.failOn = "-setsecurewebproxy"
	if err := m.Enable("127.0.0.1", 7000); err == nil {
		t.Fatal("expected failure")
	}
	f.failOn = ""
	for n, s := range f.svcs {
		if s.Web.Enabled || s.Socks.Enabled {
			t.Errorf("%s left changed: %+v", n, s)
		}
	}
	if m.Active() {
		t.Error("backup left after a rolled-back enable")
	}
}

func TestRestoreFailureKeepsBackup(t *testing.T) {
	m, f := setup(t)
	m.Enable("127.0.0.1", 7000)
	f.failOn = "-setwebproxystate"
	if err := m.Disable(); err == nil {
		t.Fatal("expected failure")
	}
	if !m.Active() {
		t.Fatal("backup dropped though the restore failed")
	}
	f.failOn = ""
	if err := m.Disable(); err != nil || f.svcs["Wi-Fi"].Web.Enabled {
		t.Errorf("retry: %v %+v", err, f.svcs["Wi-Fi"].Web)
	}
}

func TestParseProxy(t *testing.T) {
	p := parseProxy("Enabled: Yes\nServer: 127.0.0.1\nPort: 7890\nAuthenticated Proxy Enabled: 0\n")
	if p != (proxy{true, "127.0.0.1", 7890}) {
		t.Errorf("%+v", p)
	}
	if p := parseProxy("Enabled: No\nServer: \nPort: 0\n"); p != (proxy{}) {
		t.Errorf("%+v", p)
	}
}

func TestUnreadableBackup(t *testing.T) {
	m, _ := setup(t)
	os.WriteFile(m.path, []byte("{nope"), 0o600)
	if err := m.Disable(); err == nil {
		t.Error("garbage backup accepted")
	}
	if m.Active() {
		t.Error("garbage backup left in the way")
	}
}
