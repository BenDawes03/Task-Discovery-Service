package netutil

import (
	"context"
	"net"
)

func listenConfigWithReuseAddr() net.ListenConfig {
	return net.ListenConfig{Control: reuseAddrControl}
}

// ListenTCP creates a TCP listener with SO_REUSEADDR enabled.
func ListenTCP(addr string) (net.Listener, error) {
	lc := listenConfigWithReuseAddr()
	return lc.Listen(context.Background(), "tcp", addr)
}

// ListenUDP creates a UDP socket with SO_REUSEADDR enabled.
func ListenUDP(addr string) (*net.UDPConn, error) {
	lc := listenConfigWithReuseAddr()
	pc, err := lc.ListenPacket(context.Background(), "udp", addr)
	if err != nil {
		return nil, err
	}
	uc, ok := pc.(*net.UDPConn)
	if !ok {
		_ = pc.Close()
		return nil, &net.OpError{Op: "listen", Net: "udp", Err: errUnexpectedPacketConn}
	}
	return uc, nil
}

var errUnexpectedPacketConn = &net.AddrError{Err: "unexpected packet conn type", Addr: "udp"}
