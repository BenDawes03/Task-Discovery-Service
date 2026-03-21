package transport

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
	"tds/pkg/registry"
)

func isExpectedTLSProbeError(err error) bool {
	if err == nil {
		return false
	}
	// Common when plain TCP probes hit a TLS-only listener.
	return strings.Contains(err.Error(), "first record does not look like a TLS handshake")
}

func transportEvent(onEvent func(string), format string, args ...interface{}) {
	if onEvent != nil {
		onEvent(fmt.Sprintf(format, args...))
	}
}

// StartTCPServer starts a JSON-based TCP server that understands the same JSON
// messages as the UDP server. Each connection is handled concurrently and may
// send multiple commands (one JSON object per line).
//
// maxConcurrent limits the number of concurrent connections. If 0, defaults to 5000.
func StartTCPServer(reg registry.Registry, port int, maxConcurrent int64, onEvent func(string)) error {
	return StartTCPServerWithContext(context.Background(), reg, port, maxConcurrent, onEvent)
}

// StartTCPServerWithContext starts a TCP server that shuts down when ctx is canceled.
func StartTCPServerWithContext(ctx context.Context, reg registry.Registry, port int, maxConcurrent int64, onEvent func(string)) error {
	if maxConcurrent <= 0 {
		maxConcurrent = 5000 // Default limit
	}

	addr := fmt.Sprintf("0.0.0.0:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen tcp: %w", err)
	}
	defer ln.Close()

	var (
		connMu      sync.Mutex
		activeConns = make(map[net.Conn]struct{})
		handlers    sync.WaitGroup
	)

	go func() {
		<-ctx.Done()
		_ = ln.Close()
		connMu.Lock()
		for c := range activeConns {
			_ = c.Close()
		}
		connMu.Unlock()
	}()

	sem := semaphore.NewWeighted(maxConcurrent)
	const acquireTimeout = 1 * time.Second

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				handlers.Wait()
				return nil
			}
			// transient accept error: log and continue
			transportEvent(onEvent, "tcp accept error: %v", err)
			continue
		}
		connMu.Lock()
		activeConns[conn] = struct{}{}
		connMu.Unlock()

		acquireCtx, cancel := context.WithTimeout(context.Background(), acquireTimeout)
		if err := sem.Acquire(acquireCtx, 1); err == nil {
			handlers.Add(1)
			go func(c net.Conn) {
				defer handlers.Done()
				defer sem.Release(1)
				defer func() {
					connMu.Lock()
					delete(activeConns, c)
					connMu.Unlock()
				}()
				handleTCPConn(c, reg, onEvent)
			}(conn)
		} else {
			connMu.Lock()
			delete(activeConns, conn)
			connMu.Unlock()
			conn.Close() // Reject connection due to overload
		}
		cancel()
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
				transportEvent(onEvent, "tcp encode error (invalid JSON response): %v", err)
				return
			}
			continue
		}

		resp := HandleMessage(reg, msg, requestorIP, remote, onEvent)
		if err := encoder.Encode(resp); err != nil {
			transportEvent(onEvent, "tcp encode error: %v", err)
			return
		}
	}

	if err := scanner.Err(); err != nil {
		if isExpectedTLSProbeError(err) {
			return
		}
		transportEvent(onEvent, "tcp scan error: %v", err)
	}
}

// StartTCPServerTLS starts a TLS-enabled TCP server with mutual authentication.
// Requires server certificate/key and CA cert to verify client certificates.
//
// maxConcurrent limits the number of concurrent connections. If 0, defaults to 5000.
func StartTCPServerTLS(reg registry.Registry, port int, maxConcurrent int64, certFile, keyFile, clientCAFile string, onEvent func(string)) error {
	return StartTCPServerTLSWithContext(context.Background(), reg, port, maxConcurrent, certFile, keyFile, clientCAFile, onEvent)
}

// StartTCPServerTLSWithContext starts a TLS server that shuts down when ctx is canceled.
func StartTCPServerTLSWithContext(ctx context.Context, reg registry.Registry, port int, maxConcurrent int64, certFile, keyFile, clientCAFile string, onEvent func(string)) error {
	if maxConcurrent <= 0 {
		maxConcurrent = 5000 // Default limit
	}
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

	var (
		connMu      sync.Mutex
		activeConns = make(map[net.Conn]struct{})
		handlers    sync.WaitGroup
	)

	go func() {
		<-ctx.Done()
		_ = ln.Close()
		connMu.Lock()
		for c := range activeConns {
			_ = c.Close()
		}
		connMu.Unlock()
	}()

	if onEvent != nil {
		onEvent(fmt.Sprintf("TLS server started on %s (mutual auth enabled)", addr))
	}

	sem := semaphore.NewWeighted(maxConcurrent)
	const acquireTimeout = 1 * time.Second

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				handlers.Wait()
				return nil
			}
			if isExpectedTLSProbeError(err) {
				continue
			}
			transportEvent(onEvent, "tls accept error: %v", err)
			continue
		}
		connMu.Lock()
		activeConns[conn] = struct{}{}
		connMu.Unlock()

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

		acquireCtx, cancel := context.WithTimeout(context.Background(), acquireTimeout)
		if err := sem.Acquire(acquireCtx, 1); err == nil {
			handlers.Add(1)
			go func(c net.Conn) {
				defer handlers.Done()
				defer sem.Release(1)
				defer func() {
					connMu.Lock()
					delete(activeConns, c)
					connMu.Unlock()
				}()
				handleTCPConn(c, reg, onEvent)
			}(conn)
		} else {
			connMu.Lock()
			delete(activeConns, conn)
			connMu.Unlock()
			conn.Close() // Reject connection due to overload
		}
		cancel()
	}
}
