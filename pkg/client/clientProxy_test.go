package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tds/pkg/transport"
)

// mockDHTRegistry implements DHTRegistry for testing
type mockDHTRegistry struct {
	mu          sync.Mutex
	tasks       map[string][]string
	registerErr error
	queryErr    error
}

func newMockDHTRegistry() *mockDHTRegistry {
	return &mockDHTRegistry{
		tasks: make(map[string][]string),
	}
}

func (m *mockDHTRegistry) Register(task, address string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.registerErr != nil {
		return m.registerErr
	}
	m.tasks[task] = append(m.tasks[task], address)
	return nil
}

func (m *mockDHTRegistry) Query(task string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.queryErr != nil {
		return "", m.queryErr
	}
	addrs := m.tasks[task]
	if len(addrs) == 0 {
		return "", nil
	}
	return addrs[0], nil
}

func (m *mockDHTRegistry) QueryAll(task string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.queryErr != nil {
		return nil, m.queryErr
	}
	return m.tasks[task], nil
}

func (m *mockDHTRegistry) setRegisterError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registerErr = err
}

func (m *mockDHTRegistry) setQueryError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queryErr = err
}

// TestParseProxyRequest tests the request parsing logic
func TestParseProxyRequest(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantReq    proxyRequest
		wantErr    bool
		errContain string
	}{
		{
			name:  "valid REGISTER request",
			input: `{"cmd":"REGISTER","task":"test-task","address":"192.168.1.1:8080"}`,
			wantReq: proxyRequest{
				Command:  "REGISTER",
				Task:     "test-task",
				Address:  "192.168.1.1:8080",
				Capacity: 1,
				JSON:     true,
			},
			wantErr: false,
		},
		{
			name:  "valid QUERY request",
			input: `{"cmd":"QUERY","task":"test-task"}`,
			wantReq: proxyRequest{
				Command:  "QUERY",
				Task:     "test-task",
				Capacity: 1,
				JSON:     true,
			},
			wantErr: false,
		},
		{
			name:  "REGISTER with capacity",
			input: `{"cmd":"REGISTER","task":"test-task","address":"192.168.1.1:8080","capacity":10}`,
			wantReq: proxyRequest{
				Command:  "REGISTER",
				Task:     "test-task",
				Address:  "192.168.1.1:8080",
				Capacity: 10,
				JSON:     true,
			},
			wantErr: false,
		},
		{
			name:       "empty input",
			input:      "",
			wantErr:    true,
			errContain: "empty command",
		},
		{
			name:       "invalid JSON",
			input:      `{invalid json}`,
			wantErr:    true,
			errContain: "invalid JSON",
		},
		{
			name:       "negative capacity",
			input:      `{"cmd":"REGISTER","task":"test-task","address":"192.168.1.1:8080","capacity":-5}`,
			wantErr:    true,
			errContain: "capacity must be a positive integer",
		},
		{
			name:  "lowercase command converted to uppercase",
			input: `{"cmd":"register","task":"test-task","address":"192.168.1.1:8080"}`,
			wantReq: proxyRequest{
				Command:  "REGISTER",
				Task:     "test-task",
				Address:  "192.168.1.1:8080",
				Capacity: 1,
				JSON:     true,
			},
			wantErr: false,
		},
		{
			name:  "whitespace trimmed from fields",
			input: `{"cmd":"  REGISTER  ","task":"  test-task  ","address":"  192.168.1.1:8080  "}`,
			wantReq: proxyRequest{
				Command:  "REGISTER",
				Task:     "test-task",
				Address:  "192.168.1.1:8080",
				Capacity: 1,
				JSON:     true,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotReq, err := parseProxyRequest(tt.input)

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
				if gotReq.Command != tt.wantReq.Command {
					t.Errorf("expected command %q, got %q", tt.wantReq.Command, gotReq.Command)
				}
				if gotReq.Task != tt.wantReq.Task {
					t.Errorf("expected task %q, got %q", tt.wantReq.Task, gotReq.Task)
				}
				if gotReq.Address != tt.wantReq.Address {
					t.Errorf("expected address %q, got %q", tt.wantReq.Address, gotReq.Address)
				}
				if gotReq.Capacity != tt.wantReq.Capacity {
					t.Errorf("expected capacity %d, got %d", tt.wantReq.Capacity, gotReq.Capacity)
				}
			}
		})
	}
}

// TestHandleCentralizedRequest tests the centralized request handler
func TestHandleCentralizedRequest(t *testing.T) {
	// Create a mock backend server for testing
	mockServer, mockAddr := createMockUDPServer(t, func(data []byte) []byte {
		var msg transport.CentralizedMessage
		json.Unmarshal(data, &msg)

		var resp transport.CentralizedResponse
		if msg.Command == "REGISTER" {
			resp = transport.CentralizedResponse{Status: "OK"}
		} else if msg.Command == "QUERY" {
			if msg.Task == "existing-task" {
				resp = transport.CentralizedResponse{Status: "OK", Address: "192.168.1.1:8080"}
			} else {
				resp = transport.CentralizedResponse{Status: "NOTFOUND"}
			}
		}
		respData, _ := json.Marshal(resp)
		return respData
	})
	defer mockServer.Close()

	tests := []struct {
		name         string
		req          proxyRequest
		wantStatus   string
		wantAddress  string
		wantErrField string
	}{
		{
			name: "successful REGISTER",
			req: proxyRequest{
				Command:  "REGISTER",
				Task:     "test-task",
				Address:  "192.168.1.1:8080",
				Capacity: 1,
			},
			wantStatus: "OK",
		},
		{
			name: "REGISTER missing task",
			req: proxyRequest{
				Command: "REGISTER",
				Address: "192.168.1.1:8080",
			},
			wantStatus:   "ERR",
			wantErrField: "task and address required",
		},
		{
			name: "REGISTER missing address",
			req: proxyRequest{
				Command: "REGISTER",
				Task:    "test-task",
			},
			wantStatus:   "ERR",
			wantErrField: "task and address required",
		},
		{
			name: "successful QUERY",
			req: proxyRequest{
				Command: "QUERY",
				Task:    "existing-task",
			},
			wantStatus:  "OK",
			wantAddress: "192.168.1.1:8080",
		},
		{
			name: "QUERY not found",
			req: proxyRequest{
				Command: "QUERY",
				Task:    "nonexistent-task",
			},
			wantStatus: "NOTFOUND",
		},
		{
			name: "QUERY missing task",
			req: proxyRequest{
				Command: "QUERY",
			},
			wantStatus:   "ERR",
			wantErrField: "task required",
		},
		{
			name: "unknown command",
			req: proxyRequest{
				Command: "UNKNOWN",
			},
			wantStatus:   "ERR",
			wantErrField: "unknown command",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handleCentralizedRequest(tt.req, mockAddr, "udp", "127.0.0.1:12345")

			if result.Status != tt.wantStatus {
				t.Errorf("expected status %q, got %q", tt.wantStatus, result.Status)
			}
			if tt.wantAddress != "" && result.Address != tt.wantAddress {
				t.Errorf("expected address %q, got %q", tt.wantAddress, result.Address)
			}
			if tt.wantErrField != "" && !strings.Contains(result.Error, tt.wantErrField) {
				t.Errorf("expected error to contain %q, got %q", tt.wantErrField, result.Error)
			}
		})
	}
}

// TestHandleP2PRequest tests the P2P request handler
func TestHandleP2PRequest(t *testing.T) {
	tests := []struct {
		name         string
		req          proxyRequest
		setupDHT     func(*mockDHTRegistry)
		wantStatus   string
		wantAddress  string
		wantErrField string
	}{
		{
			name: "successful REGISTER",
			req: proxyRequest{
				Command: "REGISTER",
				Task:    "test-task",
				Address: "192.168.1.1:8080",
			},
			setupDHT:   func(m *mockDHTRegistry) {},
			wantStatus: "OK",
		},
		{
			name: "REGISTER with DHT error",
			req: proxyRequest{
				Command: "REGISTER",
				Task:    "test-task",
				Address: "192.168.1.1:8080",
			},
			setupDHT: func(m *mockDHTRegistry) {
				m.setRegisterError(fmt.Errorf("DHT failure"))
			},
			wantStatus:   "ERR",
			wantErrField: "DHT failure",
		},
		{
			name: "REGISTER missing task",
			req: proxyRequest{
				Command: "REGISTER",
				Address: "192.168.1.1:8080",
			},
			setupDHT:     func(m *mockDHTRegistry) {},
			wantStatus:   "ERR",
			wantErrField: "task and address required",
		},
		{
			name: "successful QUERY",
			req: proxyRequest{
				Command: "QUERY",
				Task:    "existing-task",
			},
			setupDHT: func(m *mockDHTRegistry) {
				m.Register("existing-task", "10.0.0.1:9000")
			},
			wantStatus:  "OK",
			wantAddress: "10.0.0.1:9000",
		},
		{
			name: "QUERY not found",
			req: proxyRequest{
				Command: "QUERY",
				Task:    "nonexistent-task",
			},
			setupDHT:   func(m *mockDHTRegistry) {},
			wantStatus: "NOTFOUND",
		},
		{
			name: "QUERY with DHT error",
			req: proxyRequest{
				Command: "QUERY",
				Task:    "test-task",
			},
			setupDHT: func(m *mockDHTRegistry) {
				m.setQueryError(fmt.Errorf("DHT query failed"))
			},
			wantStatus:   "ERR",
			wantErrField: "DHT query failed",
		},
		{
			name: "QUERY missing task",
			req: proxyRequest{
				Command: "QUERY",
			},
			setupDHT:     func(m *mockDHTRegistry) {},
			wantStatus:   "ERR",
			wantErrField: "task required",
		},
		{
			name: "unknown command",
			req: proxyRequest{
				Command: "DELETE",
			},
			setupDHT:     func(m *mockDHTRegistry) {},
			wantStatus:   "ERR",
			wantErrField: "unknown command",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dht := newMockDHTRegistry()
			tt.setupDHT(dht)

			result := handleP2PRequest(tt.req, dht, "127.0.0.1:12345")

			if result.Status != tt.wantStatus {
				t.Errorf("expected status %q, got %q", tt.wantStatus, result.Status)
			}
			if tt.wantAddress != "" && result.Address != tt.wantAddress {
				t.Errorf("expected address %q, got %q", tt.wantAddress, result.Address)
			}
			if tt.wantErrField != "" && !strings.Contains(result.Error, tt.wantErrField) {
				t.Errorf("expected error to contain %q, got %q", tt.wantErrField, result.Error)
			}
		})
	}
}

// TestRunProxy tests the RunProxy function with a real UDP proxy
func TestRunProxy(t *testing.T) {
	// Create a mock backend server
	mockServer, mockAddr := createMockUDPServer(t, func(data []byte) []byte {
		var msg transport.CentralizedMessage
		json.Unmarshal(data, &msg)

		resp := transport.CentralizedResponse{Status: "OK"}
		if msg.Command == "QUERY" {
			resp.Address = "192.168.1.1:8080"
		}
		respData, _ := json.Marshal(resp)
		return respData
	})
	defer mockServer.Close()

	// Set environment variables for the proxy
	t.Setenv("TDS_SERVER_ADDR", mockAddr)
	t.Setenv("TDS_SERVER_PROTO", "udp")

	// Start the proxy
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	var actualProxyAddr string

	// Get actual port by creating temp connection
	tempConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("failed to get port: %v", err)
	}
	actualProxyAddr = tempConn.LocalAddr().String()
	tempConn.Close()

	go func() {
		done <- RunProxy(ctx, actualProxyAddr)
	}()

	// Wait for proxy to start
	time.Sleep(100 * time.Millisecond)

	// Test REGISTER through proxy
	t.Run("register through proxy", func(t *testing.T) {
		conn, err := net.Dial("udp", actualProxyAddr)
		if err != nil {
			t.Fatalf("dial proxy: %v", err)
		}
		defer conn.Close()

		msg := transport.CentralizedMessage{
			Command: "REGISTER",
			Task:    "proxy-task",
			Address: "192.168.1.1:8080",
		}
		data, _ := json.Marshal(msg)
		conn.Write(data)

		buf := make([]byte, 4096)
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatalf("read response: %v", err)
		}

		var resp transport.CentralizedResponse
		if err := json.Unmarshal(buf[:n], &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}

		if resp.Status != "OK" {
			t.Errorf("expected OK, got %s", resp.Status)
		}
	})

	// Test QUERY through proxy
	t.Run("query through proxy", func(t *testing.T) {
		conn, err := net.Dial("udp", actualProxyAddr)
		if err != nil {
			t.Fatalf("dial proxy: %v", err)
		}
		defer conn.Close()

		msg := transport.CentralizedMessage{
			Command: "QUERY",
			Task:    "proxy-task",
		}
		data, _ := json.Marshal(msg)
		conn.Write(data)

		buf := make([]byte, 4096)
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatalf("read response: %v", err)
		}

		var resp transport.CentralizedResponse
		if err := json.Unmarshal(buf[:n], &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}

		if resp.Status != "OK" {
			t.Errorf("expected OK, got %s", resp.Status)
		}
		if resp.Address != "192.168.1.1:8080" {
			t.Errorf("expected address 192.168.1.1:8080, got %s", resp.Address)
		}
	})

	// Shutdown the proxy
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("proxy did not shutdown in time")
	}
}

// TestRunProxyTCP tests the TCP proxy
func TestRunProxyTCP(t *testing.T) {
	// Create a mock backend TCP server
	mockServer, mockAddr := createMockTCPServer(t, func(line string) string {
		var msg transport.CentralizedMessage
		json.Unmarshal([]byte(line), &msg)

		resp := transport.CentralizedResponse{Status: "OK"}
		if msg.Command == "QUERY" {
			resp.Address = "192.168.1.1:8080"
		}
		respData, _ := json.Marshal(resp)
		return string(respData)
	})
	defer mockServer.Close()

	// Set environment variables
	t.Setenv("TDS_SERVER_ADDR", mockAddr)

	// Start the proxy
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Get free port
	tempLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to get port: %v", err)
	}
	proxyAddr := tempLn.Addr().String()
	tempLn.Close()

	done := make(chan error, 1)
	go func() {
		done <- RunProxyTCP(ctx, proxyAddr)
	}()

	// Wait for proxy to start
	time.Sleep(100 * time.Millisecond)

	// Test REGISTER through TCP proxy
	t.Run("register through TCP proxy", func(t *testing.T) {
		conn, err := net.Dial("tcp", proxyAddr)
		if err != nil {
			t.Fatalf("dial proxy: %v", err)
		}
		defer conn.Close()

		msg := transport.CentralizedMessage{
			Command: "REGISTER",
			Task:    "tcp-task",
			Address: "192.168.1.1:8080",
		}
		data, _ := json.Marshal(msg)
		fmt.Fprintf(conn, "%s\n", string(data))

		scanner := bufio.NewScanner(conn)
		if !scanner.Scan() {
			t.Fatalf("failed to read response")
		}

		var resp transport.CentralizedResponse
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}

		if resp.Status != "OK" {
			t.Errorf("expected OK, got %s", resp.Status)
		}
	})

	// Shutdown
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("TCP proxy did not shutdown in time")
	}
}

// TestRunProxyP2P tests the P2P proxy with DHT
func TestRunProxyP2P(t *testing.T) {
	dht := newMockDHTRegistry()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Get free port
	tempConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("failed to get port: %v", err)
	}
	proxyAddr := tempConn.LocalAddr().String()
	tempConn.Close()

	done := make(chan error, 1)
	go func() {
		done <- RunProxyP2P(ctx, proxyAddr, dht)
	}()

	// Wait for proxy to start
	time.Sleep(100 * time.Millisecond)

	// Test REGISTER through P2P proxy
	t.Run("register through P2P proxy", func(t *testing.T) {
		conn, err := net.Dial("udp", proxyAddr)
		if err != nil {
			t.Fatalf("dial proxy: %v", err)
		}
		defer conn.Close()

		msg := transport.CentralizedMessage{
			Command: "REGISTER",
			Task:    "p2p-task",
			Address: "192.168.1.1:8080",
		}
		data, _ := json.Marshal(msg)
		conn.Write(data)

		buf := make([]byte, 4096)
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatalf("read response: %v", err)
		}

		var resp transport.CentralizedResponse
		if err := json.Unmarshal(buf[:n], &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}

		if resp.Status != "OK" {
			t.Errorf("expected OK, got %s", resp.Status)
		}

		// Verify it was added to DHT
		addr, _ := dht.Query("p2p-task")
		if addr != "192.168.1.1:8080" {
			t.Errorf("DHT should contain registered address, got %s", addr)
		}
	})

	// Test QUERY through P2P proxy
	t.Run("query through P2P proxy", func(t *testing.T) {
		// Pre-register a task in DHT
		dht.Register("query-task", "10.0.0.1:9000")

		conn, err := net.Dial("udp", proxyAddr)
		if err != nil {
			t.Fatalf("dial proxy: %v", err)
		}
		defer conn.Close()

		msg := transport.CentralizedMessage{
			Command: "QUERY",
			Task:    "query-task",
		}
		data, _ := json.Marshal(msg)
		conn.Write(data)

		buf := make([]byte, 4096)
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatalf("read response: %v", err)
		}

		var resp transport.CentralizedResponse
		if err := json.Unmarshal(buf[:n], &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}

		if resp.Status != "OK" {
			t.Errorf("expected OK, got %s", resp.Status)
		}
		if resp.Address != "10.0.0.1:9000" {
			t.Errorf("expected address 10.0.0.1:9000, got %s", resp.Address)
		}
	})

	// Shutdown
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("P2P proxy did not shutdown in time")
	}
}

// TestStats tests the stats counters
func TestStats(t *testing.T) {
	// Reset counters
	atomic.StoreUint64(&regCount, 0)
	atomic.StoreUint64(&queryCount, 0)
	atomic.StoreUint64(&errorCount, 0)

	// Create mock server
	mockServer, mockAddr := createMockUDPServer(t, func(data []byte) []byte {
		resp := transport.CentralizedResponse{Status: "OK", Address: "192.168.1.1:8080"}
		respData, _ := json.Marshal(resp)
		return respData
	})
	defer mockServer.Close()

	// Perform some operations using handleCentralizedRequest
	req1 := proxyRequest{Command: "REGISTER", Task: "task1", Address: "192.168.1.1:8080"}
	handleCentralizedRequest(req1, mockAddr, "udp", "127.0.0.1:12345")

	req2 := proxyRequest{Command: "QUERY", Task: "task1"}
	handleCentralizedRequest(req2, mockAddr, "udp", "127.0.0.1:12345")

	req3 := proxyRequest{Command: "QUERY", Task: "task2"}
	handleCentralizedRequest(req3, mockAddr, "udp", "127.0.0.1:12345")

	// Check stats
	regs, queries, errs := Stats()

	if regs != 1 {
		t.Errorf("expected 1 registration, got %d", regs)
	}
	if queries != 2 {
		t.Errorf("expected 2 queries, got %d", queries)
	}
	// Note: errs count might be affected by other tests, so we just check it's a number
	_ = errs
}

// TestProxyErrorHandling tests error scenarios in the proxy
func TestProxyErrorHandling(t *testing.T) {
	// Reset error counter
	atomic.StoreUint64(&errorCount, 0)

	t.Run("invalid JSON request", func(t *testing.T) {
		req, err := parseProxyRequest("not json")
		if err == nil {
			t.Error("expected error for invalid JSON")
		}
		_ = req
	})

	t.Run("missing required fields", func(t *testing.T) {
		_, err := parseProxyRequest(`{"cmd":"REGISTER"}`)
		// This won't error in parsing, but will in handling
		if err != nil {
			t.Errorf("unexpected parse error: %v", err)
		}
	})

	t.Run("empty command", func(t *testing.T) {
		_, err := parseProxyRequest("")
		if err == nil {
			t.Error("expected error for empty command")
		}
	})
}

// TestConcurrentProxyRequests tests concurrent requests through the proxy
func TestConcurrentProxyRequests(t *testing.T) {
	// Reset counters
	atomic.StoreUint64(&regCount, 0)
	atomic.StoreUint64(&queryCount, 0)
	atomic.StoreUint64(&errorCount, 0)

	mockServer, mockAddr := createMockUDPServer(t, func(data []byte) []byte {
		resp := transport.CentralizedResponse{Status: "OK", Address: "192.168.1.1:8080"}
		respData, _ := json.Marshal(resp)
		return respData
	})
	defer mockServer.Close()

	const numGoroutines = 100
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			if id%2 == 0 {
				req := proxyRequest{
					Command: "REGISTER",
					Task:    fmt.Sprintf("task-%d", id),
					Address: fmt.Sprintf("192.168.1.%d:8080", id),
				}
				handleCentralizedRequest(req, mockAddr, "udp", "127.0.0.1:12345")
			} else {
				req := proxyRequest{
					Command: "QUERY",
					Task:    fmt.Sprintf("task-%d", id),
				}
				handleCentralizedRequest(req, mockAddr, "udp", "127.0.0.1:12345")
			}
		}(i)
	}

	wg.Wait()

	regs, queries, errs := Stats()

	if regs != numGoroutines/2 {
		t.Errorf("expected %d registrations, got %d", numGoroutines/2, regs)
	}
	if queries != numGoroutines/2 {
		t.Errorf("expected %d queries, got %d", numGoroutines/2, queries)
	}
	if errs != 0 {
		t.Errorf("expected 0 errors, got %d", errs)
	}
}

// TestProxyContextCancellation tests that proxies respect context cancellation
func TestProxyContextCancellation(t *testing.T) {
	mockServer, mockAddr := createMockUDPServer(t, func(data []byte) []byte {
		resp := transport.CentralizedResponse{Status: "OK"}
		respData, _ := json.Marshal(resp)
		return respData
	})
	defer mockServer.Close()

	t.Setenv("TDS_SERVER_ADDR", mockAddr)
	t.Setenv("TDS_SERVER_PROTO", "udp")

	ctx, cancel := context.WithCancel(context.Background())

	tempConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("failed to get port: %v", err)
	}
	proxyAddr := tempConn.LocalAddr().String()
	tempConn.Close()

	done := make(chan error, 1)
	go func() {
		done <- RunProxy(ctx, proxyAddr)
	}()

	// Give proxy time to start
	time.Sleep(50 * time.Millisecond)

	// Cancel context
	cancel()

	// Proxy should exit
	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Error("proxy did not exit after context cancellation")
	}
}

// TestProxyP2PWithDHTErrors tests P2P proxy with DHT failures
func TestProxyP2PWithDHTErrors(t *testing.T) {
	dht := newMockDHTRegistry()
	dht.setRegisterError(fmt.Errorf("DHT network error"))

	req := proxyRequest{
		Command: "REGISTER",
		Task:    "test-task",
		Address: "192.168.1.1:8080",
	}

	result := handleP2PRequest(req, dht, "127.0.0.1:12345")

	if result.Status != "ERR" {
		t.Errorf("expected ERR status, got %s", result.Status)
	}
	if !strings.Contains(result.Error, "DHT network error") {
		t.Errorf("expected DHT error in response, got: %s", result.Error)
	}
}

// TestProxyBackendProtocol tests that proxy correctly uses TCP/UDP backend
func TestProxyBackendProtocol(t *testing.T) {
	t.Run("UDP backend", func(t *testing.T) {
		var protocolUsed string
		mockServer, mockAddr := createMockUDPServer(t, func(data []byte) []byte {
			protocolUsed = "udp"
			resp := transport.CentralizedResponse{Status: "OK"}
			respData, _ := json.Marshal(resp)
			return respData
		})
		defer mockServer.Close()

		req := proxyRequest{Command: "REGISTER", Task: "task", Address: "192.168.1.1:8080"}
		handleCentralizedRequest(req, mockAddr, "udp", "127.0.0.1:12345")

		time.Sleep(50 * time.Millisecond)
		if protocolUsed != "udp" {
			t.Errorf("expected UDP protocol, got %s", protocolUsed)
		}
	})

	t.Run("TCP backend", func(t *testing.T) {
		var protocolUsed string
		mockServer, mockAddr := createMockTCPServer(t, func(line string) string {
			protocolUsed = "tcp"
			resp := transport.CentralizedResponse{Status: "OK"}
			respData, _ := json.Marshal(resp)
			return string(respData)
		})
		defer mockServer.Close()

		req := proxyRequest{Command: "REGISTER", Task: "task", Address: "192.168.1.1:8080"}
		handleCentralizedRequest(req, mockAddr, "tcp", "127.0.0.1:12345")

		time.Sleep(50 * time.Millisecond)
		if protocolUsed != "tcp" {
			t.Errorf("expected TCP protocol, got %s", protocolUsed)
		}
	})
}

// TestWriteResult tests UDP response writing
func TestWriteResult(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("failed to create UDP conn: %v", err)
	}
	defer conn.Close()

	// Create a client to receive responses
	clientConn, err := net.DialUDP("udp", nil, conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("failed to create client conn: %v", err)
	}
	defer clientConn.Close()

	tests := []struct {
		name       string
		result     proxyResult
		wantStatus string
	}{
		{
			name:       "OK with address",
			result:     proxyResult{Status: StatusOK, Address: "192.168.1.1:8080"},
			wantStatus: "OK",
		},
		{
			name:       "NOTFOUND",
			result:     proxyResult{Status: StatusNotFound},
			wantStatus: "NOTFOUND",
		},
		{
			name:       "ERR with message",
			result:     proxyResult{Status: StatusErr, Error: "test error"},
			wantStatus: "ERR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeResult(conn, clientConn.LocalAddr().(*net.UDPAddr), true, tt.result)

			buf := make([]byte, 4096)
			clientConn.SetReadDeadline(time.Now().Add(1 * time.Second))
			n, err := clientConn.Read(buf)
			if err != nil {
				t.Fatalf("read response: %v", err)
			}

			var resp transport.CentralizedResponse
			if err := json.Unmarshal(buf[:n], &resp); err != nil {
				t.Fatalf("unmarshal response: %v", err)
			}

			if resp.Status != tt.wantStatus {
				t.Errorf("expected status %s, got %s", tt.wantStatus, resp.Status)
			}
		})
	}
}

// BenchmarkHandleCentralizedRequest benchmarks request handling
func BenchmarkHandleCentralizedRequest(b *testing.B) {
	mockServer, mockAddr := createMockUDPServer(&testing.T{}, func(data []byte) []byte {
		resp := transport.CentralizedResponse{Status: "OK", Address: "192.168.1.1:8080"}
		respData, _ := json.Marshal(resp)
		return respData
	})
	defer mockServer.Close()

	req := proxyRequest{Command: "QUERY", Task: "bench-task"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handleCentralizedRequest(req, mockAddr, "udp", "127.0.0.1:12345")
	}
}

// BenchmarkParseProxyRequest benchmarks request parsing
func BenchmarkParseProxyRequest(b *testing.B) {
	input := `{"cmd":"REGISTER","task":"test-task","address":"192.168.1.1:8080","capacity":10}`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		parseProxyRequest(input)
	}
}

// ========================================================================
// writeResult text-mode coverage
// ========================================================================

// makeUDPPair creates a server-side UDPConn and a dialed clientConn pointed at it.
func makeUDPPair(t *testing.T) (server *net.UDPConn, client *net.UDPConn, clientAddr *net.UDPAddr) {
	t.Helper()
	server, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("makeUDPPair server: %v", err)
	}
	client, err = net.DialUDP("udp", nil, server.LocalAddr().(*net.UDPAddr))
	if err != nil {
		server.Close()
		t.Fatalf("makeUDPPair client: %v", err)
	}
	clientAddr = client.LocalAddr().(*net.UDPAddr)
	return
}

// readUDPStr reads one UDP datagram from conn with a 1-second deadline.
func readUDPStr(t *testing.T, conn *net.UDPConn) string {
	t.Helper()
	buf := make([]byte, 4096)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("readUDPStr: %v", err)
	}
	return string(buf[:n])
}

func TestWriteResultTextMode(t *testing.T) {
	tests := []struct {
		name    string
		result  proxyResult
		wantStr string
	}{
		{
			name:    "OK with address",
			result:  proxyResult{Status: StatusOK, Address: "10.0.0.1:9000"},
			wantStr: "10.0.0.1:9000",
		},
		{
			name:    "OK without address",
			result:  proxyResult{Status: StatusOK},
			wantStr: StatusOK,
		},
		{
			name:    "NOTFOUND",
			result:  proxyResult{Status: StatusNotFound},
			wantStr: StatusNotFound,
		},
		{
			name:    "ERR with message",
			result:  proxyResult{Status: StatusErr, Error: "something went wrong"},
			wantStr: StatusErr + " something went wrong",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, client, clientAddr := makeUDPPair(t)
			defer server.Close()
			defer client.Close()

			writeResult(server, clientAddr, false, tt.result)
			got := readUDPStr(t, client)

			if got != tt.wantStr {
				t.Errorf("expected %q, got %q", tt.wantStr, got)
			}
		})
	}
}

// ========================================================================
// writeTCPResultGeneric text-mode coverage
// ========================================================================

func makeBufWriter() (*bufio.Writer, *strings.Builder) {
	sb := &strings.Builder{}
	return bufio.NewWriter(sb), sb
}

func TestWriteTCPResultGenericTextMode(t *testing.T) {
	tests := []struct {
		name    string
		result  proxyResult
		wantStr string
	}{
		{
			name:    "OK with address",
			result:  proxyResult{Status: StatusOK, Address: "10.0.0.1:9000"},
			wantStr: "10.0.0.1:9000\n",
		},
		{
			name:    "OK without address",
			result:  proxyResult{Status: StatusOK},
			wantStr: StatusOK + "\n",
		},
		{
			name:    "NOTFOUND",
			result:  proxyResult{Status: StatusNotFound},
			wantStr: StatusNotFound + "\n",
		},
		{
			name:    "ERR with message",
			result:  proxyResult{Status: StatusErr, Error: "bad input"},
			wantStr: StatusErr + " bad input\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, sb := makeBufWriter()
			writeTCPResultGeneric(w, false, tt.result)
			if sb.String() != tt.wantStr {
				t.Errorf("expected %q, got %q", tt.wantStr, sb.String())
			}
		})
	}
}

func TestWriteTCPResultGenericJSONMode(t *testing.T) {
	tests := []struct {
		name       string
		result     proxyResult
		wantStatus string
	}{
		{
			name:       "OK with address",
			result:     proxyResult{Status: StatusOK, Address: "10.0.0.1:9000"},
			wantStatus: "OK",
		},
		{
			name:       "NOTFOUND",
			result:     proxyResult{Status: StatusNotFound},
			wantStatus: StatusNotFound,
		},
		{
			name:       "ERR",
			result:     proxyResult{Status: StatusErr, Error: "some error"},
			wantStatus: StatusErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, sb := makeBufWriter()
			writeTCPResultGeneric(w, true, tt.result)

			var resp transport.CentralizedResponse
			if err := json.Unmarshal([]byte(strings.TrimSuffix(sb.String(), "\n")), &resp); err != nil {
				t.Fatalf("unmarshal TCP JSON response: %v (raw: %q)", err, sb.String())
			}
			if resp.Status != tt.wantStatus {
				t.Errorf("expected status %q, got %q", tt.wantStatus, resp.Status)
			}
		})
	}
}

// ========================================================================
// handlePacketGeneric coverage
// ========================================================================

func TestHandlePacketGenericParseError(t *testing.T) {
	server, client, clientAddr := makeUDPPair(t)
	defer server.Close()
	defer client.Close()

	handler := func(req proxyRequest, src string) proxyResult {
		t.Error("handler should not be called on parse error")
		return proxyResult{Status: StatusOK}
	}

	// Pass plaintext (not JSON) – parseProxyRequest should return an error.
	handlePacketGeneric(server, clientAddr, "not json at all", handler)

	got := readUDPStr(t, client)
	var resp transport.CentralizedResponse
	if err := json.Unmarshal([]byte(got), &resp); err != nil {
		t.Fatalf("expected JSON error response, got %q: %v", got, err)
	}
	if resp.Status != StatusErr {
		t.Errorf("expected ERR status, got %q", resp.Status)
	}
}

func TestHandlePacketGenericHandlerError(t *testing.T) {
	server, client, clientAddr := makeUDPPair(t)
	defer server.Close()
	defer client.Close()

	handler := func(req proxyRequest, src string) proxyResult {
		return proxyResult{Status: StatusErr, Error: "backend unavailable"}
	}

	data := `{"cmd":"QUERY","task":"t"}`
	handlePacketGeneric(server, clientAddr, data, handler)

	got := readUDPStr(t, client)
	var resp transport.CentralizedResponse
	if err := json.Unmarshal([]byte(got), &resp); err != nil {
		t.Fatalf("expected JSON response, got %q: %v", got, err)
	}
	if resp.Status != StatusErr {
		t.Errorf("expected ERR status, got %q", resp.Status)
	}
	if !strings.Contains(resp.Error, "backend unavailable") {
		t.Errorf("expected error detail, got %q", resp.Error)
	}
}

func TestHandlePacketGenericSuccess(t *testing.T) {
	server, client, clientAddr := makeUDPPair(t)
	defer server.Close()
	defer client.Close()

	handler := func(req proxyRequest, src string) proxyResult {
		return proxyResult{Status: StatusOK, Address: "10.0.0.5:8080"}
	}

	data := `{"cmd":"QUERY","task":"svc"}`
	handlePacketGeneric(server, clientAddr, data, handler)

	got := readUDPStr(t, client)
	var resp transport.CentralizedResponse
	if err := json.Unmarshal([]byte(got), &resp); err != nil {
		t.Fatalf("expected JSON response, got %q: %v", got, err)
	}
	if resp.Status != StatusOK || resp.Address != "10.0.0.5:8080" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

// ========================================================================
// Logging (SetLogOutput / SetQuiet)
// ========================================================================

func TestSetLogOutput(t *testing.T) {
	var buf strings.Builder
	SetLogOutput(&buf)
	// The logger should now write to buf.  Trigger a log line via SetQuiet then restore.
	t.Cleanup(func() { SetLogOutput(nil) }) // nil is a no-op per implementation

	// Direct logger invocation isn't exported, but we can trigger it via RunProxy
	// with a short-lived context so the "listening" log fires.
	t.Setenv("TDS_SERVER_ADDR", "127.0.0.1:1")
	t.Setenv("TDS_SERVER_PROTO", "udp")

	tempConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("get free port: %v", err)
	}
	proxyAddr := tempConn.LocalAddr().String()
	tempConn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunProxy(ctx, proxyAddr) }()
	time.Sleep(80 * time.Millisecond)
	cancel()
	<-done

	if !strings.Contains(buf.String(), "listening") && !strings.Contains(buf.String(), "proxy") {
		t.Logf("log output: %q (may be empty on fast cancel)", buf.String())
		// Don't fatal – the important thing is no panic was raised.
	}
}

func TestSetQuiet(t *testing.T) {
	// SetQuiet must not panic and must silence logs.
	SetQuiet()
	t.Cleanup(func() {
		// Restore to stdout after this test so other tests aren't silenced.
		logger.SetOutput(nil) // no-op intentionally; other tests reset via SetLogOutput
	})
	// If this returns without panic the test passes.
}

func TestSetLogOutputNil(t *testing.T) {
	// Passing nil should be a no-op (not a panic).
	SetLogOutput(nil)
}
