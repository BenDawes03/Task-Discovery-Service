//go:build windows

package transport

import "net"

// enableBroadcast is a no-op on Windows builds; the socket permits UDP broadcasts.
func enableBroadcast(conn *net.UDPConn) error {
	return nil
}
