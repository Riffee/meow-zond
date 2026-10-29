//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// inputPending reports whether the console already has typed characters that
// the reader goroutine has not consumed yet. A periodic refresh skips that tick
// so a half-typed menu choice is never erased.
func inputPending() bool {
	var events uint32
	if err := windows.GetNumberOfConsoleInputEvents(windows.Handle(os.Stdin.Fd()), &events); err != nil {
		return false
	}
	return events > 0
}
