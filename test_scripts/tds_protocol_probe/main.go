package main

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"tds/pkg/transport"
)

type ProbeResult struct {
	Status  string `json:"status,omitempty"`
	Address string `json:"address,omitempty"`
	Error   string `json:"error,omitempty"`
}

func main() {
	mode := flag.String("mode", "udp", "Transport mode: udp|tcp|tls")
	server := flag.String("server", "127.0.0.1:5000", "Server address host:port")
	command := flag.String("cmd", "QUERY", "Command to send (for generated payload)")
	task := flag.String("task", "", "Task name")
	address := flag.String("address", "", "Service address (for REGISTER)")
	raw := flag.String("raw", "", "Raw payload to send (if set, overrides generated JSON)")
	timeout := flag.Duration("timeout", 3*time.Second, "Request timeout")
	certFile := flag.String("cert", "certs/client.crt", "Client certificate for TLS")
	keyFile := flag.String("key", "certs/client.key", "Client private key for TLS")
	caFile := flag.String("ca", "certs/ca.crt", "CA cert for TLS server verification")
	flag.Parse()

	var payload string
	if *raw != "" {
		payload = *raw
	} else {
		msg := transport.CentralizedMessage{
			Command: strings.ToUpper(*command),
			Task:    *task,
			Address: *address,
		}
		data, err := json.Marshal(msg)
		if err != nil {
			fatalf("marshal request: %v", err)
		}
		payload = string(data)
	}

	result, err := send(strings.ToLower(*mode), *server, payload, *timeout, *certFile, *keyFile, *caFile)
	if err != nil {
		fatalf("probe request failed: %v", err)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(result); err != nil {
		fatalf("encode result: %v", err)
	}
}

func send(mode, server, payload string, timeout time.Duration, certFile, keyFile, caFile string) (*ProbeResult, error) {
	switch mode {
	case "udp":
		return sendUDP(server, payload, timeout)
	case "tcp":
		return sendTCP(server, payload, timeout)
	case "tls":
		return sendTLS(server, payload, timeout, certFile, keyFile, caFile)
	default:
		return nil, fmt.Errorf("unsupported mode %q", mode)
	}
}

func sendUDP(server, payload string, timeout time.Duration) (*ProbeResult, error) {
	conn, err := net.DialTimeout("udp", server, timeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}

	if _, err := conn.Write([]byte(payload)); err != nil {
		return nil, err
	}

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}

	var response transport.CentralizedResponse
	if err := json.Unmarshal(buf[:n], &response); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return &ProbeResult{Status: response.Status, Address: response.Address, Error: response.Error}, nil
}

func sendTCP(server, payload string, timeout time.Duration) (*ProbeResult, error) {
	conn, err := net.DialTimeout("tcp", server, timeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}

	if _, err := fmt.Fprintf(conn, "%s\n", payload); err != nil {
		return nil, err
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return nil, err
	}

	var response transport.CentralizedResponse
	if err := json.Unmarshal(line, &response); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return &ProbeResult{Status: response.Status, Address: response.Address, Error: response.Error}, nil
}

func sendTLS(server, payload string, timeout time.Duration, certFile, keyFile, caFile string) (*ProbeResult, error) {
	config, err := tlsConfig(server, certFile, keyFile, caFile)
	if err != nil {
		return nil, err
	}

	dialer := &net.Dialer{Timeout: timeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", server, config)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}

	if err := conn.Handshake(); err != nil {
		return nil, err
	}

	if _, err := fmt.Fprintf(conn, "%s\n", payload); err != nil {
		return nil, err
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("connection closed by server")
		}
		return nil, err
	}

	var response transport.CentralizedResponse
	if err := json.Unmarshal(line, &response); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return &ProbeResult{Status: response.Status, Address: response.Address, Error: response.Error}, nil
}

func tlsConfig(serverAddr, certFile, keyFile, caFile string) (*tls.Config, error) {
	clientCert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load client cert/key: %w", err)
	}

	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read ca cert: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("parse ca cert: failed")
	}

	serverName := ""
	if host, _, err := net.SplitHostPort(serverAddr); err == nil {
		serverName = host
	}

	return &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS12,
		ServerName:   serverName,
	}, nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
