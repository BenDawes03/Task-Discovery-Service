package main

import (
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	ListenPort       = 5000
	RegisterPrefix   = "REGISTER:"
	RequestPrefix    = "RESOLVE:"
	HeartbeatTimeout = 30 * time.Second
	CleanupInterval  = 10 * time.Second
)

type ServiceEntry struct {
	IP            string
	LastHeartbeat time.Time
}

//The mapping of tasks to IP addresses
//TODO: create a process to load this from a database if necessary

var serviceRegistry = make(map[string]ServiceEntry)
var registryMutex sync.RWMutex

func main() {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", ListenPort))
	if err != nil {
		fmt.Printf("Error resolving address: %v\n", err)
		os.Exit(1)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {

		panic(err)
	}
	defer conn.Close()

	fmt.Println("Server listening on UDP")
	go cleanupInactiveServices()

	// Buffer to hold incoming data (up to 1024 bytes).
	buffer := make([]byte, 1024)
	for {
		n, clientAddr, err := conn.ReadFromUDP(buffer)
		if err != nil {

			fmt.Printf("Error reading from UDP: %v\n", err)
			continue
		}

		go handleRequest(conn, clientAddr, buffer[:n])
	}
}
func handleRequest(conn *net.UDPConn, addr *net.UDPAddr, data []byte) {
	message := strings.TrimSpace(string(data))

	// Check if the message is a REGISTRATION/HEARTBEAT request
	if strings.HasPrefix(message, RegisterPrefix) {
		handleRegistration(conn, addr, message)
	} else {
		// Assume it's a QUERY request
		handleQuery(conn, addr, message)
	}
}

func handleRegistration(conn *net.UDPConn, addr *net.UDPAddr, message string) {
	// Expected format: "REGISTER:<task_name>:<ip_address>"
	parts := strings.Split(message, ":")
	if len(parts) != 3 {
		fmt.Printf("Registration format error from %s: %s\n", addr, message)
		return
	}

	taskName := parts[1]
	ipAddress := parts[2]

	// Ensure we lock the map for writing
	registryMutex.Lock()
	defer registryMutex.Unlock()

	// Update the service entry with the current time
	serviceRegistry[taskName] = ServiceEntry{
		IP:            ipAddress,
		LastHeartbeat: time.Now(),
	}

	fmt.Printf("Registered/Heartbeat: Task=%s, IP=%s\n", taskName, ipAddress)

	// Send a simple confirmation back to the client
	confirmation := fmt.Sprintf("OK:%s", taskName)
	conn.WriteToUDP([]byte(confirmation), addr)
}

func handleQuery(conn *net.UDPConn, addr *net.UDPAddr, taskName string) {
	// Ensure we lock the map for reading
	registryMutex.RLock()
	defer registryMutex.RUnlock()

	fmt.Printf("Received QUERY for task: %s from %s\n", taskName, addr)

	entry, found := serviceRegistry[taskName]

	var response string

	if found {
		// Check if the service is still active based on the heartbeat
		if time.Since(entry.LastHeartbeat) < HeartbeatTimeout {
			response = entry.IP
			fmt.Printf("Success: Returning IP %s\n", response)
		} else {
			response = "Service inactive (Heartbeat timeout)"
			fmt.Printf("Inactive: Service found, but heartbeated out.\n")
		}
	} else {
		response = "Service not found"
		fmt.Printf("Not Found: Service is not registered.\n")
	}

	// Send the response back to the client
	conn.WriteToUDP([]byte(response), addr)
}

// TODO: write task register function
// TODO: Write heartbeat program
// cleanupInactiveServices runs periodically to remove timed-out services.
func cleanupInactiveServices() {
	ticker := time.NewTicker(CleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		registryMutex.Lock()

		removedCount := 0
		now := time.Now()

		for taskName, entry := range serviceRegistry {
			if now.Sub(entry.LastHeartbeat) > HeartbeatTimeout {
				// Service has timed out, delete it
				delete(serviceRegistry, taskName)
				removedCount++
			}
		}

		registryMutex.Unlock()

		if removedCount > 0 {
			fmt.Printf("🧹 Cleanup Routine: Removed %d inactive services.\n", removedCount)
		}
	}
}
