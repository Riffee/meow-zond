//go:build !linux && !windows

package probe

import "fmt"

func bindSocketToInterface(uintptr, string, string, networkPath) error {
	return fmt.Errorf("original interface binding is not supported on this platform")
}
