package transport

import (
	"fmt"
	"net"
	"strings"

	"tds/pkg/registry"
)

// CentralizedMessage represents a JSON message for centralized mode
type CentralizedMessage struct {
	Command  string `json:"cmd"`                // "REGISTER" or "QUERY"
	Task     string `json:"task"`               // Task name
	Address  string `json:"address,omitempty"`  // Service address (for REGISTER)
	Capacity int    `json:"capacity,omitempty"` // Optional service capacity (for REGISTER)
}

// CentralizedResponse represents a JSON response from the server
type CentralizedResponse struct {
	Status    string   `json:"status"`              // "OK", "NOTFOUND", "ERR"
	Address   string   `json:"address,omitempty"`   // Service address (for QUERY success)
	Error     string   `json:"error,omitempty"`     // Error message (for ERR)
	Addresses []string `json:"addresses,omitempty"` // Multiple addresses (future extensibility)
}

type capacityAwareRegistry interface {
	RegisterWithCapacity(taskName, address string, capacity int)
}

// HandleMessage processes a CentralizedMessage and returns a CentralizedResponse.
// It serves as the shared protocol handler for both UDP and TCP servers.
func HandleMessage(reg registry.Registry, msg CentralizedMessage, requestorIP net.IP, remoteAddr net.Addr, onEvent func(string)) CentralizedResponse {
	switch msg.Command {
	case "REGISTER":
		return handleRegister(reg, msg, remoteAddr, onEvent)

	case "QUERY":
		return handleQuery(reg, msg, requestorIP, remoteAddr, onEvent)

	default:
		return CentralizedResponse{Status: "ERR", Error: "unknown command: " + msg.Command}
	}
}

func handleRegister(reg registry.Registry, msg CentralizedMessage, remoteAddr net.Addr, onEvent func(string)) CentralizedResponse {
	msg.Task = strings.TrimSpace(msg.Task)
	msg.Address = strings.TrimSpace(msg.Address)
	if msg.Task == "" || msg.Address == "" {
		return CentralizedResponse{Status: "ERR", Error: "task and address required"}
	}

	capacity := msg.Capacity
	if capacity <= 0 {
		capacity = 1
	}

	if capReg, ok := reg.(capacityAwareRegistry); ok {
		capReg.RegisterWithCapacity(msg.Task, msg.Address, capacity)
	} else {
		reg.Register(msg.Task, msg.Address)
	}

	if onEvent != nil {
		onEvent(fmt.Sprintf("REGISTER %s -> %s cap=%d from %v", msg.Task, msg.Address, capacity, remoteAddr))
	}

	return CentralizedResponse{Status: "OK"}
}

func handleQuery(reg registry.Registry, msg CentralizedMessage, requestorIP net.IP, remoteAddr net.Addr, onEvent func(string)) CentralizedResponse {
	msg.Task = strings.TrimSpace(msg.Task)
	if msg.Task == "" {
		return CentralizedResponse{Status: "ERR", Error: "task required"}
	}

	addrStr, err := reg.GetServiceForRequestor(msg.Task, requestorIP)
	if onEvent != nil {
		onEvent(fmt.Sprintf("QUERY %s from %v", msg.Task, remoteAddr))
	}

	switch {
	case err == nil:
		return CentralizedResponse{Status: "OK", Address: addrStr}
	case err == registry.ErrNotFound || addrStr == "":
		return CentralizedResponse{Status: "NOTFOUND"}
	case err == registry.ErrNoAllowedService:
		return CentralizedResponse{Status: "FORBIDDEN"}
	default:
		return CentralizedResponse{Status: "ERR", Error: err.Error()}
	}
}
