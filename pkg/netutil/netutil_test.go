package netutil

import (
	"net"
	"testing"
)

// TestListenTCP verifies that ListenTCP returns a working listener bound to
// the requested address and that the socket has SO_REUSEADDR set (demonstrated
// by immediately rebinding the same port after closing).
func TestListenTCP(t *testing.T) {
	ln, err := ListenTCP("127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenTCP failed: %v", err)
	}

	addr := ln.Addr().String()
	if addr == "" {
		t.Fatal("ListenTCP returned empty address")
	}

	// Verify it is a real TCP listener by accepting in background.
	connCh := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		connCh <- c
	}()

	dial, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial TCP listener: %v", err)
	}
	dial.Close()

	c := <-connCh
	if c != nil {
		c.Close()
	}
	ln.Close()

	// Rebind the same port – SO_REUSEADDR should make this succeed.
	ln2, err := ListenTCP(addr)
	if err != nil {
		t.Errorf("rebind after close failed (SO_REUSEADDR not set?): %v", err)
	} else {
		ln2.Close()
	}
}

// TestListenTCPInvalidAddress checks that an impossible address returns an error.
func TestListenTCPInvalidAddress(t *testing.T) {
	_, err := ListenTCP("256.256.256.256:0")
	if err == nil {
		t.Error("expected error for invalid address, got nil")
	}
}

// TestListenUDP verifies that ListenUDP returns a working UDP socket.
func TestListenUDP(t *testing.T) {
	conn, err := ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenUDP failed: %v", err)
	}
	defer conn.Close()

	addr := conn.LocalAddr().String()
	if addr == "" {
		t.Fatal("ListenUDP returned empty address")
	}

	// Roundtrip: send a packet from a separate socket and read it back.
	sender, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatalf("dial UDP: %v", err)
	}
	defer sender.Close()

	msg := []byte("hello")
	if _, err := sender.Write(msg); err != nil {
		t.Fatalf("send UDP: %v", err)
	}

	buf := make([]byte, 64)
	n, _, err := conn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("ReadFromUDP: %v", err)
	}
	if string(buf[:n]) != "hello" {
		t.Errorf("expected %q, got %q", "hello", buf[:n])
	}
}

// TestListenUDPInvalidAddress checks that an impossible address returns an error.
func TestListenUDPInvalidAddress(t *testing.T) {
	_, err := ListenUDP("256.256.256.256:0")
	if err == nil {
		t.Error("expected error for invalid address, got nil")
	}
}

// TestListenUDPReuseAddr demonstrates that SO_REUSEADDR allows rebinding the
// same UDP port after the first socket is closed.
func TestListenUDPReuseAddr(t *testing.T) {
	conn, err := ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatalf("initial ListenUDP: %v", err)
	}
	addr := conn.LocalAddr().String()
	conn.Close()

	conn2, err := ListenUDP(addr)
	if err != nil {
		t.Errorf("rebind after close failed (SO_REUSEADDR not set?): %v", err)
	} else {
		conn2.Close()
	}
}
