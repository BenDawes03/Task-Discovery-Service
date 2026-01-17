package transport

import (
	"encoding/json"
	"fmt"
	"net"
	"tds/pkg/registry"
)

// StartUDPServer starts a JSON-based UDP server that accepts commands:
// {"cmd": "REGISTER", "task": "taskname", "address": "ip:port"}
// {"cmd": "QUERY", "task": "taskname"}
// Responds with JSON:
// {"status": "OK"} or {"status": "NOTFOUND"} or {"status": "ERR", "error": "..."}
// or {"status": "OK", "address": "ip:port"}
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

		// Handle each request concurrently
		go handleUDPRequest(conn, reg, buf[:n], remote, onEvent)
	}
}

func handleUDPRequest(conn *net.UDPConn, reg registry.Registry, data []byte, remote *net.UDPAddr, onEvent func(string)) {
	// Parse JSON message
	var msg CentralizedMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		resp := CentralizedResponse{
			Status: "ERR",
			Error:  "invalid JSON: " + err.Error(),
		}
		respData, _ := json.Marshal(resp)
		conn.WriteToUDP(respData, remote)
		return
	}

	// Handle command
	switch msg.Command {
	case "REGISTER":
		if msg.Task == "" || msg.Address == "" {
			resp := CentralizedResponse{
				Status: "ERR",
				Error:  "task and address required",
			}
			respData, _ := json.Marshal(resp)
			conn.WriteToUDP(respData, remote)
			return
		}

		reg.Register(msg.Task, msg.Address)
		if onEvent != nil {
			onEvent(fmt.Sprintf("REGISTER %s -> %s from %v", msg.Task, msg.Address, remote))
		}

		resp := CentralizedResponse{Status: "OK"}
		respData, _ := json.Marshal(resp)
		conn.WriteToUDP(respData, remote)

	case "QUERY":
		if msg.Task == "" {
			resp := CentralizedResponse{
				Status: "ERR",
				Error:  "task required",
			}
			respData, _ := json.Marshal(resp)
			conn.WriteToUDP(respData, remote)
			return
		}

		addrStr, err := reg.GetService(msg.Task)
		if onEvent != nil {
			onEvent(fmt.Sprintf("QUERY %s from %v", msg.Task, remote))
		}

		var resp CentralizedResponse
		if err == registry.ErrNotFound || addrStr == "" {
			resp = CentralizedResponse{Status: "NOTFOUND"}
		} else if err != nil {
			resp = CentralizedResponse{
				Status: "ERR",
				Error:  err.Error(),
			}
		} else {
			resp = CentralizedResponse{
				Status:  "OK",
				Address: addrStr,
			}
		}

		respData, _ := json.Marshal(resp)
		conn.WriteToUDP(respData, remote)

		default:
		resp := CentralizedResponse{
			Status: "ERR",
			Error:  "unknown command: " + msg.Command,
		}
		respData, _ := json.Marshal(resp)
		conn.WriteToUDP(respData, remote)
	}
}
