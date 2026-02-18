package transport

import (
	"encoding/json"
	"fmt"
	"net"

	"tds/pkg/registry"
)

// StartUDPServer starts a JSON-based UDP server that accepts commands:
//   {"cmd": "REGISTER", "task": "taskname", "address": "ip:port"}
//   {"cmd": "QUERY", "task": "taskname"}
//
// Responds with JSON:
//   {"status": "OK"}
//   {"status": "NOTFOUND"}
//   {"status": "FORBIDDEN"}
//   {"status": "ERR", "error": "..."}
//   {"status": "OK", "address": "ip:port"}
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
		respData, _ := json.Marshal(CentralizedResponse{Status: "ERR", Error: "invalid JSON: " + err.Error()})
		_, _ = conn.WriteToUDP(respData, remote)
		return
	}

	switch msg.Command {
	case "REGISTER":
		if msg.Task == "" || msg.Address == "" {
			respData, _ := json.Marshal(CentralizedResponse{Status: "ERR", Error: "task and address required"})
			_, _ = conn.WriteToUDP(respData, remote)
			return
		}

		reg.Register(msg.Task, msg.Address)
		if onEvent != nil {
			onEvent(fmt.Sprintf("REGISTER %s -> %s from %v", msg.Task, msg.Address, remote))
		}
		respData, _ := json.Marshal(CentralizedResponse{Status: "OK"})
		_, _ = conn.WriteToUDP(respData, remote)
		return

	case "QUERY":
		if msg.Task == "" {
			respData, _ := json.Marshal(CentralizedResponse{Status: "ERR", Error: "task required"})
			_, _ = conn.WriteToUDP(respData, remote)
			return
		}

		addrStr, err := reg.GetServiceForRequestor(msg.Task, remote.IP)
		if onEvent != nil {
			onEvent(fmt.Sprintf("QUERY %s from %v", msg.Task, remote))
		}

		var resp CentralizedResponse
		switch {
		case err == nil:
			resp = CentralizedResponse{Status: "OK", Address: addrStr}
		case err == registry.ErrNotFound || addrStr == "":
			resp = CentralizedResponse{Status: "NOTFOUND"}
		case err == registry.ErrNoAllowedService:
			resp = CentralizedResponse{Status: "FORBIDDEN"}
		default:
			resp = CentralizedResponse{Status: "ERR", Error: err.Error()}
		}

		respData, _ := json.Marshal(resp)
		_, _ = conn.WriteToUDP(respData, remote)
		return

	default:
		respData, _ := json.Marshal(CentralizedResponse{Status: "ERR", Error: "unknown command: " + msg.Command})
		_, _ = conn.WriteToUDP(respData, remote)
		return
	}
}
