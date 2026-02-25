package transport

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"os"

	"tds/pkg/registry"
)

// StartTCPServer starts a JSON-based TCP server that understands the same JSON
// messages as the UDP server. Each connection is handled concurrently and may
// send multiple commands (one JSON object per line).
func StartTCPServer(reg registry.Registry, port int, onEvent func(string)) error {
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen tcp: %w", err)
	}
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			// transient accept error: log and continue
			fmt.Printf("tcp accept error: %v\n", err)
			continue
		}
		go handleTCPConn(conn, reg, onEvent)
	}
}

func handleTCPConn(conn net.Conn, reg registry.Registry, onEvent func(string)) {
	defer conn.Close()
	remote := conn.RemoteAddr()

	// Extract requestor IP for firewall-aware routing.
	var requestorIP net.IP
	if tcpAddr, ok := remote.(*net.TCPAddr); ok {
		requestorIP = tcpAddr.IP
	}

	scanner := bufio.NewScanner(conn)
	encoder := json.NewEncoder(conn)

	for scanner.Scan() {
		line := scanner.Bytes()

		var msg CentralizedMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			errResp := CentralizedResponse{Status: "ERR", Error: "invalid JSON: " + err.Error()}
			if err := encoder.Encode(errResp); err != nil {
				fmt.Printf("tcp encode error (invalid JSON response): %v\n", err)
				return
			}
			continue
		}

		resp := HandleMessage(reg, msg, requestorIP, remote, onEvent)
		if err := encoder.Encode(resp); err != nil {
			fmt.Printf("tcp encode error: %v\n", err)
			return
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("tcp scan error: %v\n", err)
	}
}

// StartTCPServerTLS starts a TLS-enabled TCP server with mutual authentication.
// Requires server certificate/key and CA cert to verify client certificates.
func StartTCPServerTLS(reg registry.Registry, port int, certFile, keyFile, clientCAFile string, onEvent func(string)) error {
	// Load server certificate
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("load server cert: %w", err)
	}

	// Load CA cert to verify client certificates
	caCert, err := os.ReadFile(clientCAFile)
	if err != nil {
		return fmt.Errorf("load client CA cert: %w", err)
	}
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return fmt.Errorf("failed to parse client CA certificate")
	}

	// Configure TLS with mutual authentication
	config := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert, // Require client certificates
		ClientCAs:    caCertPool,
		MinVersion:   tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		},
	}

	addr := fmt.Sprintf("0.0.0.0:%d", port)
	ln, err := tls.Listen("tcp", addr, config)
	if err != nil {
		return fmt.Errorf("listen tls: %w", err)
	}
	defer ln.Close()

	if onEvent != nil {
		onEvent(fmt.Sprintf("TLS server started on %s (mutual auth enabled)", addr))
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			fmt.Printf("tls accept error: %v\n", err)
			continue
		}

		// Extract client certificate info from TLS connection
		if tlsConn, ok := conn.(*tls.Conn); ok {
			state := tlsConn.ConnectionState()
			if len(state.PeerCertificates) > 0 {
				clientCert := state.PeerCertificates[0]
				if onEvent != nil {
					onEvent(fmt.Sprintf("TLS client authenticated: %s (CN=%s)",
						conn.RemoteAddr(), clientCert.Subject.CommonName))
				}
			}
		}

		go handleTCPConn(conn, reg, onEvent)
	}
}
