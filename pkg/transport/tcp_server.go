package transport

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"tds/pkg/registry"
)

// StartTCPServer starts a simple line-oriented TCP server that understands
// the same REGISTER / QUERY commands as the UDP server. Each connection is
// handled concurrently and may send multiple commands (one per line).
func StartTCPServer(reg registry.Registry, port int, onEvent func(string)) error {
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen tcp: %w", err)
	}
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			// transient accept error: log and continue
			fmt.Printf("tcp accept error: %v\n", err)
			continue
		}
		go handleTCPConn(conn, reg, onEvent)
	}
}

func handleTCPConn(conn net.Conn, reg registry.Registry, onEvent func(string)) {
	defer conn.Close()
	remote := conn.RemoteAddr()
	
	// Extract requestor IP for firewall filtering
	var requestorIP net.IP
	if tcpAddr, ok := remote.(*net.TCPAddr); ok {
		requestorIP = tcpAddr.IP
	}
	
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			// EOF or read error -> close connection
			return
		}
		line = strings.TrimSpace(line)
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		cmd := strings.ToUpper(parts[0])
		switch cmd {
		case "REGISTER":
			if len(parts) >= 3 {
				task := parts[1]
				addrParam := parts[2]
				reg.Register(task, addrParam)
				if onEvent != nil {
					onEvent(fmt.Sprintf("REGISTER %s -> %s from %v", task, addrParam, remote))
				}
				conn.Write([]byte("OK\n"))
			} else {
				conn.Write([]byte("ERR\n"))
			}
		case "QUERY":
			if len(parts) >= 2 {
				task := parts[1]
				addrStr, err := reg.GetServiceForRequestor(task, requestorIP)
				if onEvent != nil {
					onEvent(fmt.Sprintf("QUERY %s from %v", task, remote))
				}
				if err == registry.ErrNotFound || addrStr == "" {
					conn.Write([]byte("NOTFOUND\n"))
				} else if err == registry.ErrNoAllowedService {
					conn.Write([]byte("FORBIDDEN\n"))
				} else if err != nil {
					conn.Write([]byte("ERR\n"))
				} else {
					conn.Write([]byte(addrStr + "\n"))
				}
			} else {
				conn.Write([]byte("ERR\n"))
			}
		default:
			conn.Write([]byte("ERR\n"))
		}
	}
}
