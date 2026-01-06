package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

func main() {
	serverAddr := flag.String("server", "localhost:5100", "Proxy server address")
	flag.Parse()

	if len(flag.Args()) == 0 {
		fmt.Println("Usage: test_client -server <addr> <command>")
		fmt.Println("Examples:")
		fmt.Println("  test_client REGISTER my-service 192.168.1.50:8080")
		fmt.Println("  test_client QUERY my-service")
		os.Exit(1)
	}

	command := strings.Join(flag.Args(), " ")

	// Send UDP request
	conn, err := net.DialTimeout("udp", *serverAddr, 2*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Send command
	_, err = fmt.Fprintf(conn, "%s\n", command)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to send: %v\n", err)
		os.Exit(1)
	}

	// Read response
	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to receive: %v\n", err)
		os.Exit(1)
	}

	response := strings.TrimSpace(string(buf[:n]))
	fmt.Println(response)
}
