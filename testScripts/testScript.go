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
	// Address of the Service Lookup Proxy (Your client_proxy.go application)
	ProxyAddress = "127.0.0.1:8054"
	Timeout      = 5 * time.Second
)

func main() {
	if len(os.Args) != 2 {
		fmt.Printf("Usage: go run test_client.go <task_name>\n")
		fmt.Printf("Example: go run test_client.go file_storage\n")
		os.Exit(1)
	}

	taskName := os.Args[1]
	fmt.Printf("Attempting to connect to proxy %s to query for '%s'...\n", ProxyAddress, taskName)

	// 1. Establish TCP connection to the Proxy
	conn, err := net.DialTimeout("tcp", ProxyAddress, Timeout)
	if err != nil {
		fmt.Printf("Error connecting to proxy: %v\n", err)
		// Suggest running the proxy if connection fails
		fmt.Println("   Ensure your 'client_proxy.go' is running on port 8054.")
		os.Exit(1)
	}
	defer conn.Close() // Ensure the connection is closed when we finish

	// Set a deadline for the entire exchange
	conn.SetDeadline(time.Now().Add(Timeout))

	// 2. Send the task name request over the TCP stream
	_, err = conn.Write([]byte(taskName))
	if err != nil {
		fmt.Printf("Error sending request: %v\n", err)
		return
	}

	// 3. Receive the response (IP address or error message)
	buffer := make([]byte, 1024)
	n, err := conn.Read(buffer)
	if err != nil {
		fmt.Printf("Error reading response from proxy: %v\n", err)
		return
	}

	response := strings.TrimSpace(string(buffer[:n]))

	// 4. Output the result
	fmt.Println("\n--- Lookup Result ---")
	if strings.HasPrefix(response, "ERROR:") || strings.Contains(response, "Service") {
		// Output server-level errors (e.g., Service not found)
		fmt.Printf("Lookup Failed: %s\n", response)
	} else {
		// Output successful IP address
		fmt.Printf("SUCCESS! Target IP for '%s': %s\n", taskName, response)
	}
	fmt.Println("---------------------\n")
}
