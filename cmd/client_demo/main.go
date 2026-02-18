package main

import (
	"bufio"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"tds/pkg/client"
)

func main() {
	// Command-line flags
	serverAddr := flag.String("server", "", "Server address (centralized) or proxy address (p2p). Examples: 127.0.0.1:5000 or localhost:5100")
	mode := flag.String("mode", "", "Mode: 'centralized' or 'p2p'")
	proto := flag.String("proto", "udp", "Centralized mode transport: 'udp' or 'tcp' (ignored when -tls is set)")

	// TLS flags (centralized mode only)
	useTLS := flag.Bool("tls", false, "Centralized mode: Use TLS with mutual authentication (TCP)")
	certFile := flag.String("cert", "certs/client.crt", "Client TLS certificate file")
	keyFile := flag.String("key", "certs/client.key", "Client TLS private key file")
	caFile := flag.String("ca", "certs/ca.crt", "CA certificate to verify server")
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

	*mode = strings.ToLower(strings.TrimSpace(*mode))
	*proto = strings.ToLower(strings.TrimSpace(*proto))

	switch *mode {
	case "centralized", "p2p":
		// ok
	default:
		fmt.Printf("Invalid -mode %q (expected 'centralized' or 'p2p')\n", *mode)
		os.Exit(2)
	}

	if *mode == "p2p" && *useTLS {
		fmt.Println("-tls is only supported in centralized mode")
		os.Exit(2)
	}

	if *mode == "centralized" && !*useTLS {
		if *proto != "udp" && *proto != "tcp" {
			fmt.Printf("Invalid -proto %q (expected 'udp' or 'tcp')\n", *proto)
			os.Exit(2)
		}
	}

	if *mode == "centralized" {
		if *useTLS {
			fmt.Printf("\n[client_demo] Centralized mode (TLS)\n")
			fmt.Printf("[client_demo] Server: %s\n\n", *serverAddr)
		} else {
			fmt.Printf("\n[client_demo] Centralized mode (%s)\n", *proto)
			fmt.Printf("[client_demo] Server: %s\n\n", *serverAddr)
		}
	} else {
		fmt.Printf("\n[client_demo] P2P mode\n")
		fmt.Printf("[client_demo] Proxy: %s\n\n", *serverAddr)
	}

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

		lower := strings.ToLower(input)
		if lower == "quit" || lower == "exit" {
			fmt.Println("Exiting demo client")
			return
		}
		if lower == "help" {
			printHelp(*mode)
			continue
		}

		response, err := runCommand(*mode, *serverAddr, *proto, *useTLS, *certFile, *keyFile, *caFile, input)
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

func runCommand(mode, serverAddr, proto string, useTLS bool, certFile, keyFile, caFile, input string) (string, error) {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return "ERR empty command", nil
	}
	cmd := strings.ToUpper(parts[0])

	switch mode {
	case "p2p":
		// In P2P mode, we talk to the local proxy over UDP using the simple text protocol.
		return sendCommand(serverAddr, input)

	case "centralized":
		// In centralized mode, we talk directly to the server using the JSON protocol.
		switch cmd {
		case "REGISTER":
			if len(parts) < 3 {
				return "ERR usage: REGISTER <task> <address>", nil
			}
			task := parts[1]
			addr := parts[2]
			var err error
			if useTLS {
				err = client.RegisterTLS(serverAddr, task, addr, certFile, keyFile, caFile)
			} else if proto == "tcp" {
				err = client.RegisterTCP(serverAddr, task, addr)
			} else {
				err = client.RegisterUDP(serverAddr, task, addr)
			}
			if err != nil {
				return "ERR " + err.Error(), nil
			}
			return "OK", nil

		case "QUERY":
			if len(parts) < 2 {
				return "ERR usage: QUERY <task>", nil
			}
			task := parts[1]
			var (
				addr string
				err  error
			)
			if useTLS {
				addr, err = client.QueryTLS(serverAddr, task, certFile, keyFile, caFile)
			} else if proto == "tcp" {
				addr, err = client.QueryTCP(serverAddr, task)
			} else {
				addr, err = client.QueryUDP(serverAddr, task)
			}
			if err != nil {
				return "ERR " + err.Error(), nil
			}
			if addr == "" {
				return "NOTFOUND", nil
			}
			return addr, nil

		default:
			return "ERR unknown command", nil
		}
	}

	return "ERR invalid mode", nil
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
