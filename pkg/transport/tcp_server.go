package transport

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"strings"
	"tds/pkg/registry"
)

// StartTCPServer starts a simple line-oriented TCP server that understands
// the same REGISTER / QUERY commands as the UDP server. Each connection is
// handled concurrently and may send multiple commands (one per line).
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
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			// EOF or read error -> close connection
			return
		}
		line = strings.TrimSpace(line)
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		cmd := strings.ToUpper(parts[0])
		switch cmd {
		case "REGISTER":
			if len(parts) >= 3 {
				task := parts[1]
				addrParam := parts[2]
				reg.Register(task, addrParam)
				if onEvent != nil {
					onEvent(fmt.Sprintf("REGISTER %s -> %s from %v", task, addrParam, remote))
				}
				conn.Write([]byte("OK\n"))
			} else {
				conn.Write([]byte("ERR\n"))
			}
		case "QUERY":
			if len(parts) >= 2 {
				task := parts[1]
				addrStr, err := reg.GetService(task)
				if onEvent != nil {
					onEvent(fmt.Sprintf("QUERY %s from %v", task, remote))
				}
				if err == registry.ErrNotFound || addrStr == "" {
					conn.Write([]byte("NOTFOUND\n"))
				} else if err != nil {
					conn.Write([]byte("ERR\n"))
				} else {
					conn.Write([]byte(addrStr + "\n"))
				}
			} else {
				conn.Write([]byte("ERR\n"))
			}
		default:
			conn.Write([]byte("ERR\n"))
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
