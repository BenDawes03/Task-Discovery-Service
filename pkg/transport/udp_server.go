package transport

import (
	"fmt"
	"net"
	"strings"
	"tds/pkg/registry"
)

// StartUDPServer starts a minimal UDP server that accepts two commands (single-line, whitespace-separated):
// REGISTER <task> <address>  -> updates registry and replies OK
// QUERY <task>              -> replies with the selected address or NOTFOUND
// StartUDPServer starts a minimal UDP server that accepts two commands (single-line, whitespace-separated):
// REGISTER <task> <address>  -> updates registry and replies OK
// QUERY <task>              -> replies with the selected address or NOTFOUND
// onEvent, if non-nil, will be called with short human-readable messages for UI/logging.
func StartUDPServer(reg registry.Registry, port int, onEvent func(string)) error {
	addr := net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: port}
	conn, err := net.ListenUDP("udp", &addr)
	if err != nil {
		return fmt.Errorf("listen udp: %w", err)
	}
	defer conn.Close()
	buf := make([]byte, 1024)
	for {
		n, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			// transient read error: log and continue
			fmt.Printf("udp read error: %v\n", err)
			continue
		}
		line := strings.TrimSpace(string(buf[:n]))
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
				conn.WriteToUDP([]byte("OK"), remote)
			} else {
				conn.WriteToUDP([]byte("ERR"), remote)
			}
		case "QUERY":
			if len(parts) >= 2 {
				task := parts[1]
				// Extract requestor IP for firewall filtering
				requestorIP := remote.IP
				addrStr, err := reg.GetServiceForRequestor(task, requestorIP)
				if onEvent != nil {
					onEvent(fmt.Sprintf("QUERY %s from %v", task, remote))
				}
				if err == registry.ErrNotFound || addrStr == "" {
					conn.WriteToUDP([]byte("NOTFOUND"), remote)
				} else if err == registry.ErrNoAllowedService {
					conn.WriteToUDP([]byte("FORBIDDEN"), remote)
				} else if err != nil {
					conn.WriteToUDP([]byte("ERR"), remote)
				} else {
					conn.WriteToUDP([]byte(addrStr), remote)
				}
			} else {
				conn.WriteToUDP([]byte("ERR"), remote)
			}
		default:
			conn.WriteToUDP([]byte("ERR"), remote)
		}
	}
}
