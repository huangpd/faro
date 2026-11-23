package core

import (
	"os"
	"path/filepath"
	"strconv"
)

// logFile is the core's log on disk: lines appended to one file, which is
// moved aside at a size, keeping a few, so it can never grow past
// size*(keep+1). It is kept for the person to read; nothing reads it back.
type logFile struct {
	path       string
	size, keep int64
	f          *os.File
	n          int64
}

func newLogFile(path string, size, keep int64) *logFile {
	return &logFile{path: path, size: size, keep: keep}
}

// Write appends one line; a failure is dropped, as a log must not stop the core.
func (l *logFile) Write(line string) {
	if l.f == nil && !l.open() {
		return
	}
	if l.n >= l.size {
		l.rotate()
		if !l.open() {
			return
		}
	}
	n, err := l.f.WriteString(line + "\n")
	l.n += int64(n)
	if err != nil {
		l.Close()
	}
}

func (l *logFile) open() bool {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return false
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return false
	}
	l.f, l.n = f, st.Size()
	return true
}

func (l *logFile) rotate() {
	l.Close()
	os.Remove(l.path + "." + strconv.FormatInt(l.keep, 10))
	for i := l.keep - 1; i >= 1; i-- {
		os.Rename(l.path+"."+strconv.FormatInt(i, 10), l.path+"."+strconv.FormatInt(i+1, 10))
	}
	os.Rename(l.path, l.path+".1")
}

func (l *logFile) Close() {
	if l.f != nil {
		l.f.Close()
		l.f = nil
	}
}
