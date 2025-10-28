package main

import (
	"fmt"
	"net"
	"strings"
)

//The mapping of tasks to IP addresses
//TODO: create a process to load this from a database if necessary

var ServiceRegistry = map[string][]string{
	//Default values for testing
	"file_storage":     {"192.168.1.10", "192.168.1.11"},
	"database_query":   {"192.168.1.20"},
	"image_processing": {"192.168.1.30", "192.168.1.31", "192.168.1.32"},
}

func main() {
	//use service port 5000
	conn, err := net.ListenPacket("udp", ":5000")
	if err != nil {

		panic(err)
	}
	defer conn.Close()

	fmt.Println("Server listening on :5000 (UDP)")

	// Buffer to hold incoming data (up to 1024 bytes).
	buffer := make([]byte, 1024)
	for {
		// Read the incoming packet.
		n, addr, err := conn.ReadFrom(buffer)
		if err != nil {
			fmt.Println("Error:", err)
			continue
		}

		// Handle the client request concurrently using a goroutine.
		go handleRequest(conn, addr, buffer[:n])
	}
}

func handleRequest(conn net.PacketConn, addr net.Addr, data []byte) {
	// The incoming data is the task name (e.g., "file_storage").
	task := strings.TrimSpace(string(data))

	fmt.Printf("Received query for '%s' from %s\n", task, addr.String())

	// Lookup the task in the registry.
	ips, found := ServiceRegistry[task]
	var response string

	if found && len(ips) > 0 {
		// Currently return first entry
		///TODO implement Round Robin
		ipToReturn := ips[0]
		response = ipToReturn
		fmt.Printf("Responding with IP: %s\n", ipToReturn)
	} else {
		response = "Task not found."
		fmt.Printf("Responding: Task not found.\n")
	}

	// Respond to client.
	_, err := conn.WriteTo([]byte(response), addr)
	if err != nil {
		fmt.Println("Error sending response:", err)
	}
}

//TODO: write task register function
//TODO: Write heartbeat program
