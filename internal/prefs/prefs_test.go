package prefs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestExitCheckSchedule(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 1, 1, h, m, 0, 0, time.Local) }
	every, in, err := ExitCheck{}.Schedule()
	if err != nil || every != DefaultExitEvery || !in(at(3, 0)) {
		t.Errorf("empty: %v %v %v", every, in(at(3, 0)), err)
	}
	every, in, err = ExitCheck{Every: "5m", Hours: "09:00-23:30"}.Schedule()
	if err != nil || every != 5*time.Minute {
		t.Fatalf("%v %v", every, err)
	}
	if in(at(8, 59)) || !in(at(9, 0)) || !in(at(23, 29)) || in(at(23, 30)) {
		t.Error("daytime hours read wrong")
	}
	_, in, _ = ExitCheck{Hours: "22:00-06:00"}.Schedule()
	if !in(at(23, 0)) || !in(at(5, 59)) || in(at(6, 0)) || in(at(12, 0)) {
		t.Error("hours across midnight read wrong")
	}
	if every, _, _ = (ExitCheck{Every: "1s"}).Schedule(); every != MinExitEvery {
		t.Errorf("too short: %v", every)
	}
	every, in, err = ExitCheck{Every: "often", Hours: "morning"}.Schedule()
	if err == nil || every != DefaultExitEvery || !in(at(3, 0)) {
		t.Errorf("bad values: %v %v %v", every, in(at(3, 0)), err)
	}
}

func TestExitCheckIsReadAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.json")
	p, _ := Load(path)
	_ = p.Save()
	if err := os.WriteFile(path, []byte(`{"exitCheck":{"every":"30s","hours":"08:00-20:00"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	x, err := p.ReloadExitCheck()
	if err != nil || x.Every != "30s" || p.ExitCheck.Hours != "08:00-20:00" {
		t.Errorf("%+v %v", x, err)
	}
}
