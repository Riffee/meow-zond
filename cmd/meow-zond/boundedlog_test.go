package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoundedLogKeepsOnlyLastLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probe.log")
	var out bytes.Buffer
	log := newBoundedLog(&out, path, 500)
	for i := 1; i <= 620; i++ {
		fmt.Fprintf(log, "line %d\n", i)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 500 {
		t.Fatalf("expected 500 lines, got %d", len(lines))
	}
	if lines[0] != "line 121" || lines[499] != "line 620" {
		t.Fatalf("unexpected ring contents: %q … %q", lines[0], lines[499])
	}
	if got := strings.Count(out.String(), "\n"); got != 620 {
		t.Fatalf("stdout must mirror every line, got %d", got)
	}
}
