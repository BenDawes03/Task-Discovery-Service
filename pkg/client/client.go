package client

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"tds/pkg/transport"
	"time"
)

// Register sends a REGISTER command to the server. Protocol is selected via
// the TDS_SERVER_PROTO environment variable ("tcp" or "udp", default "udp").
func Register(serverAddr, task, address string) error {
	return RegisterWithCapacity(serverAddr, task, address, 1)
}

// RegisterWithCapacity sends a REGISTER command with an optional capacity hint.
// capacity <= 0 is treated as 1.
func RegisterWithCapacity(serverAddr, task, address string, capacity int) error {
	if capacity <= 0 {
		capacity = 1
	}
	proto := strings.ToLower(os.Getenv("TDS_SERVER_PROTO"))
	if proto == "tcp" {
		return RegisterTCPWithCapacity(serverAddr, task, address, capacity)
	}
	return RegisterUDPWithCapacity(serverAddr, task, address, capacity)
}

// RegisterUDP performs a UDP register using JSON protocol.
func RegisterUDP(serverAddr, task, address string) error {
	return RegisterUDPWithCapacity(serverAddr, task, address, 1)
}

// RegisterUDPWithCapacity performs a UDP register using JSON protocol.
func RegisterUDPWithCapacity(serverAddr, task, address string, capacity int) error {
	if capacity <= 0 {
		capacity = 1
	}
	conn, err := net.DialTimeout("udp", serverAddr, 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Send JSON request
	msg := transport.CentralizedMessage{
		Command:  "REGISTER",
		Task:     task,
		Address:  address,
		Capacity: capacity,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	_, err = conn.Write(data)
	if err != nil {
		return err
	}

	// Read JSON response
	buf := make([]byte, 4096)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		return err
	}

	var resp transport.CentralizedResponse
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}

	if resp.Status == "OK" {
		return nil
	}
	return fmt.Errorf("server error: %s", resp.Error)
}

// RegisterTCP performs a TCP register using JSON protocol and reads the server response.
func RegisterTCP(serverAddr, task, address string) error {
	return RegisterTCPWithCapacity(serverAddr, task, address, 1)
}

// RegisterTCPWithCapacity performs a TCP register using JSON protocol and reads the server response.
func RegisterTCPWithCapacity(serverAddr, task, address string, capacity int) error {
	if capacity <= 0 {
		capacity = 1
	}
	conn, err := net.DialTimeout("tcp", serverAddr, 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Send JSON request (one line)
	msg := transport.CentralizedMessage{
		Command:  "REGISTER",
		Task:     task,
		Address:  address,
		Capacity: capacity,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	_, err = fmt.Fprintf(conn, "%s\n", string(data))
	if err != nil {
		return err
	}

	// Read JSON response
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	r := bufio.NewReader(conn)
	line, err := r.ReadBytes('\n')
	if err != nil {
		return err
	}

	var resp transport.CentralizedResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}

	if resp.Status == "OK" {
		return nil
	}
	return fmt.Errorf("server error: %s", resp.Error)
}

// Query asks the server for a task; returns the address or an empty string on not found.
// Protocol is selected via TDS_SERVER_PROTO ("tcp" or "udp").
func Query(serverAddr, task string) (string, error) {
	proto := strings.ToLower(os.Getenv("TDS_SERVER_PROTO"))
	if proto == "tcp" {
		return QueryTCP(serverAddr, task)
	}
	return QueryUDP(serverAddr, task)
}

// QueryUDP performs the query over UDP using JSON protocol.
func QueryUDP(serverAddr, task string) (string, error) {
	conn, err := net.DialTimeout("udp", serverAddr, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	// Send JSON request
	msg := transport.CentralizedMessage{
		Command: "QUERY",
		Task:    task,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	_, err = conn.Write(data)
	if err != nil {
		return "", err
	}

	// Read JSON response
	buf := make([]byte, 4096)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		return "", err
	}

	var resp transport.CentralizedResponse
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}

	if resp.Status == "NOTFOUND" {
		return "", nil
	}
	if resp.Status == "ERR" {
		return "", fmt.Errorf("server error: %s", resp.Error)
	}
	return resp.Address, nil
}

// QueryTCP performs the query over TCP using JSON protocol and interprets server replies.
func QueryTCP(serverAddr, task string) (string, error) {
	conn, err := net.DialTimeout("tcp", serverAddr, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	// Send JSON request (one line)
	msg := transport.CentralizedMessage{
		Command: "QUERY",
		Task:    task,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	_, err = fmt.Fprintf(conn, "%s\n", string(data))
	if err != nil {
		return "", err
	}

	// Read JSON response
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	r := bufio.NewReader(conn)
	line, err := r.ReadBytes('\n')
	if err != nil {
		return "", err
	}

	var resp transport.CentralizedResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}

	if resp.Status == "NOTFOUND" {
		return "", nil
	}
	if resp.Status == "ERR" {
		return "", fmt.Errorf("server error: %s", resp.Error)
	}
	return resp.Address, nil
}

// RegisterTLS performs a TCP register with TLS and mutual authentication.
// Requires client certificate, key, and CA cert to verify server.
func RegisterTLS(serverAddr, task, address, certFile, keyFile, caFile string) error {
	config, err := loadTLSConfig(certFile, keyFile, caFile, serverAddr)
	if err != nil {
		return err
	}

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", serverAddr, config)
	if err != nil {
		return fmt.Errorf("tls dial: %w", err)
	}
	defer conn.Close()

	// Verify TLS handshake
	if err := conn.Handshake(); err != nil {
		return fmt.Errorf("tls handshake: %w", err)
	}

	// Send JSON request and read JSON response
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)

	msg := transport.CentralizedMessage{
		Command:  "REGISTER",
		Task:     task,
		Address:  address,
		Capacity: 1,
	}
	if err := enc.Encode(msg); err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	var resp transport.CentralizedResponse
	if err := dec.Decode(&resp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if resp.Status == "OK" {
		return nil
	}
	if resp.Error != "" {
		return fmt.Errorf("server error: %s", resp.Error)
	}
	return fmt.Errorf("server error: %s", resp.Status)
}

// QueryTLS performs a query over TLS with mutual authentication.
func QueryTLS(serverAddr, task, certFile, keyFile, caFile string) (string, error) {
	config, err := loadTLSConfig(certFile, keyFile, caFile, serverAddr)
	if err != nil {
		return "", err
	}

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", serverAddr, config)
	if err != nil {
		return "", fmt.Errorf("tls dial: %w", err)
	}
	defer conn.Close()

	// Verify TLS handshake
	if err := conn.Handshake(); err != nil {
		return "", fmt.Errorf("tls handshake: %w", err)
	}

	conn.SetDeadline(time.Now().Add(5 * time.Second))
	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)

	msg := transport.CentralizedMessage{
		Command: "QUERY",
		Task:    task,
	}
	if err := enc.Encode(msg); err != nil {
		return "", fmt.Errorf("encode request: %w", err)
	}

	var resp transport.CentralizedResponse
	if err := dec.Decode(&resp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if resp.Status == "NOTFOUND" {
		return "", nil
	}
	if resp.Status == "ERR" {
		return "", fmt.Errorf("server error: %s", resp.Error)
	}
	if resp.Status != "OK" {
		return "", fmt.Errorf("server error: %s", resp.Status)
	}
	return resp.Address, nil
}

func tlsServerName(serverAddr string) string {
	override := strings.TrimSpace(os.Getenv("TDS_TLS_SERVER_NAME"))
	if override != "" {
		return override
	}

	host, _, err := net.SplitHostPort(strings.TrimSpace(serverAddr))
	if err != nil {
		return ""
	}
	host = strings.Trim(host, "[]")
	lowerHost := strings.ToLower(host)

	// Kubernetes service DNS for TDS currently fronts a cert issued for localhost.
	if lowerHost == "tds-server" || strings.HasPrefix(lowerHost, "tds-server.") {
		return "localhost"
	}

	return ""
}

// loadTLSConfig creates a TLS configuration with client certificate and CA verification.
func loadTLSConfig(certFile, keyFile, caFile, serverAddr string) (*tls.Config, error) {
	// Load client certificate
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load client cert: %w", err)
	}

	// Load CA cert to verify server
	caCert, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("load ca cert: %w", err)
	}
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}

	config := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
		MinVersion:   tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		},
	}

	if serverName := tlsServerName(serverAddr); serverName != "" {
		config.ServerName = serverName
	}

	return config, nil
}
