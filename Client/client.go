package main

import (
	"fmt"
	"net"
	"strings"
	"time"
)

// --- Configuration ---
const (
	ServerAddress     = "127.0.0.1:5000"
	Timeout           = 5 * time.Second
	HeartbeatInterval = 15 * time.Second // How often the service sends a heartbeat

	// Example service definition for this client instance
	ServiceTask = "web_server"
	ServiceIP   = "192.168.1.50" // A dummy IP for this service
)

func main() {
	// 1. Start the Heartbeat in a background process (Goroutine)
	// This simulates a real service constantly registering itself to stay "active."
	go startHeartbeat()

	fmt.Printf("Client running. Heartbeat for '%s' started in background.\n", ServiceTask)

	// Wait a moment for the initial registration message to be sent
	time.Sleep(1 * time.Second)

	// 2. Perform a test query
	// This simulates a different part of the client application (or a different client)
	// trying to find a service.
	fmt.Println("\n--- Initiating Test Query ---")
	queryService("web_server")

	// Keep the main function alive long enough to see a few heartbeats.
	fmt.Println("\nClient sleeping for 35 seconds to observe heartbeats...")
	time.Sleep(35 * time.Second)

	fmt.Println("Client finished.")
}

// startHeartbeat handles continuous registration and renewal for this service.
func startHeartbeat() {
	// Create a new Ticker that fires every HeartbeatInterval
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop() // Ensure the ticker is stopped when the function exits

	// Loop forever, running the registration logic every time the ticker fires
	for {
		// Send the initial registration immediately, then wait for the ticker
		registerTask(ServiceTask, ServiceIP)
		<-ticker.C // Wait for the next tick
	}
}

// registerTask connects to the server and sends the registration/heartbeat message.
func registerTask(taskName, ipAddress string) {
	serverAddr, err := net.ResolveUDPAddr("udp", ServerAddress)
	if err != nil {
		fmt.Printf("[Heartbeat] Error resolving address: %s\n", err)
		return
	}

	conn, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		fmt.Printf("[Heartbeat] Error dialing server: %s\n", err)
		return
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(Timeout))

	// Format: "REGISTER:<task_name>:<ip_address>"
	registrationMessage := fmt.Sprintf("REGISTER:%s:%s", taskName, ipAddress)

	_, err = conn.Write([]byte(registrationMessage))
	if err != nil {
		fmt.Printf("[Heartbeat] Error sending registration: %s\n", err)
		return
	}

	// Wait for a confirmation response from the server
	buffer := make([]byte, 1024)
	n, _, err := conn.ReadFromUDP(buffer)

	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			fmt.Printf("💙 Heartbeat sent for %s. (No quick server confirmation)\n", taskName)
		} else {
			// This might be an actual error
			fmt.Printf("[Heartbeat] Error receiving confirmation: %s\n", err)
		}
		return
	}

	confirmation := strings.TrimSpace(string(buffer[:n]))
	fmt.Printf("💙 Heartbeat Confirmed: %s\n", confirmation)
}

// queryService connects to the server and queries for a task's IP.
func queryService(taskName string) {
	fmt.Printf("Querying for task: '%s'\n", taskName)

	serverAddr, err := net.ResolveUDPAddr("udp", ServerAddress)
	if err != nil {
		fmt.Printf("Error resolving server address: %s\n", err)
		return
	}

	conn, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		fmt.Printf("Error dialing server: %s\n", err)
		return
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(Timeout))

	// Send the query (just the task name)
	_, err = conn.Write([]byte(taskName))
	if err != nil {
		fmt.Printf("Error sending query: %s\n", err)
		return
	}

	// Receive the response
	buffer := make([]byte, 1024)
	n, _, err := conn.ReadFromUDP(buffer)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			fmt.Printf("Query failed: Server response timed out after %s\n", Timeout)
		} else {
			fmt.Printf("Error receiving response: %s\n", err)
		}
		return
	}

	response := strings.TrimSpace(string(buffer[:n]))
	fmt.Println("------------------------------------------")
	fmt.Printf("QUERY RESULT for %s: %s\n", taskName, response)
	fmt.Println("------------------------------------------")
}
