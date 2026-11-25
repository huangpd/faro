package core

import (
	"errors"
	"os/exec"
	"syscall"
	"testing"
)

// A process ended by a signal is told from one that failed, by its exit status.
func TestBySignal(t *testing.T) {
	cmd := exec.Command("sleep", "10")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	cmd.Process.Signal(syscall.SIGTERM)
	if err := cmd.Wait(); !bySignal(err) {
		t.Errorf("SIGTERM: %v not by a signal", err)
	}
	if err := exec.Command("sh", "-c", "exit 3").Run(); err == nil || bySignal(err) {
		t.Errorf("exit 3: %v", err)
	}
	if bySignal(errors.New("signal: terminated")) {
		t.Error("the words alone are not a signal")
	}
}
