package transport

import (
	"encoding/json"
	"fmt"
	"net"

	"tds/pkg/registry"
)

// StartUDPServer starts a JSON-based UDP server that accepts commands:
//
//	{"cmd": "REGISTER", "task": "taskname", "address": "ip:port"}
//	{"cmd": "QUERY", "task": "taskname"}
//
// Responds with JSON:
//
//	{"status": "OK"}
//	{"status": "NOTFOUND"}
//	{"status": "FORBIDDEN"}
//	{"status": "ERR", "error": "..."}
//	{"status": "OK", "address": "ip:port"}
//
// onEvent, if non-nil, will be called with short human-readable messages for UI/logging.
func StartUDPServer(reg registry.Registry, port int, onEvent func(string)) error {
	addr := net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: port}
	conn, err := net.ListenUDP("udp", &addr)
	if err != nil {
		return fmt.Errorf("listen udp: %w", err)
	}
	defer conn.Close()

	for {
		buf := make([]byte, 4096)
		n, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			// transient read error: log and continue
			fmt.Printf("udp read error: %v\n", err)
			continue
		}

		// Handle each request concurrently.
		data := make([]byte, n)
		copy(data, buf[:n])
		go handleUDPRequest(conn, reg, data, remote, onEvent)
	}
}

func handleUDPRequest(conn *net.UDPConn, reg registry.Registry, data []byte, remote *net.UDPAddr, onEvent func(string)) {
	var msg CentralizedMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		resp := CentralizedResponse{Status: "ERR", Error: "invalid JSON: " + err.Error()}
		respData, err := json.Marshal(resp)
		if err != nil {
			fmt.Printf("udp marshal error: %v\n", err)
			return
		}
		if _, err := conn.WriteToUDP(respData, remote); err != nil {
			fmt.Printf("udp write error (invalid JSON response): %v\n", err)
		}
		return
	}

	resp := HandleMessage(reg, msg, remote.IP, remote, onEvent)
	respData, err := json.Marshal(resp)
	if err != nil {
		fmt.Printf("udp marshal error: %v\n", err)
		return
	}
	if _, err := conn.WriteToUDP(respData, remote); err != nil {
		fmt.Printf("udp write error: %v\n", err)
	}
}
