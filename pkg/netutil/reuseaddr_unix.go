//go:build !windows

package netutil

import "syscall"

func reuseAddrControl(network, address string, c syscall.RawConn) error {
	var innerErr error
	if err := c.Control(func(fd uintptr) {
		innerErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
	}); err != nil {
		return err
	}
	return innerErr
}
