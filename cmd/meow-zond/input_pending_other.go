//go:build !windows

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// inputPending reports whether stdin already has bytes waiting. A periodic
// refresh skips that tick so a half-typed menu choice is never erased.
func inputPending() bool {
	ready, err := unix.IoctlGetInt(int(os.Stdin.Fd()), unix.TIOCINQ)
	return err == nil && ready > 0
}
