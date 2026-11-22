package sysproxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Runs the real networksetup, on ONE network service only, so the user's
// other services (the one carrying their traffic) are never touched.
//
//	FARO_LIVE_SERVICE=Tailscale go test ./internal/sysproxy -run Live -v
func TestLiveSingleService(t *testing.T) {
	svc := os.Getenv("FARO_LIVE_SERVICE")
	if svc == "" {
		t.Skip("set FARO_LIVE_SERVICE to a service that carries no traffic")
	}
	only := OnlyRunner(svc)
	m := New(only, filepath.Join(t.TempDir(), "sysproxy.json"))
	read := func() service { s, err := m.read(svc); must(t, err); return s }
	before := read()
	t.Cleanup(func() { m.Disable() }) // whatever happens, put it back

	must(t, m.Enable("127.0.0.1", 17999))
	during := read()
	for _, p := range []proxy{during.Web, during.Secure, during.Socks} {
		if p != (proxy{true, "127.0.0.1", 17999}) {
			t.Errorf("not enabled: %+v", p)
		}
	}
	if !strings.Contains(strings.Join(during.Bypass, ","), "192.168.0.0/16") {
		t.Errorf("bypass %v", during.Bypass)
	}

	must(t, m.Disable())
	after := read()
	if after.Web.Enabled || after.Secure.Enabled || after.Socks.Enabled {
		t.Errorf("still enabled: %+v", after)
	}
	if after.Web != before.Web || after.Secure != before.Secure || after.Socks != before.Socks {
		t.Errorf("not as before: %+v vs %+v", before, after)
	}
	if strings.Join(after.Bypass, ",") != strings.Join(before.Bypass, ",") {
		t.Errorf("bypass: before %v after %v", before.Bypass, after.Bypass)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
