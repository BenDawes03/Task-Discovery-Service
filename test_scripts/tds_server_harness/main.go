package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"tds/pkg/registry"
	"tds/pkg/transport"
)

func main() {
	mode := flag.String("mode", "udp", "Transport mode: udp|tcp|tls")
	port := flag.Int("port", 5500, "Listen port")
	heartbeatTimeout := flag.Duration("heartbeat-timeout", 5*time.Second, "Heartbeat timeout")
	cleanupInterval := flag.Duration("cleanup-interval", 1*time.Second, "Cleanup interval")
	certFile := flag.String("tls-cert", "certs/server.crt", "TLS server cert")
	keyFile := flag.String("tls-key", "certs/server.key", "TLS server key")
	clientCAFile := flag.String("tls-client-ca", "certs/ca.crt", "TLS client CA")
	flag.Parse()

	reg := registry.NewMemoryRegistry()

	go func() {
		ticker := time.NewTicker(*cleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			reg.Cleanup(*heartbeatTimeout)
		}
	}()

	logEvent := func(msg string) {
		fmt.Fprintf(os.Stderr, "[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), msg)
	}

	go func() {
		var err error
		switch strings.ToLower(*mode) {
		case "udp":
			err = transport.StartUDPServer(reg, *port, 0, logEvent) // 0 = use default
		case "tcp":
			err = transport.StartTCPServer(reg, *port, 0, logEvent) // 0 = use default
		case "tls":
			err = transport.StartTCPServerTLS(reg, *port, 0, *certFile, *keyFile, *clientCAFile, logEvent) // 0 = use default
		default:
			err = fmt.Errorf("unsupported mode %q", *mode)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "server error: %v\n", err)
			os.Exit(1)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
}
