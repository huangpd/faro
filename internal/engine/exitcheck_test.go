package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The exit is asked of several services: one that is down, or answers in its
// own form, does not leave the route without an answer.
func TestTheExitIsAskedOfTheNextServiceWhenOneFails(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer down.Close()
	trace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("fl=1\nh=x\nip=198.51.100.12\nts=1\n"))
	}))
	defer trace.Close()
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("203.0.113.9\n")) }))
	defer plain.Close()
	junk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("<html>hi</html>")) }))
	defer junk.Close()
	ctx := context.Background()
	c := http.DefaultClient

	if ip, err := firstIP(ctx, c, []string{down.URL, trace.URL}); err != nil || ip != "198.51.100.12" {
		t.Errorf("after one down: %q %v", ip, err)
	}
	if ip, err := firstIP(ctx, c, []string{junk.URL, plain.URL}); err != nil || ip != "203.0.113.9" {
		t.Errorf("the address alone: %q %v", ip, err)
	}
	_, err := firstIP(ctx, c, []string{down.URL, junk.URL})
	if err == nil || !strings.Contains(err.Error(), "502") || !strings.Contains(err.Error(), "no address") {
		t.Errorf("none answered, and the error does not say why each: %v", err)
	}
}
