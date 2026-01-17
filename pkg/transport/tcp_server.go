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

// StartTCPServer starts a JSON-based TCP server that understands
// the same JSON messages as the UDP server. Each connection is
// handled concurrently and may send multiple commands (one JSON object per line).
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
	scanner := bufio.NewScanner(conn)
	encoder := json.NewEncoder(conn)

	for scanner.Scan() {
		line := scanner.Bytes()
		
		// Parse JSON message
		var msg CentralizedMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			resp := CentralizedResponse{
				Status: "ERR",
				Error:  "invalid JSON: " + err.Error(),
			}
			encoder.Encode(resp)
			continue
		}

		// Handle command
		switch msg.Command {
		case "REGISTER":
			if msg.Task == "" || msg.Address == "" {
				resp := CentralizedResponse{
					Status: "ERR",
					Error:  "task and address required",
				}
				encoder.Encode(resp)
				continue
			}

			reg.Register(msg.Task, msg.Address)
			if onEvent != nil {
				onEvent(fmt.Sprintf("REGISTER %s -> %s from %v", msg.Task, msg.Address, remote))
			}

			resp := CentralizedResponse{Status: "OK"}
			encoder.Encode(resp)

		case "QUERY":
			if msg.Task == "" {
				resp := CentralizedResponse{
					Status: "ERR",
					Error:  "task required",
				}
				encoder.Encode(resp)
				continue
			}

			addrStr, err := reg.GetService(msg.Task)
			if onEvent != nil {
				onEvent(fmt.Sprintf("QUERY %s from %v", msg.Task, remote))
			}

			var resp CentralizedResponse
			if err == registry.ErrNotFound || addrStr == "" {
				resp = CentralizedResponse{Status: "NOTFOUND"}
			} else if err != nil {
				resp = CentralizedResponse{
					Status: "ERR",
					Error:  err.Error(),
				}
			} else {
				resp = CentralizedResponse{
					Status:  "OK",
					Address: addrStr,
				}
			}

			encoder.Encode(resp)

		default:
			resp := CentralizedResponse{
				Status: "ERR",
				Error:  "unknown command: " + msg.Command,
			}
			encoder.Encode(resp)
		}
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

		// Verify TLS connection and extract client certificate info
		tlsConn, ok := conn.(*tls.Conn)
		if ok {
			if err := tlsConn.Handshake(); err != nil {
				fmt.Printf("tls handshake error: %v\n", err)
				conn.Close()
				continue
			}
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
