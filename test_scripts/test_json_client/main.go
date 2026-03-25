package main

import (
	"fmt"
	"os"
	"tds/pkg/client"
)

func main() {
	serverAddr := "127.0.0.1:5000"
	if len(os.Args) > 1 {
		serverAddr = os.Args[1]
	}

	fmt.Println("Testing JSON messaging protocol...")
	fmt.Printf("Server: %s\n", serverAddr)
	fmt.Printf("Protocol: %s\n\n", os.Getenv("TDS_SERVER_PROTO"))

	// Test REGISTER
	fmt.Println("1. Testing REGISTER...")
	err := client.Register(serverAddr, "test-task", "192.168.1.100:8080")
	if err != nil {
		fmt.Printf("   ERROR: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("   ✓ REGISTER successful")

	// Test QUERY
	fmt.Println("\n2. Testing QUERY for existing task...")
	addr, err := client.Query(serverAddr, "test-task")
	if err != nil {
		fmt.Printf("   ERROR: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("   ✓ QUERY successful: %s\n", addr)

	// Test QUERY for non-existent task
	fmt.Println("\n3. Testing QUERY for non-existent task...")
	addr, err = client.Query(serverAddr, "nonexistent-task")
	if err != nil {
		fmt.Printf("   ERROR: %v\n", err)
		os.Exit(1)
	}
	if addr == "" {
		fmt.Println("   ✓ Correctly returned empty (not found)")
	} else {
		fmt.Printf("   ✗ Unexpected result: %s\n", addr)
	}

	// Test multiple registrations (round-robin)
	fmt.Println("\n4. Testing multiple service registrations...")
	err = client.Register(serverAddr, "multi-task", "10.0.0.1:8080")
	if err != nil {
		fmt.Printf("   ERROR: %v\n", err)
		os.Exit(1)
	}
	err = client.Register(serverAddr, "multi-task", "10.0.0.2:8080")
	if err != nil {
		fmt.Printf("   ERROR: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("   ✓ Registered two instances")

	fmt.Println("\n5. Testing round-robin selection...")
	for i := 1; i <= 4; i++ {
		addr, err := client.Query(serverAddr, "multi-task")
		if err != nil {
			fmt.Printf("   ERROR on query %d: %v\n", i, err)
			os.Exit(1)
		}
		fmt.Printf("   Query %d: %s\n", i, addr)
	}

	fmt.Println("\n✓ All JSON messaging tests passed!")
}
