package main

import (
	"strings"
	"testing"

	"meowzond/internal/probe"
)

func TestFitLineKeepsOneWindowWidth(t *testing.T) {
	colorEnabled = true
	line := paint(ansiMuted, "  https://status.example.com/very/long/path/that/must/not/wrap") + paint(ansiRed, " ошибка")
	fit := fitLine(line, 32)
	if got := visibleWidth(fit); got > 32 {
		t.Fatalf("visible width = %d, want <= 32: %q", got, fit)
	}
	if !strings.Contains(fit, "…") {
		t.Fatalf("long line was not truncated: %q", fit)
	}
	if !strings.HasSuffix(fit, ansiReset) {
		t.Fatalf("truncated coloured line must reset colour: %q", fit)
	}
	if fitLine("short", 32) != "short" {
		t.Fatal("short line changed")
	}
}

func TestScreenFrameFitsEveryWindowHeight(t *testing.T) {
	c := probe.Config{Server: "https://status.example.com", ProbeName: "Windows probe"}
	notice := "Зонд включён. Проверяю связь…"
	for _, height := range []int{8, 12, 15, 20, 25, 30, 40} {
		lines := screenFrameAt(c, nil, notice, true, 80, height)
		if len(lines) > height-1 {
			t.Fatalf("height %d: %d lines do not fit the window", height, len(lines))
		}
		if len(lines) == 0 || !strings.Contains(lines[len(lines)-1], "Выбор") {
			t.Fatalf("height %d: prompt is missing: %#v", height, lines)
		}
	}
}

func TestScreenFrameReflectsRevokedAccess(t *testing.T) {
	c := probe.Config{Server: "https://status.example.com", ProbeName: "Windows probe"}
	frame := strings.Join(screenFrameAt(c, nil, "", true, 80, 30), "\n")
	if !strings.Contains(frame, "ВЫКЛЮЧЕН") {
		t.Fatalf("off state missing: %s", frame)
	}
}
