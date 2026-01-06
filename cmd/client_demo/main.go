package main

import (
	"bufio"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

func main() {
	// Command-line flags
	serverAddr := flag.String("server", "", "Server address (e.g., localhost:5100 for proxy)")
	mode := flag.String("mode", "", "Mode: 'centralized' or 'p2p'")
	flag.Parse()

	// If no flags provided, ask interactively
	if *serverAddr == "" || *mode == "" {
		selectedMode, selectedAddr := askModeOptions()
		if *mode == "" {
			*mode = selectedMode
		}
		if *serverAddr == "" {
			*serverAddr = selectedAddr
		}
	}

	fmt.Printf("\n[client_demo] Starting in %s mode\n", *mode)
	fmt.Printf("[client_demo] Connecting to: %s\n\n", *serverAddr)

	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print("> ")
		input, err := reader.ReadString('\n')
		if err != nil {
			fmt.Printf("Error reading input: %v\n", err)
			continue
		}

		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}

		if input == "quit" || input == "exit" {
			fmt.Println("Exiting demo client")
			return
		}

		if input == "help" {
			printHelp(*mode)
			continue
		}

		// Send the command to the server
		response, err := sendCommand(*serverAddr, input)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}

		fmt.Printf("Response: %s\n", response)
	}
}

func askModeOptions() (string, string) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("\nSelect mode:")
	fmt.Println("1. Centralized (default)")
	fmt.Println("2. P2P")
	fmt.Print("Enter choice [1]: ")

	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(choice)

	if choice == "" {
		choice = "1"
	}

	var mode string
	var defaultAddr string

	switch choice {
	case "2":
		mode = "p2p"
		defaultAddr = "localhost:5100"
		fmt.Printf("\nP2P mode selected\n")
		fmt.Printf("Connect to a client_proxy running in P2P mode\n")
		fmt.Printf("Enter proxy address (e.g., localhost:5100) [default: %s]: ", defaultAddr)
	default:
		mode = "centralized"
		defaultAddr = "localhost:5100"
		fmt.Printf("\nCentralized mode selected\n")
		fmt.Printf("Connect to a client_proxy in centralized mode\n")
		fmt.Printf("Enter proxy address (e.g., localhost:5100) [default: %s]: ", defaultAddr)
	}

	addr, _ := reader.ReadString('\n')
	addr = strings.TrimSpace(addr)
	if addr == "" {
		addr = defaultAddr
	}

	return mode, addr
}

func sendCommand(serverAddr, command string) (string, error) {
	// Resolve the address
	udpAddr, err := net.ResolveUDPAddr("udp", serverAddr)
	if err != nil {
		return "", fmt.Errorf("failed to resolve address: %w", err)
	}

	// Create UDP connection
	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return "", fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close()

	// Set timeout
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Send command
	_, err = conn.Write([]byte(command))
	if err != nil {
		return "", fmt.Errorf("failed to send: %w", err)
	}

	// Read response
	buffer := make([]byte, 1024)
	n, err := conn.Read(buffer)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	return string(buffer[:n]), nil
}

func printHelp(mode string) {
	fmt.Println("\nAvailable commands:")
	fmt.Println("  REGISTER <task> <address>  - Register a service")
	fmt.Println("  QUERY <task>               - Query a service")
	fmt.Println("  help                       - Show this help")
	fmt.Println("  quit / exit                - Exit the demo")
	fmt.Println("\nExamples:")
	if mode == "p2p" {
		fmt.Println("  REGISTER web-api 192.168.1.50:8080")
		fmt.Println("  QUERY web-api")
		fmt.Println("\nNote: In P2P mode, registrations propagate across nodes (~1-2s)")
		fmt.Println("      You can query from any node in the network")
	} else {
		fmt.Println("  REGISTER my-service 10.0.0.1:9000")
		fmt.Println("  QUERY my-service")
		fmt.Println("\nNote: In centralized mode, all queries go through the central server")
	}
}
