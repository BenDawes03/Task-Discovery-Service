package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

	"tds/pkg/client"
	"tds/pkg/dht"
)

var (
	stdinIsTerminalFn  = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
	stdoutIsTerminalFn = func() bool { return term.IsTerminal(int(os.Stdout.Fd())) }
	modePromptReaderFn = func() *bufio.Reader { return bufio.NewReader(os.Stdin) }

	runProxyFn    = client.RunProxy
	runProxyTCPFn = client.RunProxyTCP
	runProxyP2PFn = client.RunProxyP2P
)

// askModeOptions collects interactive options from the terminal.
// Returns: mode ("centralized"|"p2p"), transport ("udp"|"tcp"), p2pPort, bootstrapNodes, background, kClosest, simplifiedUI, p2pHeartbeatTimeout
func askModeOptions() (string, string, string, []string, bool, int, bool, time.Duration) {
	mode := "centralized"
	transport := "udp"
	p2pPort := ":6000"
	kClosest := dht.ReplicationFactor
	simplifiedUI := false
	p2pHeartbeatTimeout := dht.ServiceHeartbeatTimeout
	var bootstrapNodes []string
	background := false

	// Parse command-line flags for non-interactive mode
	p2pFlag := flag.Bool("p2p", false, "Enable peer-to-peer mode using DHT")
	p2pPortFlag := flag.String("p2p-port", "6000", "Listen address or port for P2P DHT communication (e.g. '6000')")
	bootstrapFlag := flag.String("bootstrap", "", "Comma-separated list of bootstrap nodes in host:port form (e.g. '127.0.0.1:6000,127.0.0.1:6002')")
	kClosestFlag := flag.Int("k-closest", dht.ReplicationFactor, "Number of k-closest nodes used by DHT replication/query in p2p mode")
	p2pHeartbeatTimeoutFlag := flag.Duration("p2p-heartbeat-timeout", dht.ServiceHeartbeatTimeout, "Timeout for P2P service heartbeats before cleanup")
	simpleUIFlag := flag.Bool("simple-ui", false, "Use a simplified P2P dashboard focused on DHT activity")
	tcpFlag := flag.Bool("tcp", false, "Use TCP transport (centralized mode)")
	backgroundFlag := flag.Bool("background", false, "Run in background (no interactive stdin); exit on SIGINT/SIGTERM or proxy error")
	flag.Parse()
	background = *backgroundFlag
	simplifiedUI = *simpleUIFlag
	if *kClosestFlag > 0 {
		kClosest = *kClosestFlag
	} else {
		fmt.Fprintf(os.Stderr, "Invalid -k-closest=%d; using default %d\n", *kClosestFlag, dht.ReplicationFactor)
	}
	if *p2pHeartbeatTimeoutFlag > 0 {
		p2pHeartbeatTimeout = *p2pHeartbeatTimeoutFlag
	} else {
		fmt.Fprintf(os.Stderr, "Invalid -p2p-heartbeat-timeout=%s; using default %s\n", *p2pHeartbeatTimeoutFlag, dht.ServiceHeartbeatTimeout)
	}

	// Check if flags were provided (non-interactive)
	if *p2pFlag {
		mode = "p2p"
		p2pPort = normalizePortInput(*p2pPortFlag)
		if *bootstrapFlag != "" {
			bootstrapNodes = strings.Split(*bootstrapFlag, ",")
			for i := range bootstrapNodes {
				bootstrapNodes[i] = strings.TrimSpace(bootstrapNodes[i])
			}
		}
		return mode, transport, p2pPort, bootstrapNodes, background, kClosest, simplifiedUI, p2pHeartbeatTimeout
	}

	if *tcpFlag {
		transport = "tcp"
	}

	// If not in interactive terminal, return defaults
	if !stdinIsTerminalFn() {
		if !background {
			fmt.Fprintln(os.Stderr, "No interactive terminal detected; defaulting to centralized mode with UDP")
		}
		return mode, transport, p2pPort, bootstrapNodes, background, kClosest, simplifiedUI, p2pHeartbeatTimeout
	}

	// In background mode, skip interactive prompts (use defaults unless flags are provided).
	if background {
		return mode, transport, p2pPort, bootstrapNodes, background, kClosest, simplifiedUI, p2pHeartbeatTimeout
	}

	// Interactive prompts
	reader := modePromptReaderFn()

	// Ask for mode
	fmt.Fprintln(os.Stderr, "Select mode:")
	fmt.Fprintln(os.Stderr, "  1) centralized (default)")
	fmt.Fprintln(os.Stderr, "  2) p2p (DHT)")
	fmt.Fprint(os.Stderr, "Enter choice [1]: ")
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
		fmt.Fprint(os.Stderr, "Enter P2P listen address/port (example: '6000') [6000]: ")
		portInput, _ := reader.ReadString('\n')
		portInput = strings.TrimSpace(portInput)
		if portInput != "" {
			p2pPort = normalizePortInput(portInput)
		}

		// Ask for bootstrap nodes
		bootstrapNodes = askBootstrapNodes(reader)

		// Ask for dashboard style
		fmt.Fprint(os.Stderr, "Use simplified dashboard UI focused on DHT events? [Y/n]: ")
		simpleInput, _ := reader.ReadString('\n')
		simpleInput = strings.ToLower(strings.TrimSpace(simpleInput))
		simplifiedUI = (simpleInput == "" || simpleInput == "y" || simpleInput == "yes")
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

	return mode, transport, p2pPort, bootstrapNodes, background, kClosest, simplifiedUI, p2pHeartbeatTimeout
}

func askBootstrapNodes(reader *bufio.Reader) []string {
	for {
		fmt.Fprintln(os.Stderr, "Enter bootstrap nodes (comma-separated host:port, empty for none)")
		fmt.Fprintln(os.Stderr, "  Example: 127.0.0.1:6000")
		fmt.Fprint(os.Stderr, "Bootstrap nodes: ")
		bootstrapInput, _ := reader.ReadString('\n')
		bootstrapInput = strings.TrimSpace(bootstrapInput)
		if bootstrapInput == "" {
			return nil
		}

		nodes := strings.Split(bootstrapInput, ",")
		var out []string
		var bad []string
		for _, n := range nodes {
			n = strings.TrimSpace(n)
			if n == "" {
				continue
			}
			host, port, err := net.SplitHostPort(n)
			if err != nil || host == "" || port == "" {
				bad = append(bad, n)
				continue
			}
			out = append(out, n)
		}
		if len(bad) == 0 && len(out) > 0 {
			return out
		}
		if len(out) == 0 {
			fmt.Fprintln(os.Stderr, "No valid bootstrap nodes provided.")
		} else {
			fmt.Fprintf(os.Stderr, "Invalid bootstrap node(s): %s\n", strings.Join(bad, ", "))
		}
		fmt.Fprintln(os.Stderr, "Format must be host:port (for IPv6, use [addr]:port). Try again or press Enter for none.")
	}
}

func normalizePortInput(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return input
	}
	// If user provided only digits, treat it as a port and prepend ':' so net.Listen works.
	allDigits := true
	for _, r := range input {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return ":" + input
	}
	return input
}

func main() {
	// Gather mode and transport options
	mode, transport, p2pPort, bootstrapNodes, background, kClosest, simplifiedUI, p2pHeartbeatTimeout := askModeOptions()

	if background {
		// Quiet background operation: no logs, no prints, no interactive prompts.
		log.SetOutput(io.Discard)
		client.SetQuiet()
		dht.SetQuiet()
	}

	listen := os.Getenv("TDS_PROXY_LISTEN")
	if listen == "" {
		listen = ":5100"
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	proxyExited := make(chan struct{})
	var proxyErr error
	var proxyErrMu sync.Mutex
	setProxyErr := func(err error) {
		proxyErrMu.Lock()
		defer proxyErrMu.Unlock()
		proxyErr = err
	}
	getProxyErr := func() error {
		proxyErrMu.Lock()
		defer proxyErrMu.Unlock()
		return proxyErr
	}

	var dhtRegistry *dht.DHTRegistry
	var shutdownOnce sync.Once
	shutdown := func() {
		shutdownOnce.Do(func() {
			cancel()
			select {
			case <-proxyExited:
				if err := getProxyErr(); err != nil {
					log.Printf("proxy stopped with error: %v", err)
				}
			case <-time.After(2 * time.Second):
				log.Println("proxy shutdown timed out")
			}
			if dhtRegistry != nil {
				_ = dhtRegistry.Stop()
			}
		})
	}

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	signalForwardStop := make(chan struct{})
	go func() {
		select {
		case sig := <-sigCh:
			if !background {
				fmt.Fprintf(os.Stderr, "\nreceived %s, shutting down...\n", sig.String())
			}
			shutdown()
		case <-signalForwardStop:
		}
	}()
	defer close(signalForwardStop)

	go func() {
		err := <-done
		setProxyErr(err)
		close(proxyExited)
	}()

	if mode == "p2p" {
		// P2P mode: use DHT
		dht.ServiceHeartbeatTimeout = p2pHeartbeatTimeout

		useDashboard := !background && stdinIsTerminalFn() && stdoutIsTerminalFn()
		var dashboard *p2pDashboard
		if !background && !useDashboard {
			fmt.Println("Starting in P2P mode with DHT...")
		}

		dht.ReplicationFactor = kClosest
		if !background && !useDashboard {
			fmt.Printf("DHT k-closest replication factor: %d\n", dht.ReplicationFactor)
			fmt.Printf("P2P heartbeat timeout: %s\n", dht.ServiceHeartbeatTimeout)
		}

		if !background && !useDashboard && len(bootstrapNodes) > 0 {
			fmt.Printf("Bootstrap nodes: %v\n", bootstrapNodes)
		}

		// create DHT registry
		var err error
		dhtRegistry, err = dht.NewDHTRegistry(p2pPort, bootstrapNodes)
		if err != nil {
			if background {
				os.Exit(1)
			}
			log.Fatalf("failed to create DHT registry: %v", err)
		}

		if useDashboard {
			dashboard = newP2PDashboard(dhtRegistry, listen, p2pPort, bootstrapNodes, kClosest, simplifiedUI)
			log.SetOutput(dashboard)
			client.SetLogOutput(dashboard)
			dht.SetLogOutput(dashboard)
			if simplifiedUI {
				fmt.Fprintln(dashboard, "starting in P2P mode with simplified dashboard UI")
			} else {
				fmt.Fprintln(dashboard, "starting in P2P mode with dashboard UI")
			}
		}

		if err := dhtRegistry.Start(); err != nil {
			if background {
				os.Exit(1)
			}
			if useDashboard {
				fmt.Fprintf(os.Stderr, "failed to start DHT: %v\n", err)
				os.Exit(1)
			}
			log.Fatalf("failed to start DHT: %v", err)
		}

		// run proxy with DHT backend
		go func() {
			done <- runProxyP2PFn(ctx, listen, dhtRegistry)
		}()

		if !background && !useDashboard {
			fmt.Printf("DHT node listening on %s\n", p2pPort)
			fmt.Printf("Client proxy listening on %s\n", listen)
		}

		if useDashboard {
			fmt.Fprintf(dashboard, "proxy listening on %s\n", listen)
			if err := dashboard.Run(proxyExited, getProxyErr, shutdown); err != nil {
				shutdown()
				fmt.Fprintf(os.Stderr, "client-proxy dashboard exited: %v\n", err)
				os.Exit(1)
			}
			shutdown()
			return
		}
	} else {
		// Traditional centralized mode
		if !background {
			fmt.Printf("Starting in centralized mode with %s transport...\n", transport)
		}

		go func() {
			// run proxy and report any error
			if transport == "tcp" {
				done <- runProxyTCPFn(ctx, listen)
			} else {
				done <- runProxyFn(ctx, listen)
			}
		}()

		if !background {
			fmt.Printf("Client proxy listening on %s\n", listen)
		}
	}

	if background {
		<-proxyExited
		if err := getProxyErr(); err != nil {
			os.Exit(1)
		}
		return
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
			shutdown()
			fmt.Println("exiting")
			return
		default:
			fmt.Println("unknown command; type 'help'")
		}
	}
	// stdin closed — shutdown
	shutdown()
}
