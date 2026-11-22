package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"faro/internal/singcfg"
	"faro/internal/sub"
)

// The whole way: a subscription served over HTTP is fetched, parsed and
// stored; the core starts from it and lists its nodes.
func TestSubscriptionToRunningCore(t *testing.T) {
	bin, _ := filepath.Abs("../../bin/sing-box")
	if _, err := os.Stat(bin); err != nil {
		t.Skip("no bin/sing-box")
	}
	links := strings.Join([]string{
		"vless://b831381d-6324-4d53-ad4f-8cda48b30811@192.0.2.1:1?security=reality&pbk=Ro_C3LfQ6ouz6d5L3F8bQ6D8XpJbG8vNlXjK0y3Vd0w&sid=ab&sni=apple.com&fp=chrome#香港 01",
		"trojan://pw@192.0.2.1:2?sni=example.com#日本 02",
		"hysteria2://pw@192.0.2.1:3?insecure=1#US 03",
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(links)) }))
	defer srv.Close()

	t.Setenv("FARO_CORE", bin)
	t.Setenv("HOME", t.TempDir())
	c, err := New("", nil)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := sub.OpenStore(filepath.Join(c.Dir(), "subscriptions.json"))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := st.Add(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	if err := c.Start(singcfg.Input{Nodes: st.Outbounds()}, 0); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	if _, err := c.WaitReady(ctx); err != nil {
		t.Fatalf("%v\n%q", err, c.Logs())
	}
	ps, err := c.API().Proxies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"香港 01", "日本 02", "US 03", "auto", "proxy"} {
		if _, ok := ps[tag]; !ok {
			t.Errorf("controller lacks %q", tag)
		}
	}
	if ps["proxy"].Now != "auto" {
		t.Errorf("default choice %q", ps["proxy"].Now)
	}
}
