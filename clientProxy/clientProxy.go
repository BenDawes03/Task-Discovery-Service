package main

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// --- Configuration ---
const (
	// Port service listens on
	LocalListenPort = 8054
	// address of task server
	DiscoveryServerAddress = "127.0.0.1:5000"
	// Timeout for communication with the Discovery Server.
	DiscoveryTimeout = 5 * time.Second
	// The task this proxy registers itself as
	ServiceTask       = "client_proxy"
	ServiceIP         = "127.0.0.1" // The IP for this proxy
	HeartbeatInterval = 15 * time.Second
)

func main() {
	// Start the background heartbeat for this proxy service itself (if needed)
	go startHeartbeat()

	// 1. Start the TCP Listener
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", LocalListenPort))
	if err != nil {
		fmt.Printf("Error starting TCP listener: %v\n", err)
		os.Exit(1)
	}
	defer listener.Close()

	fmt.Printf("Service Lookup Proxy running (TCP) on port: %d\n", LocalListenPort)
	fmt.Printf("Forwarding queries to Discovery Server (UDP) at: %s\n", DiscoveryServerAddress)

	// 2. Accept incoming TCP connections
	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Printf("Error accepting connection: %v\n", err)
			continue
		}
		// Handle each incoming connection in a new goroutine
		go handleIncomingServiceRequest(conn)
	}
}

// handleIncomingServiceRequest reads the task name and performs the lookup.
func handleIncomingServiceRequest(conn net.Conn) {
	defer conn.Close()

	// Use a 1024-byte buffer for incoming TCP request (the task name)
	buffer := make([]byte, 1024)
	n, err := conn.Read(buffer)
	if err != nil {
		fmt.Printf("Error reading from TCP connection: %v\n", err)
		return
	}

	// The incoming TCP message is the task name requested by a local service
	taskName := strings.TrimSpace(string(buffer[:n]))
	fmt.Printf("  Received TCP request for task: '%s'\n", taskName)

	// 1. Query the Discovery Server over UDP
	responseIP, lookupErr := queryDiscoveryServer(taskName)

	// 2. Send the result back over the TCP connection
	if lookupErr != nil {
		fmt.Printf("   Lookup failed for %s: %v\n", taskName, lookupErr)
		// Send the error message back to the requesting service
		conn.Write([]byte(fmt.Sprintf("ERROR: %v", lookupErr)))
	} else {
		fmt.Printf("  Successfully resolved IP: %s\n", responseIP)
		// Send the discovered IP back to the requesting service
		conn.Write([]byte(responseIP))
	}
}

// queryDiscoveryServer handles the actual UDP communication with the Discovery Server.
func queryDiscoveryServer(taskName string) (string, error) {
	serverAddr, err := net.ResolveUDPAddr("udp", DiscoveryServerAddress)
	if err != nil {
		return "", fmt.Errorf("error resolving address: %v", err)
	}

	conn, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		return "", fmt.Errorf("error dialing server: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(DiscoveryTimeout))

	// Send the query (just the task name)
	_, err = conn.Write([]byte(taskName))
	if err != nil {
		return "", fmt.Errorf("error sending query: %v", err)
	}

	// Receive the response
	buffer := make([]byte, 1024)
	n, _, err := conn.ReadFromUDP(buffer)
	if err != nil {
		return "", fmt.Errorf("error receiving response (timeout or network issue): %v", err)
	}

	response := strings.TrimSpace(string(buffer[:n]))

	// Check for a specific error response from the server
	if strings.HasPrefix(response, "Service inactive") || strings.HasPrefix(response, "Service not found") {
		return "", fmt.Errorf(response)
	}

	return response, nil
}

// --- Heartbeat Logic (Kept from previous iteration) ---

func startHeartbeat() {
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()
	for {
		registerTask(ServiceTask, ServiceIP)
		<-ticker.C
	}
}

func registerTask(taskName, ipAddress string) {
	serverAddr, err := net.ResolveUDPAddr("udp", DiscoveryServerAddress)
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

	conn.SetDeadline(time.Now().Add(DiscoveryTimeout))

	registrationMessage := fmt.Sprintf("REGISTER:%s:%s", taskName, ipAddress)
	_, err = conn.Write([]byte(registrationMessage))

	if err != nil {
		fmt.Printf("[Heartbeat] Error sending registration: %s\n", err)
		return
	}

	// Optional: Wait for confirmation to avoid flooding logs
	// We'll skip detailed confirmation logging here as the main job is proxying.
}
