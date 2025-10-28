package main

import (
	"fmt"
	"net"
	"os"
	"time"
)

// --- Config ---
const (
	ServerAddress = "127.0.0.1:5000"
	QueryTask     = "file_storage" // The task to query
	Timeout       = 5 * time.Second
)

func main() {
	fmt.Printf("Client attempting to connect to: %s\n", ServerAddress)

	// Resolve UDP Address

	serverAddr, err := net.ResolveUDPAddr("udp", ServerAddress)
	if err != nil {
		fmt.Printf("Error resolving server address: %s\n", err)
		os.Exit(1)
	}

	//Dial UDP
	conn, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		fmt.Printf("Error dialing server: %s\n", err)
		os.Exit(1)
	}
	defer conn.Close() // Ensure the connection is closed when main() exits

	conn.SetDeadline(time.Now().Add(Timeout))

	// 3. Send the query
	fmt.Printf("Sending query for task: '%s'\n", QueryTask)
	query := []byte(QueryTask)

	_, err = conn.Write(query)
	if err != nil {
		fmt.Printf("Error sending query: %s\n", err)
		os.Exit(1)
	}

	// 4. Receive the response
	buffer := make([]byte, 1024)
	n, _, err := conn.ReadFromUDP(buffer) //  get data from the connection
	if err != nil {
		// Check for timeout error
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			fmt.Printf("Error: Server response timed out after %s\n", Timeout)
		} else {
			fmt.Printf("Error receiving response: %s\n", err)
		}
		os.Exit(1)
	}

	// 5. Process and display the response
	ipAddress := string(buffer[:n])
	fmt.Println("------------------------------------------")
	fmt.Printf("Server Response: %s\n", ipAddress)
	fmt.Println("------------------------------------------")
}
