package prefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.json")
	p, err := Load(path)
	if err != nil || p.SystemProxy {
		t.Fatalf("defaults: %+v %v", p, err)
	}
	p.SystemProxy = true
	if err := p.Save(); err != nil {
		t.Fatal(err)
	}
	if p, err = Load(path); err != nil || !p.SystemProxy {
		t.Errorf("reload: %+v %v", p, err)
	}
	os.WriteFile(path, []byte("{bad"), 0o600)
	if _, err := Load(path); err == nil {
		t.Error("bad file accepted")
	}
}
