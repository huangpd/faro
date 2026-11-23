package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogFileRotatesAndStaysUnderItsBound(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "logs", "faro.log")
	l := newLogFile(p, 100, 2)
	line := strings.Repeat("x", 49) // 50 bytes with its newline
	for i := 0; i < 40; i++ {
		l.Write(line)
	}
	l.Close()
	var total int64
	names, _ := filepath.Glob(p + "*")
	for _, n := range names {
		st, _ := os.Stat(n)
		total += st.Size()
		if st.Mode().Perm() != 0o600 {
			t.Errorf("%s is %v", n, st.Mode().Perm())
		}
	}
	if len(names) != 3 {
		t.Errorf("files %v, want the log and 2 kept", names)
	}
	if total > 100*3+50 {
		t.Errorf("%d bytes kept, more than the bound", total)
	}
}
