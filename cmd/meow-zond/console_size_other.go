//go:build !windows

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

func consoleSize() (int, int) {
	size, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0
	}
	return int(size.Col), int(size.Row)
}
