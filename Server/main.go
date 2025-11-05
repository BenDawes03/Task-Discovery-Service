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
	Address       string
	LastHeartbeat time.Time
}

//The mapping of tasks to IP addresses
//TODO: create a process to load this from a database if necessary

var serviceRegistry = make(map[string][]ServiceEntry)
var registryMutex sync.RWMutex
var roundRobinIndex = make(map[string]int)

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
	parts := strings.SplitN(message, ":", 3)
	if len(parts) != 3 {
		fmt.Printf("Registration format error from %s: %s\n", addr, message)
		return
	}

	taskName := parts[1]
	ipAddress := parts[2]

	// Ensure we lock the map for writing
	registryMutex.Lock()
	defer registryMutex.Unlock()

	found := false
	for i := range serviceRegistry[taskName] {
		if serviceRegistry[taskName][i].Address == ipAddress {
			// Found existing entry, just update the heartbeat
			serviceRegistry[taskName][i].LastHeartbeat = time.Now()
			found = true
			break
		}
	}

	if !found {
		// New registration, append to the slice
		newEntry := ServiceEntry{
			Address:       ipAddress,
			LastHeartbeat: time.Now(),
		}
		serviceRegistry[taskName] = append(serviceRegistry[taskName], newEntry)
	}

	// Reset the round-robin index if the slice was previously empty
	if len(serviceRegistry[taskName]) == 1 && roundRobinIndex[taskName] != 0 {
		roundRobinIndex[taskName] = 0
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

	services, found := serviceRegistry[taskName]
	var response string

	if found && len(services) > 0 {
		//Round Robin

		index := roundRobinIndex[taskName]
		if index >= len(services) {
			index = 0
		}
		entry := services[index]

		if time.Since(entry.LastHeartbeat) < HeartbeatTimeout {
			response = entry.Address
			fmt.Printf("Success (RR Index %d): Returning IP %s\n", index, response)

			// modify the roundRobinIndex map safely.
			registryMutex.RUnlock()
			registryMutex.Lock()
			roundRobinIndex[taskName] = (index + 1) % len(services)
			registryMutex.Unlock()
			registryMutex.RLock()

		} else {

			response = "Service inactive (Heartbeat timeout) at selected index"
			fmt.Printf("Inactive: Service found but selected provider timed out.\n")
		}
		// --- End Round-Robin Logic ---

	} else {
		response = "Service not found"
		fmt.Printf("Not Found: Service is not registered or has no providers.\n")
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

		for taskName, entries := range serviceRegistry {

			// Build a new list of active entries
			var activeEntries []ServiceEntry
			for _, entry := range entries {
				if now.Sub(entry.LastHeartbeat) < HeartbeatTimeout {
					activeEntries = append(activeEntries, entry)
				} else {
					removedCount++
				}
			}

			if len(activeEntries) == 0 {
				//Remove task entry
				delete(serviceRegistry, taskName)
				delete(roundRobinIndex, taskName)
			} else {
				// Update t with active services only
				serviceRegistry[taskName] = activeEntries
				// Update Round Robin
				if roundRobinIndex[taskName] >= len(activeEntries) {
					roundRobinIndex[taskName] = 0
				}
			}
		}

		registryMutex.Unlock()

		if removedCount > 0 {
			fmt.Printf("Cleanup Routine: Removed %d inactive services.\n", removedCount)
		}
	}
}
