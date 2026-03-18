package client

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tds/pkg/transport"
)

// Test helper: create a mock UDP server
func createMockUDPServer(t *testing.T, handler func([]byte) []byte) (*net.UDPConn, string) {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("failed to create UDP server: %v", err)
	}

	addr := conn.LocalAddr().String()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, clientAddr, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			response := handler(buf[:n])
			if response != nil {
				conn.WriteToUDP(response, clientAddr)
			}
		}
	}()

	return conn, addr
}

// Test helper: create a mock TCP server
func createMockTCPServer(t *testing.T, handler func(string) string) (net.Listener, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create TCP server: %v", err)
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				scanner := bufio.NewScanner(c)
				for scanner.Scan() {
					line := scanner.Text()
					if line == "" {
						continue
					}
					response := handler(line)
					fmt.Fprintf(c, "%s\n", response)
				}
			}(conn)
		}
	}()

	return ln, ln.Addr().String()
}

// Test helper: generate self-signed certificates for TLS testing
func generateTestCert(t *testing.T) (certFile, keyFile, caFile string) {
	t.Helper()

	// Generate CA certificate
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}

	caTemplate := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test CA"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}

	// Generate server/client certificate
	certKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate cert key: %v", err)
	}

	certTemplate := x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			Organization: []string{"Test Cert"},
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:    []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &certTemplate, &caTemplate, &certKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}

	// Write CA cert
	caFile = t.TempDir() + "/ca.crt"
	f, err := os.Create(caFile)
	if err != nil {
		t.Fatalf("create ca file: %v", err)
	}
	pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: caCertDER})
	f.Close()

	// Write certificate
	certFile = t.TempDir() + "/cert.crt"
	f, err = os.Create(certFile)
	if err != nil {
		t.Fatalf("create cert file: %v", err)
	}
	pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	f.Close()

	// Write private key
	keyFile = t.TempDir() + "/cert.key"
	f, err = os.Create(keyFile)
	if err != nil {
		t.Fatalf("create key file: %v", err)
	}
	pem.Encode(f, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(certKey)})
	f.Close()

	return certFile, keyFile, caFile
}

// TestRegisterUDP tests UDP registration with various scenarios
func TestRegisterUDP(t *testing.T) {
	tests := []struct {
		name       string
		task       string
		address    string
		response   transport.CentralizedResponse
		wantErr    bool
		errContain string
	}{
		{
			name:     "successful registration",
			task:     "test-task",
			address:  "192.168.1.1:8080",
			response: transport.CentralizedResponse{Status: "OK"},
			wantErr:  false,
		},
		{
			name:       "server returns error",
			task:       "test-task",
			address:    "192.168.1.1:8080",
			response:   transport.CentralizedResponse{Status: "ERR", Error: "database error"},
			wantErr:    true,
			errContain: "database error",
		},
		{
			name:     "empty task name",
			task:     "",
			address:  "192.168.1.1:8080",
			response: transport.CentralizedResponse{Status: "OK"},
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, addr := createMockUDPServer(t, func(data []byte) []byte {
				// Verify the request is valid JSON
				var msg transport.CentralizedMessage
				if err := json.Unmarshal(data, &msg); err != nil {
					t.Errorf("invalid JSON request: %v", err)
				}
				// Return the configured response
				respData, _ := json.Marshal(tt.response)
				return respData
			})
			defer server.Close()

			err := RegisterUDP(addr, tt.task, tt.address)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error but got nil")
				} else if tt.errContain != "" && !strings.Contains(err.Error(), tt.errContain) {
					t.Errorf("expected error to contain %q, got %q", tt.errContain, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

// TestRegisterUDPWithCapacity tests capacity parameter handling
func TestRegisterUDPWithCapacity(t *testing.T) {
	tests := []struct {
		name            string
		capacity        int
		expectedCapInMsg int
	}{
		{"positive capacity", 10, 10},
		{"zero capacity defaults to 1", 0, 1},
		{"negative capacity defaults to 1", -5, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var receivedCapacity int
			server, addr := createMockUDPServer(t, func(data []byte) []byte {
				var msg transport.CentralizedMessage
				json.Unmarshal(data, &msg)
				receivedCapacity = msg.Capacity
				resp := transport.CentralizedResponse{Status: "OK"}
				respData, _ := json.Marshal(resp)
				return respData
			})
			defer server.Close()

			err := RegisterUDPWithCapacity(addr, "test-task", "192.168.1.1:8080", tt.capacity)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Give time for server to process
			time.Sleep(50 * time.Millisecond)

			if receivedCapacity != tt.expectedCapInMsg {
				t.Errorf("expected capacity %d, got %d", tt.expectedCapInMsg, receivedCapacity)
			}
		})
	}
}

// TestRegisterTCP tests TCP registration
func TestRegisterTCP(t *testing.T) {
	tests := []struct {
		name       string
		task       string
		address    string
		response   transport.CentralizedResponse
		wantErr    bool
		errContain string
	}{
		{
			name:     "successful registration",
			task:     "test-task",
			address:  "192.168.1.1:8080",
			response: transport.CentralizedResponse{Status: "OK"},
			wantErr:  false,
		},
		{
			name:       "server returns error",
			task:       "test-task",
			address:    "192.168.1.1:8080",
			response:   transport.CentralizedResponse{Status: "ERR", Error: "invalid address"},
			wantErr:    true,
			errContain: "invalid address",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, addr := createMockTCPServer(t, func(line string) string {
				respData, _ := json.Marshal(tt.response)
				return string(respData)
			})
			defer server.Close()

			err := RegisterTCP(addr, tt.task, tt.address)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error but got nil")
				} else if tt.errContain != "" && !strings.Contains(err.Error(), tt.errContain) {
					t.Errorf("expected error to contain %q, got %q", tt.errContain, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

// TestQueryUDP tests UDP query operations
func TestQueryUDP(t *testing.T) {
	tests := []struct {
		name       string
		task       string
		response   transport.CentralizedResponse
		wantAddr   string
		wantErr    bool
		errContain string
	}{
		{
			name:     "successful query",
			task:     "test-task",
			response: transport.CentralizedResponse{Status: "OK", Address: "192.168.1.1:8080"},
			wantAddr: "192.168.1.1:8080",
			wantErr:  false,
		},
		{
			name:     "task not found",
			task:     "nonexistent",
			response: transport.CentralizedResponse{Status: "NOTFOUND"},
			wantAddr: "",
			wantErr:  false,
		},
		{
			name:       "server error",
			task:       "test-task",
			response:   transport.CentralizedResponse{Status: "ERR", Error: "internal error"},
			wantAddr:   "",
			wantErr:    true,
			errContain: "internal error",
		},
		{
			name:       "forbidden",
			task:       "test-task",
			response:   transport.CentralizedResponse{Status: "FORBIDDEN"},
			wantAddr:   "",
			wantErr:    true,
			errContain: "forbidden",
		},
		{
			name:       "unexpected status",
			task:       "test-task",
			response:   transport.CentralizedResponse{Status: "MYSTERY"},
			wantAddr:   "",
			wantErr:    true,
			errContain: "MYSTERY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, addr := createMockUDPServer(t, func(data []byte) []byte {
				respData, _ := json.Marshal(tt.response)
				return respData
			})
			defer server.Close()

			gotAddr, err := QueryUDP(addr, tt.task)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error but got nil")
				} else if tt.errContain != "" && !strings.Contains(err.Error(), tt.errContain) {
					t.Errorf("expected error to contain %q, got %q", tt.errContain, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}

			if gotAddr != tt.wantAddr {
				t.Errorf("expected address %q, got %q", tt.wantAddr, gotAddr)
			}
		})
	}
}

// TestQueryTCP tests TCP query operations
func TestQueryTCP(t *testing.T) {
	tests := []struct {
		name       string
		task       string
		response   transport.CentralizedResponse
		wantAddr   string
		wantErr    bool
		errContain string
	}{
		{
			name:     "successful query",
			task:     "test-task",
			response: transport.CentralizedResponse{Status: "OK", Address: "10.0.0.1:9000"},
			wantAddr: "10.0.0.1:9000",
			wantErr:  false,
		},
		{
			name:     "task not found",
			task:     "unknown-task",
			response: transport.CentralizedResponse{Status: "NOTFOUND"},
			wantAddr: "",
			wantErr:  false,
		},
		{
			name:       "forbidden",
			task:       "blocked-task",
			response:   transport.CentralizedResponse{Status: "FORBIDDEN"},
			wantAddr:   "",
			wantErr:    true,
			errContain: "forbidden",
		},
		{
			name:       "unexpected status",
			task:       "test-task",
			response:   transport.CentralizedResponse{Status: "MYSTERY"},
			wantAddr:   "",
			wantErr:    true,
			errContain: "MYSTERY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, addr := createMockTCPServer(t, func(line string) string {
				respData, _ := json.Marshal(tt.response)
				return string(respData)
			})
			defer server.Close()

			gotAddr, err := QueryTCP(addr, tt.task)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error but got nil")
				} else if tt.errContain != "" && !strings.Contains(err.Error(), tt.errContain) {
					t.Errorf("expected error to contain %q, got %q", tt.errContain, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}

			if gotAddr != tt.wantAddr {
				t.Errorf("expected address %q, got %q", tt.wantAddr, gotAddr)
			}
		})
	}
}

// TestRegisterWithProtocolSelection tests the protocol selection based on env var
func TestRegisterWithProtocolSelection(t *testing.T) {
	tests := []struct {
		name     string
		proto    string
		wantType string
	}{
		{"UDP when proto is udp", "udp", "udp"},
		{"UDP when proto is UDP", "UDP", "udp"},
		{"TCP when proto is tcp", "tcp", "tcp"},
		{"TCP when proto is TCP", "TCP", "tcp"},
		{"UDP by default", "", "udp"},
		{"UDP on invalid proto", "invalid", "udp"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("TDS_SERVER_PROTO", tt.proto)
			defer os.Unsetenv("TDS_SERVER_PROTO")

			var receivedProto string

			// Create both UDP and TCP servers
			udpServer, udpAddr := createMockUDPServer(t, func(data []byte) []byte {
				receivedProto = "udp"
				resp := transport.CentralizedResponse{Status: "OK"}
				respData, _ := json.Marshal(resp)
				return respData
			})
			defer udpServer.Close()

			tcpServer, tcpAddr := createMockTCPServer(t, func(line string) string {
				receivedProto = "tcp"
				resp := transport.CentralizedResponse{Status: "OK"}
				respData, _ := json.Marshal(resp)
				return string(respData)
			})
			defer tcpServer.Close()

			// Use appropriate address based on expected protocol
			addr := udpAddr
			if tt.wantType == "tcp" {
				addr = tcpAddr
			}

			err := Register(addr, "test-task", "192.168.1.1:8080")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Give time for server to process
			time.Sleep(50 * time.Millisecond)

			if receivedProto != tt.wantType {
				t.Errorf("expected protocol %s, got %s", tt.wantType, receivedProto)
			}
		})
	}
}

// TestQueryWithProtocolSelection tests query protocol selection
func TestQueryWithProtocolSelection(t *testing.T) {
	tests := []struct {
		name     string
		proto    string
		wantType string
	}{
		{"UDP when proto is udp", "udp", "udp"},
		{"TCP when proto is tcp", "tcp", "tcp"},
		{"UDP by default", "", "udp"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("TDS_SERVER_PROTO", tt.proto)
			defer os.Unsetenv("TDS_SERVER_PROTO")

			var receivedProto string

			udpServer, udpAddr := createMockUDPServer(t, func(data []byte) []byte {
				receivedProto = "udp"
				resp := transport.CentralizedResponse{Status: "OK", Address: "192.168.1.1:8080"}
				respData, _ := json.Marshal(resp)
				return respData
			})
			defer udpServer.Close()

			tcpServer, tcpAddr := createMockTCPServer(t, func(line string) string {
				receivedProto = "tcp"
				resp := transport.CentralizedResponse{Status: "OK", Address: "192.168.1.1:8080"}
				respData, _ := json.Marshal(resp)
				return string(respData)
			})
			defer tcpServer.Close()

			addr := udpAddr
			if tt.wantType == "tcp" {
				addr = tcpAddr
			}

			_, err := Query(addr, "test-task")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			time.Sleep(50 * time.Millisecond)

			if receivedProto != tt.wantType {
				t.Errorf("expected protocol %s, got %s", tt.wantType, receivedProto)
			}
		})
	}
}

// TestInvalidServerAddress tests error handling for invalid addresses
func TestInvalidServerAddress(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		fn      func(string) error
		wantErr bool
	}{
		{"UDP invalid host", "invalid-host:99999", func(addr string) error {
			return RegisterUDP(addr, "task", "addr")
		}, true},
		{"TCP invalid host", "invalid-host:99999", func(addr string) error {
			return RegisterTCP(addr, "task", "addr")
		}, true},
		{"UDP empty address", "", func(addr string) error {
			return RegisterUDP(addr, "task", "addr")
		}, true},
		{"TCP empty address", "", func(addr string) error {
			return RegisterTCP(addr, "task", "addr")
		}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn(tt.addr)
			if (err != nil) != tt.wantErr {
				t.Errorf("expected error=%v, got=%v", tt.wantErr, err)
			}
		})
	}
}

// TestMalformedResponse tests handling of invalid JSON responses
func TestMalformedResponse(t *testing.T) {
	t.Run("UDP malformed JSON", func(t *testing.T) {
		server, addr := createMockUDPServer(t, func(data []byte) []byte {
			return []byte("not valid json")
		})
		defer server.Close()

		_, err := QueryUDP(addr, "test-task")
		if err == nil {
			t.Error("expected error for malformed JSON")
		}
		if !strings.Contains(err.Error(), "unmarshal") {
			t.Errorf("expected unmarshal error, got: %v", err)
		}
	})

	t.Run("TCP malformed JSON", func(t *testing.T) {
		server, addr := createMockTCPServer(t, func(line string) string {
			return "not valid json"
		})
		defer server.Close()

		_, err := QueryTCP(addr, "test-task")
		if err == nil {
			t.Error("expected error for malformed JSON")
		}
	})
}

// TestConcurrentRequests tests multiple concurrent requests
func TestConcurrentRequests(t *testing.T) {
	server, addr := createMockUDPServer(t, func(data []byte) []byte {
		resp := transport.CentralizedResponse{Status: "OK", Address: "192.168.1.1:8080"}
		respData, _ := json.Marshal(resp)
		return respData
	})
	defer server.Close()

	const numGoroutines = 50
	var wg sync.WaitGroup
	var errorCount int32

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			taskName := fmt.Sprintf("task-%d", id%10)
			_, err := QueryUDP(addr, taskName)
			if err != nil {
				atomic.AddInt32(&errorCount, 1)
			}
		}(i)
	}

	wg.Wait()

	if errorCount > 0 {
		t.Errorf("expected 0 errors, got %d", errorCount)
	}
}

// TestRegisterTLS tests TLS registration with certificates
func TestRegisterTLS(t *testing.T) {
	certFile, keyFile, caFile := generateTestCert(t)

	// Load the certificate for the server
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatalf("load server cert: %v", err)
	}

	caCert, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatalf("read ca cert: %v", err)
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)

	config := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    caCertPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}

	// Create TLS server
	ln, err := tls.Listen("tcp", "127.0.0.1:0", config)
	if err != nil {
		t.Fatalf("create TLS server: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				dec := json.NewDecoder(c)
				enc := json.NewEncoder(c)

				var msg transport.CentralizedMessage
				if err := dec.Decode(&msg); err != nil {
					return
				}

				resp := transport.CentralizedResponse{Status: "OK"}
				enc.Encode(resp)
			}(conn)
		}
	}()

	// Test TLS registration
	err = RegisterTLS(ln.Addr().String(), "test-task", "192.168.1.1:8080", certFile, keyFile, caFile)
	if err != nil {
		t.Errorf("RegisterTLS failed: %v", err)
	}
}

// TestQueryTLS tests TLS query operations
func TestQueryTLS(t *testing.T) {
	certFile, keyFile, caFile := generateTestCert(t)

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatalf("load server cert: %v", err)
	}

	caCert, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatalf("read ca cert: %v", err)
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)

	config := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    caCertPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", config)
	if err != nil {
		t.Fatalf("create TLS server: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				dec := json.NewDecoder(c)
				enc := json.NewEncoder(c)

				var msg transport.CentralizedMessage
				if err := dec.Decode(&msg); err != nil {
					return
				}

				resp := transport.CentralizedResponse{Status: "OK", Address: "192.168.1.1:8080"}
				enc.Encode(resp)
			}(conn)
		}
	}()

	addr, err := QueryTLS(ln.Addr().String(), "test-task", certFile, keyFile, caFile)
	if err != nil {
		t.Errorf("QueryTLS failed: %v", err)
	}
	if addr != "192.168.1.1:8080" {
		t.Errorf("expected address 192.168.1.1:8080, got %s", addr)
	}
}

// TestTLSInvalidCertificates tests TLS error handling
func TestTLSInvalidCertificates(t *testing.T) {
	tests := []struct {
		name       string
		certFile   string
		keyFile    string
		caFile     string
		wantErr    bool
		errContain string
	}{
		{
			name:       "missing cert file",
			certFile:   "/nonexistent/cert.crt",
			keyFile:    "/nonexistent/key.key",
			caFile:     "/nonexistent/ca.crt",
			wantErr:    true,
			errContain: "load",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RegisterTLS("127.0.0.1:5000", "task", "addr", tt.certFile, tt.keyFile, tt.caFile)
			if (err != nil) != tt.wantErr {
				t.Errorf("expected error=%v, got=%v", tt.wantErr, err)
			}
			if tt.wantErr && tt.errContain != "" && !strings.Contains(err.Error(), tt.errContain) {
				t.Errorf("expected error to contain %q, got %q", tt.errContain, err.Error())
			}
		})
	}
}

// TestTimeouts tests request timeout behavior
func TestTimeouts(t *testing.T) {
	t.Run("UDP timeout", func(t *testing.T) {
		// Create a server that doesn't respond
		conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
		if err != nil {
			t.Fatalf("failed to create UDP server: %v", err)
		}
		defer conn.Close()

		addr := conn.LocalAddr().String()

		// This should timeout
		_, err = QueryUDP(addr, "test-task")
		if err == nil {
			t.Error("expected timeout error")
		}
	})

	t.Run("TCP timeout", func(t *testing.T) {
		// Create a server that accepts but doesn't respond
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("failed to create TCP server: %v", err)
		}
		defer ln.Close()

		go func() {
			conn, _ := ln.Accept()
			if conn != nil {
				// Don't respond, just wait
				time.Sleep(5 * time.Second)
				conn.Close()
			}
		}()

		addr := ln.Addr().String()

		// This should timeout
		_, err = QueryTCP(addr, "test-task")
		if err == nil {
			t.Error("expected timeout error")
		}
	})
}

// TestRequestFormat tests that requests are properly formatted
func TestRequestFormat(t *testing.T) {
	t.Run("UDP request format", func(t *testing.T) {
		var receivedMsg transport.CentralizedMessage
		server, addr := createMockUDPServer(t, func(data []byte) []byte {
			json.Unmarshal(data, &receivedMsg)
			resp := transport.CentralizedResponse{Status: "OK"}
			respData, _ := json.Marshal(resp)
			return respData
		})
		defer server.Close()

		RegisterUDP(addr, "my-task", "10.0.0.1:9000")
		time.Sleep(50 * time.Millisecond)

		if receivedMsg.Command != "REGISTER" {
			t.Errorf("expected command REGISTER, got %s", receivedMsg.Command)
		}
		if receivedMsg.Task != "my-task" {
			t.Errorf("expected task my-task, got %s", receivedMsg.Task)
		}
		if receivedMsg.Address != "10.0.0.1:9000" {
			t.Errorf("expected address 10.0.0.1:9000, got %s", receivedMsg.Address)
		}
	})

	t.Run("TCP request format", func(t *testing.T) {
		var receivedMsg transport.CentralizedMessage
		server, addr := createMockTCPServer(t, func(line string) string {
			json.Unmarshal([]byte(line), &receivedMsg)
			resp := transport.CentralizedResponse{Status: "OK", Address: "10.0.0.1:9000"}
			respData, _ := json.Marshal(resp)
			return string(respData)
		})
		defer server.Close()

		QueryTCP(addr, "my-query-task")
		time.Sleep(50 * time.Millisecond)

		if receivedMsg.Command != "QUERY" {
			t.Errorf("expected command QUERY, got %s", receivedMsg.Command)
		}
		if receivedMsg.Task != "my-query-task" {
			t.Errorf("expected task my-query-task, got %s", receivedMsg.Task)
		}
	})
}

// BenchmarkRegisterUDP benchmarks UDP registration
func BenchmarkRegisterUDP(b *testing.B) {
	server, addr := createMockUDPServer(&testing.T{}, func(data []byte) []byte {
		resp := transport.CentralizedResponse{Status: "OK"}
		respData, _ := json.Marshal(resp)
		return respData
	})
	defer server.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		RegisterUDP(addr, "bench-task", "192.168.1.1:8080")
	}
}

// BenchmarkQueryUDP benchmarks UDP queries
func BenchmarkQueryUDP(b *testing.B) {
	server, addr := createMockUDPServer(&testing.T{}, func(data []byte) []byte {
		resp := transport.CentralizedResponse{Status: "OK", Address: "192.168.1.1:8080"}
		respData, _ := json.Marshal(resp)
		return respData
	})
	defer server.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		QueryUDP(addr, "bench-task")
	}
}
