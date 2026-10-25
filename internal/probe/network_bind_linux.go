//go:build linux

package probe

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func bindSocketToInterface(fd uintptr, _, _ string, path networkPath) error {
	if path.Interface == "" {
		return fmt.Errorf("original interface is empty")
	}
	if err := unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, path.Interface); err != nil {
		return fmt.Errorf("cannot bind to original interface %s: %w", path.Interface, err)
	}
	return nil
}
