package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"tds/pkg/client"
)

func main() {
	// Prompt for transport (default udp)
	proto := "udp"
	if fi, _ := os.Stdin.Stat(); (fi.Mode() & os.ModeCharDevice) != 0 {
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
