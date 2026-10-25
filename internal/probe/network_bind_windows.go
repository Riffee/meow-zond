//go:build windows

package probe

import (
	"encoding/binary"
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

const ipUnicastIF = 31

func bindSocketToInterface(fd uintptr, _, address string, path networkPath) error {
	if path.Index <= 0 {
		return fmt.Errorf("original interface index is unavailable")
	}
	handle := syscall.Handle(fd)
	// Xray and Winsock expect the IPv4 interface index in network byte order.
	if strings.Contains(address, ".") {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], uint32(path.Index))
		index := int(*(*uint32)(unsafe.Pointer(&b[0])))
		if err := syscall.SetsockoptInt(handle, syscall.IPPROTO_IP, ipUnicastIF, index); err != nil {
			return fmt.Errorf("cannot bind IPv4 to original interface: %w", err)
		}
		return nil
	}
	if err := syscall.SetsockoptInt(handle, syscall.IPPROTO_IPV6, ipUnicastIF, path.Index); err != nil {
		return fmt.Errorf("cannot bind IPv6 to original interface: %w", err)
	}
	return nil
}
