package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"tds/pkg/client"
	"tds/pkg/dht"
)

// askModeOptions collects interactive options from the terminal.
// Returns: mode ("centralized"|"p2p"), transport ("udp"|"tcp"), p2pPort, bootstrapNodes
func askModeOptions() (string, string, string, []string) {
	mode := "centralized"
	transport := "udp"
	p2pPort := ":6000"
	var bootstrapNodes []string

	// Parse command-line flags for non-interactive mode
	p2pFlag := flag.Bool("p2p", false, "Enable peer-to-peer mode using DHT")
	p2pPortFlag := flag.String("p2p-port", ":6000", "Port for P2P DHT communication")
	bootstrapFlag := flag.String("bootstrap", "", "Comma-separated list of bootstrap nodes")
	tcpFlag := flag.Bool("tcp", false, "Use TCP transport (centralized mode)")
	flag.Parse()

	// Check if flags were provided (non-interactive)
	if *p2pFlag {
		mode = "p2p"
		p2pPort = *p2pPortFlag
		if *bootstrapFlag != "" {
			bootstrapNodes = strings.Split(*bootstrapFlag, ",")
			for i := range bootstrapNodes {
				bootstrapNodes[i] = strings.TrimSpace(bootstrapNodes[i])
			}
		}
		return mode, transport, p2pPort, bootstrapNodes
	}

	if *tcpFlag {
		transport = "tcp"
	}

	// If not in interactive terminal, return defaults
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintln(os.Stderr, "No interactive terminal detected; defaulting to centralized mode with UDP")
		return mode, transport, p2pPort, bootstrapNodes
	}

	// Interactive prompts
	reader := bufio.NewReader(os.Stdin)

	// Ask for mode
	fmt.Fprint(os.Stderr, "Select mode: 1) centralized (default) 2) p2p. Enter 1 or 2 [1]: ")
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	switch strings.ToLower(input) {
	case "", "1", "centralized":
		mode = "centralized"
	case "2", "p2p":
		mode = "p2p"
	default:
		fmt.Fprintln(os.Stderr, "Unrecognized input; defaulting to centralized mode")
		mode = "centralized"
	}

	if mode == "p2p" {
		// Ask for P2P port
		fmt.Fprint(os.Stderr, "Enter P2P port [:6000]: ")
		portInput, _ := reader.ReadString('\n')
		portInput = strings.TrimSpace(portInput)
		if portInput != "" {
			p2pPort = portInput
		}

		// Ask for bootstrap nodes
		fmt.Fprint(os.Stderr, "Enter bootstrap nodes (comma-separated, or leave empty): ")
		bootstrapInput, _ := reader.ReadString('\n')
		bootstrapInput = strings.TrimSpace(bootstrapInput)
		if bootstrapInput != "" {
			bootstrapNodes = strings.Split(bootstrapInput, ",")
			for i := range bootstrapNodes {
				bootstrapNodes[i] = strings.TrimSpace(bootstrapNodes[i])
			}
		}
	} else {
		// Centralized mode - ask for transport
		fmt.Fprint(os.Stderr, "Select transport mode: 1) udp (default) 2) tcp. Enter 1 or 2 [1]: ")
		transportInput, _ := reader.ReadString('\n')
		transportInput = strings.TrimSpace(transportInput)
		switch strings.ToLower(transportInput) {
		case "", "1", "udp":
			transport = "udp"
		case "2", "tcp":
			transport = "tcp"
		default:
			fmt.Fprintln(os.Stderr, "Unrecognized input; defaulting to UDP transport")
			transport = "udp"
		}
	}

	return mode, transport, p2pPort, bootstrapNodes
}

func main() {
	// Gather mode and transport options
	mode, transport, p2pPort, bootstrapNodes := askModeOptions()

	listen := os.Getenv("TDS_PROXY_LISTEN")
	if listen == "" {
		listen = ":5100"
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	if mode == "p2p" {
		// P2P mode: use DHT
		fmt.Println("Starting in P2P mode with DHT...")

		if len(bootstrapNodes) > 0 {
			fmt.Printf("Bootstrap nodes: %v\n", bootstrapNodes)
		}

		// create DHT registry
		dhtRegistry, err := dht.NewDHTRegistry(p2pPort, bootstrapNodes)
		if err != nil {
			log.Fatalf("failed to create DHT registry: %v", err)
		}

		if err := dhtRegistry.Start(); err != nil {
			log.Fatalf("failed to start DHT: %v", err)
		}

		// run proxy with DHT backend
		go func() {
			done <- client.RunProxyP2P(ctx, listen, dhtRegistry)
		}()

		fmt.Printf("DHT node listening on %s\n", p2pPort)
		fmt.Printf("Client proxy listening on %s\n", listen)
	} else {
		// Traditional centralized mode
		fmt.Printf("Starting in centralized mode with %s transport...\n", transport)

		go func() {
			// run proxy and report any error
			if transport == "tcp" {
				done <- client.RunProxyTCP(ctx, listen)
			} else {
				done <- client.RunProxy(ctx, listen)
			}
		}()

		fmt.Printf("Client proxy listening on %s\n", listen)
	}

	// simple interactive loop
	if mode == "p2p" {
		fmt.Println("client-proxy running in P2P mode. type 'help' for commands.")
	} else {
		fmt.Println("client-proxy running in centralized mode. type 'help' for commands.")
	}
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			// EOF
			break
		}
		line := strings.TrimSpace(scanner.Text())
		switch strings.ToLower(line) {
		case "", "help":
			if mode == "p2p" {
				fmt.Println("commands: help, stats, dht, quit")
			} else {
				fmt.Println("commands: help, stats, quit")
			}
		case "stats":
			regs, queries, errs := client.Stats()
			fmt.Printf("stats: registers=%d queries=%d errors=%d\n", regs, queries, errs)
		case "dht":
			if mode == "p2p" {
				// show DHT info
				fmt.Println("DHT information available via client package")
			} else {
				fmt.Println("DHT mode not enabled")
			}
		case "quit", "exit", "q":
			cancel()
			select {
			case err := <-done:
				if err != nil {
					log.Printf("proxy stopped with error: %v", err)
				}
			case <-time.After(2 * time.Second):
				log.Println("proxy shutdown timed out")
			}
			fmt.Println("exiting")
			return
		default:
			fmt.Println("unknown command; type 'help'")
		}
	}
	// stdin closed — shutdown
	cancel()
	<-done
}
