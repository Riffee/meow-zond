package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const maxLogLine = 320

// boundedLog keeps the last max lines on disk and mirrors them to stdout.
// Docker's own json-file rotation is configured separately in compose.yaml.
type boundedLog struct {
	mu    sync.Mutex
	out   io.Writer
	path  string
	max   int
	lines []string
}

func newBoundedLog(out io.Writer, path string, max int) *boundedLog {
	if max <= 0 {
		max = 500
	}
	return &boundedLog{out: out, path: path, max: max}
}

func (l *boundedLog) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.out != nil {
		_, _ = l.out.Write(p)
	}
	text := strings.TrimSuffix(string(p), "\n")
	for _, line := range strings.Split(text, "\n") {
		if len(line) > maxLogLine {
			line = line[:maxLogLine] + "…"
		}
		l.lines = append(l.lines, line)
	}
	if len(l.lines) > l.max {
		l.lines = append([]string(nil), l.lines[len(l.lines)-l.max:]...)
	}
	l.flush()
	return len(p), nil
}

func (l *boundedLog) flush() {
	if l.path == "" {
		return
	}
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return
	}
	data := strings.Join(l.lines, "\n")
	if data != "" {
		data += "\n"
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(data), 0600); err != nil {
		return
	}
	if err := os.Rename(tmp, l.path); err != nil {
		_ = os.Remove(tmp)
	}
}
