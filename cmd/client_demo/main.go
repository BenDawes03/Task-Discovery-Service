package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"tds/pkg/client"
)

func main() {
	// TLS flags
	var (
		useTLS   bool
		certFile string
		keyFile  string
		caFile   string
	)

	flag.BoolVar(&useTLS, "tls", false, "Use TLS with mutual authentication")
	flag.StringVar(&certFile, "cert", "certs/client.crt", "Client TLS certificate file")
	flag.StringVar(&keyFile, "key", "certs/client.key", "Client TLS private key file")
	flag.StringVar(&caFile, "ca", "certs/ca.crt", "CA certificate to verify server")
	flag.Parse()

	// Prompt for transport (default udp, unless TLS is enabled)
	proto := "udp"
	if useTLS {
		proto = "tcp"
		fmt.Println("TLS enabled - using TCP transport")
	} else if fi, _ := os.Stdin.Stat(); (fi.Mode() & os.ModeCharDevice) != 0 {
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Select transport mode for demo (udp/tcp) [udp]: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input != "" {
			proto = strings.ToLower(input)
		}
	}
	os.Setenv("TDS_SERVER_PROTO", proto)

	server := "127.0.0.1:5000"
	task := "demo"
	addr := "127.0.0.1:12345"

	if useTLS {
		fmt.Printf("Registering %s -> %s using TLS with mutual auth\n", task, addr)
		if err := client.RegisterTLS(server, task, addr, certFile, keyFile, caFile); err != nil {
			fmt.Println("Register error:", err)
			return
		}

		// brief pause to allow server processing
		time.Sleep(200 * time.Millisecond)

		resp, err := client.QueryTLS(server, task, certFile, keyFile, caFile)
		if err != nil {
			fmt.Println("Query error:", err)
			return
		}
		fmt.Println("Query response:", resp)
	} else {
		fmt.Println("Registering", task, "->", addr, "using", proto)
		if err := client.Register(server, task, addr); err != nil {
			fmt.Println("Register error:", err)
			return
		}

		// brief pause to allow server processing
		time.Sleep(200 * time.Millisecond)

		resp, err := client.Query(server, task)
		if err != nil {
			fmt.Println("Query error:", err)
			return
		}
		fmt.Println("Query response:", resp)
	}
}
