package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"tds/pkg/client"
)

func main() {
	listen := os.Getenv("TDS_PROXY_LISTEN")
	if listen == "" {
		listen = ":5100"
	}

	// Ask which transport to run (interactive). Default is UDP.
	transportMode := "udp"
	if term.IsTerminal(int(os.Stdin.Fd())) {
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Select transport mode (udp/tcp) [udp]: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input != "" {
			transportMode = strings.ToLower(input)
		}
	} else {
		fmt.Fprintln(os.Stderr, "No interactive terminal detected; defaulting to UDP transport")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		// run proxy and report any error
		if transportMode == "tcp" {
			done <- client.RunProxyTCP(ctx, listen)
		} else {
			done <- client.RunProxy(ctx, listen)
		}
	}()

	// simple interactive loop
	fmt.Println("client-proxy running. type 'help' for commands.")
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
			fmt.Println("commands: help, stats, quit")
		case "stats":
			regs, queries, errs := client.Stats()
			fmt.Printf("stats: registers=%d queries=%d errors=%d\n", regs, queries, errs)
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
