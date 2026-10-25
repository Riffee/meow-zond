//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

func prepareConsole() {
	_ = windows.SetConsoleOutputCP(65001)
	_ = windows.SetConsoleCP(65001)
	for _, fd := range []uintptr{os.Stdout.Fd(), os.Stderr.Fd()} {
		handle := windows.Handle(fd)
		var mode uint32
		if windows.GetConsoleMode(handle, &mode) == nil {
			_ = windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
		}
	}
}
