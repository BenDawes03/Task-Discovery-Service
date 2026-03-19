package transport

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
	"tds/pkg/registry"
)

var (
	tcpAcquireTimeout        = 1 * time.Second
	tcpReadTimeout           = 120 * time.Second
	tcpWriteTimeout          = 15 * time.Second
	tlsHandshakeTimeout      = 5 * time.Second
	tcpKeepAlivePeriod       = 3 * time.Minute
	serverShutdownWaitPeriod = 3 * time.Second
)

type connTracker struct {
	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

func newConnTracker() *connTracker {
	return &connTracker{conns: make(map[net.Conn]struct{})}
}

func (ct *connTracker) Add(conn net.Conn) {
	ct.mu.Lock()
	ct.conns[conn] = struct{}{}
	ct.mu.Unlock()
}

func (ct *connTracker) Remove(conn net.Conn) {
	ct.mu.Lock()
	delete(ct.conns, conn)
	ct.mu.Unlock()
}

func (ct *connTracker) CloseAll() {
	ct.mu.Lock()
	conns := make([]net.Conn, 0, len(ct.conns))
	for conn := range ct.conns {
		conns = append(conns, conn)
	}
	ct.mu.Unlock()

	for _, conn := range conns {
		_ = conn.Close()
	}
}

func configureTCPKeepAlive(conn net.Conn) {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetKeepAlive(true)
		if tcpKeepAlivePeriod > 0 {
			_ = tcpConn.SetKeepAlivePeriod(tcpKeepAlivePeriod)
		}
		return
	}

	if tlsConn, ok := conn.(*tls.Conn); ok {
		if tcpConn, ok := tlsConn.NetConn().(*net.TCPConn); ok {
			_ = tcpConn.SetKeepAlive(true)
			if tcpKeepAlivePeriod > 0 {
				_ = tcpConn.SetKeepAlivePeriod(tcpKeepAlivePeriod)
			}
		}
	}
}

func encodeWithTimeout(conn net.Conn, encoder *json.Encoder, resp CentralizedResponse) error {
	if tcpWriteTimeout > 0 {
		_ = conn.SetWriteDeadline(time.Now().Add(tcpWriteTimeout))
	}
	err := encoder.Encode(resp)
	if tcpWriteTimeout > 0 {
		_ = conn.SetWriteDeadline(time.Time{})
	}
	return err
}

func waitForHandlers(wg *sync.WaitGroup) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	if serverShutdownWaitPeriod <= 0 {
		<-done
		return
	}

	select {
	case <-done:
	case <-time.After(serverShutdownWaitPeriod):
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

// StartTCPServerWithContext starts the TCP server and allows graceful shutdown via context cancellation.
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

	tracker := newConnTracker()
	var handlers sync.WaitGroup
	go func() {
		<-ctx.Done()
		tracker.CloseAll()
		_ = ln.Close()
	}()

	sem := semaphore.NewWeighted(maxConcurrent)

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				waitForHandlers(&handlers)
				return nil
			}
			// transient accept error: log and continue
			fmt.Printf("tcp accept error: %v\n", err)
			continue
		}
		configureTCPKeepAlive(conn)
		tracker.Add(conn)

		acquireCtx, cancel := context.WithTimeout(ctx, tcpAcquireTimeout)
		if err := sem.Acquire(acquireCtx, 1); err == nil {
			handlers.Add(1)
			go func(c net.Conn) {
				defer handlers.Done()
				defer tracker.Remove(c)
				defer sem.Release(1)
				handleTCPConn(c, reg, onEvent)
			}(conn)
		} else {
			tracker.Remove(conn)
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

	if tcpReadTimeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(tcpReadTimeout))
	}

	scanner := bufio.NewScanner(conn)
	encoder := json.NewEncoder(conn)

	for scanner.Scan() {
		if tcpReadTimeout > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(tcpReadTimeout))
		}
		line := scanner.Bytes()

		var msg CentralizedMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			errResp := CentralizedResponse{Status: "ERR", Error: "invalid JSON: " + err.Error()}
			if err := encodeWithTimeout(conn, encoder, errResp); err != nil {
				fmt.Printf("tcp encode error (invalid JSON response): %v\n", err)
				return
			}
			continue
		}

		resp := HandleMessage(reg, msg, requestorIP, remote, onEvent)
		if err := encodeWithTimeout(conn, encoder, resp); err != nil {
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
//
// maxConcurrent limits the number of concurrent connections. If 0, defaults to 5000.
func StartTCPServerTLS(reg registry.Registry, port int, maxConcurrent int64, certFile, keyFile, clientCAFile string, onEvent func(string)) error {
	return StartTCPServerTLSWithContext(context.Background(), reg, port, maxConcurrent, certFile, keyFile, clientCAFile, onEvent)
}

// StartTCPServerTLSWithContext starts the TLS server and allows graceful shutdown via context cancellation.
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

	tracker := newConnTracker()
	var handlers sync.WaitGroup
	go func() {
		<-ctx.Done()
		tracker.CloseAll()
		_ = ln.Close()
	}()

	if onEvent != nil {
		onEvent(fmt.Sprintf("TLS server started on %s (mutual auth enabled)", addr))
	}

	sem := semaphore.NewWeighted(maxConcurrent)

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				waitForHandlers(&handlers)
				return nil
			}
			fmt.Printf("tls accept error: %v\n", err)
			continue
		}
		configureTCPKeepAlive(conn)
		tracker.Add(conn)

		// Extract client certificate info from TLS connection
		if tlsConn, ok := conn.(*tls.Conn); ok {
			if tlsHandshakeTimeout > 0 {
				_ = tlsConn.SetDeadline(time.Now().Add(tlsHandshakeTimeout))
			}
			if err := tlsConn.Handshake(); err != nil {
				tracker.Remove(conn)
				_ = conn.Close()
				continue
			}
			if tlsHandshakeTimeout > 0 {
				_ = tlsConn.SetDeadline(time.Time{})
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

		acquireCtx, cancel := context.WithTimeout(ctx, tcpAcquireTimeout)
		if err := sem.Acquire(acquireCtx, 1); err == nil {
			handlers.Add(1)
			go func(c net.Conn) {
				defer handlers.Done()
				defer tracker.Remove(c)
				defer sem.Release(1)
				handleTCPConn(c, reg, onEvent)
			}(conn)
		} else {
			tracker.Remove(conn)
			conn.Close() // Reject connection due to overload
		}
		cancel()
	}
}
