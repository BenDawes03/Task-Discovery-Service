package transport

import (
	"bufio"
	"context"
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

	"tds/pkg/registry"
)

type fakeRegistry struct {
	mu sync.Mutex

	registeredTask     string
	registeredAddress  string
	registeredCapacity int

	queryAddress string
	queryError   error
}

func (f *fakeRegistry) Register(taskName, address string) {
	f.RegisterWithCapacity(taskName, address, 1)
}

func (f *fakeRegistry) RegisterWithCapacity(taskName, address string, capacity int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registeredTask = taskName
	f.registeredAddress = address
	f.registeredCapacity = capacity
}

func (f *fakeRegistry) GetService(taskName string) (string, error) {
	return f.GetServiceForRequestor(taskName, nil)
}

func (f *fakeRegistry) GetServiceForRequestor(taskName string, requestorIP net.IP) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.queryAddress, f.queryError
}

func (f *fakeRegistry) Cleanup(timeout time.Duration) int {
	return 0
}

func (f *fakeRegistry) ListServices() map[string][]registry.ServiceEntry {
	return map[string][]registry.ServiceEntry{}
}

func (f *fakeRegistry) GetStats() registry.Stats {
	return registry.Stats{}
}

func (f *fakeRegistry) snapshotRegister() (string, string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.registeredTask, f.registeredAddress, f.registeredCapacity
}

func readUDPResponse(t *testing.T, conn *net.UDPConn) CentralizedResponse {
	t.Helper()
	buf := make([]byte, 4096)
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline failed: %v", err)
	}
	n, _, err := conn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("ReadFromUDP failed: %v", err)
	}
	var resp CentralizedResponse
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		t.Fatalf("unmarshal UDP response failed: %v", err)
	}
	return resp
}

func sendAndHandleUDP(t *testing.T, reg registry.Registry, request []byte) CentralizedResponse {
	t.Helper()
	serverConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP server failed: %v", err)
	}
	defer serverConn.Close()

	clientConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP client failed: %v", err)
	}
	defer clientConn.Close()

	remote := clientConn.LocalAddr().(*net.UDPAddr)
	handleUDPRequest(serverConn, reg, request, remote, nil)
	return readUDPResponse(t, clientConn)
}

func TestHandleUDPRequestRegisterCapacityAndDefaults(t *testing.T) {
	fake := &fakeRegistry{}

	resp := sendAndHandleUDP(t, fake, []byte(`{"cmd":"REGISTER","task":"ticket","address":"10.0.0.1:7000","capacity":4}`))
	if resp.Status != "OK" {
		t.Fatalf("expected OK, got %+v", resp)
	}

	task, addr, cap := fake.snapshotRegister()
	if task != "ticket" || addr != "10.0.0.1:7000" || cap != 4 {
		t.Fatalf("unexpected register values task=%q addr=%q cap=%d", task, addr, cap)
	}

	resp = sendAndHandleUDP(t, fake, []byte(`{"cmd":"REGISTER","task":"ticket","address":"10.0.0.2:7000"}`))
	if resp.Status != "OK" {
		t.Fatalf("expected OK for default capacity register, got %+v", resp)
	}
	_, _, cap = fake.snapshotRegister()
	if cap != 1 {
		t.Fatalf("expected default capacity 1, got %d", cap)
	}
}

func TestHandleUDPRequestQueryStatusMapping(t *testing.T) {
	fake := &fakeRegistry{}

	fake.queryAddress = "10.0.0.9:8000"
	resp := sendAndHandleUDP(t, fake, []byte(`{"cmd":"QUERY","task":"ticket"}`))
	if resp.Status != "OK" || resp.Address != "10.0.0.9:8000" {
		t.Fatalf("expected OK with address, got %+v", resp)
	}

	fake.queryAddress = ""
	fake.queryError = registry.ErrNotFound
	resp = sendAndHandleUDP(t, fake, []byte(`{"cmd":"QUERY","task":"ticket"}`))
	if resp.Status != "NOTFOUND" {
		t.Fatalf("expected NOTFOUND, got %+v", resp)
	}

	fake.queryAddress = "10.0.0.9:8000"
	fake.queryError = registry.ErrNoAllowedService
	resp = sendAndHandleUDP(t, fake, []byte(`{"cmd":"QUERY","task":"ticket"}`))
	if resp.Status != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN, got %+v", resp)
	}
}

func TestHandleUDPRequestProtocolErrors(t *testing.T) {
	fake := &fakeRegistry{}

	resp := sendAndHandleUDP(t, fake, []byte(`{"cmd":"REGISTER","task":"x"}`))
	if resp.Status != "ERR" || !strings.Contains(resp.Error, "task and address required") {
		t.Fatalf("expected register validation error, got %+v", resp)
	}

	resp = sendAndHandleUDP(t, fake, []byte(`{"cmd":"QUERY"}`))
	if resp.Status != "ERR" || !strings.Contains(resp.Error, "task required") {
		t.Fatalf("expected query validation error, got %+v", resp)
	}

	resp = sendAndHandleUDP(t, fake, []byte(`{"cmd":"NOPE"}`))
	if resp.Status != "ERR" || !strings.Contains(resp.Error, "unknown command") {
		t.Fatalf("expected unknown command error, got %+v", resp)
	}

	resp = sendAndHandleUDP(t, fake, []byte(`{not json}`))
	if resp.Status != "ERR" || !strings.Contains(resp.Error, "invalid JSON") {
		t.Fatalf("expected invalid JSON error, got %+v", resp)
	}

	resp = sendAndHandleUDP(t, fake, []byte(`{"cmd":"REGISTER","task":"   ","address":"10.0.0.1:7000"}`))
	if resp.Status != "ERR" || !strings.Contains(resp.Error, "task and address required") {
		t.Fatalf("expected whitespace task validation error, got %+v", resp)
	}

	resp = sendAndHandleUDP(t, fake, []byte(`{"cmd":"REGISTER","task":"ticket","address":"   "}`))
	if resp.Status != "ERR" || !strings.Contains(resp.Error, "task and address required") {
		t.Fatalf("expected whitespace address validation error, got %+v", resp)
	}

	resp = sendAndHandleUDP(t, fake, []byte(`{"cmd":"QUERY","task":"   "}`))
	if resp.Status != "ERR" || !strings.Contains(resp.Error, "task required") {
		t.Fatalf("expected whitespace query task validation error, got %+v", resp)
	}

	resp = sendAndHandleUDP(t, fake, []byte(`{"cmd":"HEARTBEAT","task":"ticket","address":"10.0.0.1:7000"}`))
	if resp.Status != "ERR" || !strings.Contains(resp.Error, "unknown command") {
		t.Fatalf("expected unknown HEARTBEAT command error, got %+v", resp)
	}

	resp = sendAndHandleUDP(t, fake, []byte(`{"cmd":"REGISTER","task":123,"address":"10.0.0.1:7000"}`))
	if resp.Status != "ERR" || !strings.Contains(resp.Error, "invalid JSON") {
		t.Fatalf("expected malformed register payload error, got %+v", resp)
	}

	resp = sendAndHandleUDP(t, fake, []byte(`{"cmd":"QUERY","task":123}`))
	if resp.Status != "ERR" || !strings.Contains(resp.Error, "invalid JSON") {
		t.Fatalf("expected malformed query payload error, got %+v", resp)
	}
}

func runTCPConversation(t *testing.T, reg registry.Registry, lines ...string) []CentralizedResponse {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	go handleTCPConn(serverConn, reg, nil)

	writer := bufio.NewWriter(clientConn)
	reader := bufio.NewReader(clientConn)

	responses := make([]CentralizedResponse, 0, len(lines))
	for _, line := range lines {
		if _, err := writer.WriteString(line + "\n"); err != nil {
			t.Fatalf("write TCP request failed: %v", err)
		}
		if err := writer.Flush(); err != nil {
			t.Fatalf("flush TCP request failed: %v", err)
		}

		respLine, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read TCP response failed: %v", err)
		}
		var resp CentralizedResponse
		if err := json.Unmarshal(respLine, &resp); err != nil {
			t.Fatalf("unmarshal TCP response failed: %v", err)
		}
		responses = append(responses, resp)
	}

	return responses
}

func TestHandleTCPConnRegisterAndQueryFlow(t *testing.T) {
	fake := &fakeRegistry{queryAddress: "10.2.2.2:9000"}
	responses := runTCPConversation(
		t,
		fake,
		`{"cmd":"REGISTER","task":"ticket","address":"10.0.0.5:7000","capacity":3}`,
		`{"cmd":"QUERY","task":"ticket"}`,
	)

	if len(responses) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(responses))
	}
	if responses[0].Status != "OK" {
		t.Fatalf("expected first response OK, got %+v", responses[0])
	}
	if responses[1].Status != "OK" || responses[1].Address != "10.2.2.2:9000" {
		t.Fatalf("expected second response OK/address, got %+v", responses[1])
	}

	task, addr, cap := fake.snapshotRegister()
	if task != "ticket" || addr != "10.0.0.5:7000" || cap != 3 {
		t.Fatalf("unexpected register values task=%q addr=%q cap=%d", task, addr, cap)
	}
}

func TestHandleTCPConnQueryStatusMappingAndErrors(t *testing.T) {
	fake := &fakeRegistry{queryError: registry.ErrNotFound}
	responses := runTCPConversation(
		t,
		fake,
		`{"cmd":"QUERY","task":"x"}`,
		`{"cmd":"QUERY"}`,
		`{"cmd":"QUERY","task":"   "}`,
		`{"cmd":"REGISTER","task":"   ","address":"10.0.0.1:7000"}`,
		`{"cmd":"REGISTER","task":"ticket","address":"   "}`,
		`{"cmd":"NOPE"}`,
		`{"cmd":"HEARTBEAT","task":"ticket","address":"10.0.0.1:7000"}`,
		`{"cmd":"REGISTER","task":123,"address":"10.0.0.1:7000"}`,
		`{"cmd":"QUERY","task":123}`,
		`{not json}`,
	)

	if responses[0].Status != "NOTFOUND" {
		t.Fatalf("expected NOTFOUND, got %+v", responses[0])
	}
	if responses[1].Status != "ERR" || !strings.Contains(responses[1].Error, "task required") {
		t.Fatalf("expected task required ERR, got %+v", responses[1])
	}
	if responses[2].Status != "ERR" || !strings.Contains(responses[2].Error, "task required") {
		t.Fatalf("expected whitespace task ERR, got %+v", responses[2])
	}
	if responses[3].Status != "ERR" || !strings.Contains(responses[3].Error, "task and address required") {
		t.Fatalf("expected register task validation ERR, got %+v", responses[3])
	}
	if responses[4].Status != "ERR" || !strings.Contains(responses[4].Error, "task and address required") {
		t.Fatalf("expected register address validation ERR, got %+v", responses[4])
	}
	if responses[5].Status != "ERR" || !strings.Contains(responses[5].Error, "unknown command") {
		t.Fatalf("expected unknown command ERR, got %+v", responses[5])
	}
	if responses[6].Status != "ERR" || !strings.Contains(responses[6].Error, "unknown command") {
		t.Fatalf("expected HEARTBEAT unknown command ERR, got %+v", responses[6])
	}
	if responses[7].Status != "ERR" || !strings.Contains(responses[7].Error, "invalid JSON") {
		t.Fatalf("expected malformed register invalid JSON ERR, got %+v", responses[7])
	}
	if responses[8].Status != "ERR" || !strings.Contains(responses[8].Error, "invalid JSON") {
		t.Fatalf("expected malformed query invalid JSON ERR, got %+v", responses[8])
	}
	if responses[9].Status != "ERR" || !strings.Contains(responses[9].Error, "invalid JSON") {
		t.Fatalf("expected invalid JSON ERR, got %+v", responses[9])
	}
}

func TestHandleTCPConnForbiddenMapping(t *testing.T) {
	fake := &fakeRegistry{queryAddress: "10.9.9.9:9000", queryError: registry.ErrNoAllowedService}
	responses := runTCPConversation(t, fake, `{"cmd":"QUERY","task":"blocked"}`)
	if responses[0].Status != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN, got %+v", responses[0])
	}
}

// ========================================================================
// Test Infrastructure Helpers
// ========================================================================

// slowRegistry simulates a slow-processing registry for concurrency tests
type slowRegistry struct {
	fakeRegistry
	delay        time.Duration
	activeCount  int32
	peakCount    int32
	registerCall int32
	queryCall    int32
}

func (s *slowRegistry) RegisterWithCapacity(taskName, address string, capacity int) {
	atomic.AddInt32(&s.registerCall, 1)
	current := atomic.AddInt32(&s.activeCount, 1)
	defer atomic.AddInt32(&s.activeCount, -1)
	
	// Track peak concurrency
	for {
		peak := atomic.LoadInt32(&s.peakCount)
		if current <= peak {
			break
		}
		if atomic.CompareAndSwapInt32(&s.peakCount, peak, current) {
			break
		}
	}
	
	time.Sleep(s.delay)
	s.fakeRegistry.RegisterWithCapacity(taskName, address, capacity)
}

func (s *slowRegistry) GetServiceForRequestor(taskName string, requestorIP net.IP) (string, error) {
	atomic.AddInt32(&s.queryCall, 1)
	current := atomic.AddInt32(&s.activeCount, 1)
	defer atomic.AddInt32(&s.activeCount, -1)
	
	for {
		peak := atomic.LoadInt32(&s.peakCount)
		if current <= peak {
			break
		}
		if atomic.CompareAndSwapInt32(&s.peakCount, peak, current) {
			break
		}
	}
	
	time.Sleep(s.delay)
	return s.fakeRegistry.GetServiceForRequestor(taskName, requestorIP)
}

// startTestUDPServer starts a UDP server on a random port and returns the address
func startTestUDPServer(t *testing.T, reg registry.Registry, maxConcurrent int64) (string, func()) {
	t.Helper()
	
	// Find a free port
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	addr := listener.LocalAddr().String()
	listener.Close()
	
	_, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	
	go func() {
		StartUDPServer(reg, port, maxConcurrent, nil)
	}()
	
	time.Sleep(50 * time.Millisecond) // Let server start
	
	return addr, func() {
		// UDP server runs in infinite loop, cleanup is a no-op
		// In production this would need context-based cancellation
	}
}

// startTestTCPServer starts a TCP server on a random port and returns the address
func startTestTCPServer(t *testing.T, reg registry.Registry, maxConcurrent int64) (string, func()) {
	t.Helper()
	
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	addr := listener.Addr().String()
	listener.Close()
	
	_, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	
	go func() {
		StartTCPServer(reg, port, maxConcurrent, nil)
	}()
	
	time.Sleep(50 * time.Millisecond)
	
	return addr, func() {
		// TCP server runs in infinite loop, cleanup is a no-op
		// In production this would need context-based cancellation
	}
}

// sendUDPMessage sends a message and returns the response
func sendUDPMessage(t *testing.T, serverAddr string, msg CentralizedMessage) (CentralizedResponse, error) {
	t.Helper()
	
	conn, err := net.Dial("udp", serverAddr)
	if err != nil {
		return CentralizedResponse{}, err
	}
	defer conn.Close()
	
	data, err := json.Marshal(msg)
	if err != nil {
		return CentralizedResponse{}, err
	}
	
	if _, err := conn.Write(data); err != nil {
		return CentralizedResponse{}, err
	}
	
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return CentralizedResponse{}, err
	}
	
	var resp CentralizedResponse
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		return CentralizedResponse{}, err
	}
	
	return resp, nil
}

// sendTCPMessage sends a message over TCP and returns the response
func sendTCPMessage(t *testing.T, serverAddr string, msg CentralizedMessage) (CentralizedResponse, error) {
	t.Helper()
	
	conn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		return CentralizedResponse{}, err
	}
	defer conn.Close()
	
	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(msg); err != nil {
		return CentralizedResponse{}, err
	}
	
	decoder := json.NewDecoder(conn)
	var resp CentralizedResponse
	if err := decoder.Decode(&resp); err != nil {
		return CentralizedResponse{}, err
	}
	
	return resp, nil
}

// ========================================================================
// Server Lifecycle Tests
// ========================================================================

func TestUDPServerLifecycle(t *testing.T) {
	reg := registry.NewMemoryRegistry()
	addr, stop := startTestUDPServer(t, reg, 100)
	defer stop()
	
	// Register a service
	regMsg := CentralizedMessage{
		Command:  "REGISTER",
		Task:     "ticket",
		Address:  "10.0.0.1:8000",
		Capacity: 5,
	}
	resp, err := sendUDPMessage(t, addr, regMsg)
	if err != nil {
		t.Fatalf("failed to send register: %v", err)
	}
	if resp.Status != "OK" {
		t.Errorf("register failed: %+v", resp)
	}
	
	// Query the service
	queryMsg := CentralizedMessage{
		Command: "QUERY",
		Task:    "ticket",
	}
	resp, err = sendUDPMessage(t, addr, queryMsg)
	if err != nil {
		t.Fatalf("failed to send query: %v", err)
	}
	if resp.Status != "OK" || resp.Address != "10.0.0.1:8000" {
		t.Errorf("query failed: %+v", resp)
	}
}

func TestTCPServerLifecycle(t *testing.T) {
	reg := registry.NewMemoryRegistry()
	addr, stop := startTestTCPServer(t, reg, 100)
	defer stop()
	
	// Register a service
	regMsg := CentralizedMessage{
		Command:  "REGISTER",
		Task:     "ticket",
		Address:  "10.0.0.2:9000",
		Capacity: 3,
	}
	resp, err := sendTCPMessage(t, addr, regMsg)
	if err != nil {
		t.Fatalf("failed to send register: %v", err)
	}
	if resp.Status != "OK" {
		t.Errorf("register failed: %+v", resp)
	}
	
	// Query the service
	queryMsg := CentralizedMessage{
		Command: "QUERY",
		Task:    "ticket",
	}
	resp, err = sendTCPMessage(t, addr, queryMsg)
	if err != nil {
		t.Fatalf("failed to send query: %v", err)
	}
	if resp.Status != "OK" || resp.Address != "10.0.0.2:9000" {
		t.Errorf("query failed: %+v", resp)
	}
}

func TestTCPServerPersistentConnection(t *testing.T) {
	reg := registry.NewMemoryRegistry()
	addr, stop := startTestTCPServer(t, reg, 100)
	defer stop()
	
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()
	
	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)
	
	// Send multiple commands on same connection
	for i := 0; i < 5; i++ {
		regMsg := CentralizedMessage{
			Command:  "REGISTER",
			Task:     fmt.Sprintf("task%d", i),
			Address:  fmt.Sprintf("10.0.0.%d:8000", i),
			Capacity: i + 1,
		}
		if err := encoder.Encode(regMsg); err != nil {
			t.Fatalf("encode failed: %v", err)
		}
		
		var resp CentralizedResponse
		if err := decoder.Decode(&resp); err != nil {
			t.Fatalf("decode failed: %v", err)
		}
		if resp.Status != "OK" {
			t.Errorf("request %d failed: %+v", i, resp)
		}
	}
}

// ========================================================================
// Concurrency Limit Tests
// ========================================================================

func TestUDPServerConcurrencyLimit(t *testing.T) {
	slow := &slowRegistry{
		delay: 100 * time.Millisecond,
	}
	slow.queryAddress = "10.0.0.1:8000"
	
	// Start with limit of 10 concurrent handlers
	addr, stop := startTestUDPServer(t, slow, 10)
	defer stop()
	
	// Send 50 requests rapidly
	var wg sync.WaitGroup
	successCount := int32(0)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			msg := CentralizedMessage{Command: "QUERY", Task: "test"}
			_, err := sendUDPMessage(t, addr, msg)
			if err == nil {
				atomic.AddInt32(&successCount, 1)
			}
		}()
	}
	wg.Wait()
	
	// Should have received most responses (some may be dropped due to overload)
	if successCount < 40 {
		t.Errorf("too many dropped requests: only %d/50 succeeded", successCount)
	}
	
	// Peak concurrency should not exceed limit significantly
	peak := atomic.LoadInt32(&slow.peakCount)
	if peak > 15 { // Allow small overshoot due to timing
		t.Errorf("concurrency limit violated: peak=%d, limit=10", peak)
	}
	
	t.Logf("Processed %d/50 requests with peak concurrency %d", successCount, peak)
}

func TestTCPServerConnectionLimit(t *testing.T) {
	slow := &slowRegistry{
		delay: 200 * time.Millisecond,
	}
	slow.queryAddress = "10.0.0.1:8000"
	
	// Start with limit of 5 concurrent connections
	addr, stop := startTestTCPServer(t, slow, 5)
	defer stop()
	
	// Open 5 connections and hold them
	conns := make([]net.Conn, 5)
	for i := 0; i < 5; i++ {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatalf("failed to open connection %d: %v", i, err)
		}
		conns[i] = conn
		defer conn.Close()
		
		// Send a slow query on each
		go func(c net.Conn) {
			encoder := json.NewEncoder(c)
			encoder.Encode(CentralizedMessage{Command: "QUERY", Task: "test"})
		}(conn)
	}
	
	time.Sleep(50 * time.Millisecond)
	
	// Try to open 6th connection - should be rejected or timeout quickly
	conn6, err := net.DialTimeout("tcp", addr, 1500*time.Millisecond)
	if err == nil {
		defer conn6.Close()
		// Connection succeeded - it will be accepted after one of the 5 finishes
		// This is acceptable behavior
		t.Logf("6th connection accepted (one of first 5 likely finished)")
	}
	
	// Verify peak concurrency doesn't exceed limit significantly
	peak := atomic.LoadInt32(&slow.peakCount)
	if peak > 7 {
		t.Errorf("concurrency limit violated: peak=%d, limit=5", peak)
	}
	
	t.Logf("Peak concurrency: %d", peak)
}

func TestServerConcurrencyLimitDefaults(t *testing.T) {
	reg := &fakeRegistry{}
	reg.queryAddress = "10.0.0.1:8000"
	
	// Test UDP with 0 (should default to 1000)
	udpAddr, udpStop := startTestUDPServer(t, reg, 0)
	defer udpStop()
	
	resp, err := sendUDPMessage(t, udpAddr, CentralizedMessage{Command: "QUERY", Task: "test"})
	if err != nil {
		t.Fatalf("UDP with default limit failed: %v", err)
	}
	if resp.Status != "OK" {
		t.Errorf("UDP query failed: %+v", resp)
	}
	
	// Test TCP with 0 (should default to 5000)
	tcpAddr, tcpStop := startTestTCPServer(t, reg, 0)
	defer tcpStop()
	
	resp, err = sendTCPMessage(t, tcpAddr, CentralizedMessage{Command: "QUERY", Task: "test"})
	if err != nil {
		t.Fatalf("TCP with default limit failed: %v", err)
	}
	if resp.Status != "OK" {
		t.Errorf("TCP query failed: %+v", resp)
	}
}

// ========================================================================
// Race Condition Tests (run with -race flag)
// ========================================================================

func TestUDPServerConcurrentRequests(t *testing.T) {
	reg := registry.NewMemoryRegistry()
	addr, stop := startTestUDPServer(t, reg, 100)
	defer stop()
	
	var wg sync.WaitGroup
	errors := int32(0)
	
	// Send 100 concurrent REGISTER requests
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			msg := CentralizedMessage{
				Command:  "REGISTER",
				Task:     fmt.Sprintf("task%d", id),
				Address:  fmt.Sprintf("10.0.0.%d:8000", id%256),
				Capacity: id%10 + 1,
			}
			resp, err := sendUDPMessage(t, addr, msg)
			if err != nil || resp.Status != "OK" {
				atomic.AddInt32(&errors, 1)
			}
		}(i)
	}
	wg.Wait()
	
	if errors > 10 {
		t.Errorf("too many errors: %d/100", errors)
	}
	
	// Send 100 concurrent QUERY requests
	errors = 0
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			msg := CentralizedMessage{
				Command: "QUERY",
				Task:    fmt.Sprintf("task%d", id),
			}
			_, err := sendUDPMessage(t, addr, msg)
			if err != nil {
				atomic.AddInt32(&errors, 1)
			}
		}(i)
	}
	wg.Wait()
	
	if errors > 10 {
		t.Errorf("too many query errors: %d/100", errors)
	}
}

func TestTCPServerConcurrentConnections(t *testing.T) {
	reg := registry.NewMemoryRegistry()
	addr, stop := startTestTCPServer(t, reg, 200)
	defer stop()
	
	var wg sync.WaitGroup
	errors := int32(0)
	
	// Open 50 concurrent connections, each sends 10 commands
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(connID int) {
			defer wg.Done()
			
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				atomic.AddInt32(&errors, 1)
				return
			}
			defer conn.Close()
			
			encoder := json.NewEncoder(conn)
			decoder := json.NewDecoder(conn)
			
			for j := 0; j < 10; j++ {
				msg := CentralizedMessage{
					Command:  "REGISTER",
					Task:     fmt.Sprintf("conn%d_task%d", connID, j),
					Address:  fmt.Sprintf("10.0.%d.%d:8000", connID%256, j%256),
					Capacity: j + 1,
				}
				if err := encoder.Encode(msg); err != nil {
					atomic.AddInt32(&errors, 1)
					return
				}
				
				var resp CentralizedResponse
				if err := decoder.Decode(&resp); err != nil {
					atomic.AddInt32(&errors, 1)
					return
				}
				if resp.Status != "OK" {
					atomic.AddInt32(&errors, 1)
				}
			}
		}(i)
	}
	wg.Wait()
	
	if errors > 5 {
		t.Errorf("too many errors: %d", errors)
	}
}

// ========================================================================
// HandleMessage Unit Tests
// ========================================================================

func TestHandleMessageRegister(t *testing.T) {
	reg := &fakeRegistry{}
	msg := CentralizedMessage{
		Command:  "REGISTER",
		Task:     "ticket",
		Address:  "10.0.0.1:8000",
		Capacity: 5,
	}
	
	resp := HandleMessage(reg, msg, nil, &net.UDPAddr{}, nil)
	
	if resp.Status != "OK" {
		t.Errorf("expected OK, got %+v", resp)
	}
	
	task, addr, cap := reg.snapshotRegister()
	if task != "ticket" || addr != "10.0.0.1:8000" || cap != 5 {
		t.Errorf("unexpected registry values: task=%q addr=%q cap=%d", task, addr, cap)
	}
}

func TestHandleMessageQuery(t *testing.T) {
	reg := &fakeRegistry{queryAddress: "10.0.0.2:9000"}
	msg := CentralizedMessage{
		Command: "QUERY",
		Task:    "ticket",
	}
	
	resp := HandleMessage(reg, msg, net.ParseIP("192.168.1.100"), &net.UDPAddr{}, nil)
	
	if resp.Status != "OK" || resp.Address != "10.0.0.2:9000" {
		t.Errorf("expected OK with address, got %+v", resp)
	}
}

func TestHandleMessageValidation(t *testing.T) {
	testCases := []struct {
		name    string
		msg     CentralizedMessage
		wantErr string
	}{
		{
			name:    "empty task on REGISTER",
			msg:     CentralizedMessage{Command: "REGISTER", Task: "", Address: "10.0.0.1:8000"},
			wantErr: "task and address required",
		},
		{
			name:    "whitespace task on REGISTER",
			msg:     CentralizedMessage{Command: "REGISTER", Task: "   ", Address: "10.0.0.1:8000"},
			wantErr: "task and address required",
		},
		{
			name:    "empty address on REGISTER",
			msg:     CentralizedMessage{Command: "REGISTER", Task: "ticket", Address: ""},
			wantErr: "task and address required",
		},
		{
			name:    "whitespace address on REGISTER",
			msg:     CentralizedMessage{Command: "REGISTER", Task: "ticket", Address: "   "},
			wantErr: "task and address required",
		},
		{
			name:    "empty task on QUERY",
			msg:     CentralizedMessage{Command: "QUERY", Task: ""},
			wantErr: "task required",
		},
		{
			name:    "whitespace task on QUERY",
			msg:     CentralizedMessage{Command: "QUERY", Task: "   "},
			wantErr: "task required",
		},
		{
			name:    "unknown command",
			msg:     CentralizedMessage{Command: "FOOBAR"},
			wantErr: "unknown command",
		},
	}
	
	reg := &fakeRegistry{}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp := HandleMessage(reg, tc.msg, nil, &net.UDPAddr{}, nil)
			if resp.Status != "ERR" {
				t.Errorf("expected ERR status, got %s", resp.Status)
			}
			if !strings.Contains(resp.Error, tc.wantErr) {
				t.Errorf("expected error %q, got %q", tc.wantErr, resp.Error)
			}
		})
	}
}

func TestHandleMessageDefaultCapacity(t *testing.T) {
	reg := &fakeRegistry{}
	
	// Capacity = 0 should default to 1
	msg := CentralizedMessage{
		Command:  "REGISTER",
		Task:     "ticket",
		Address:  "10.0.0.1:8000",
		Capacity: 0,
	}
	
	resp := HandleMessage(reg, msg, nil, &net.UDPAddr{}, nil)
	if resp.Status != "OK" {
		t.Errorf("register failed: %+v", resp)
	}
	
	_, _, cap := reg.snapshotRegister()
	if cap != 1 {
		t.Errorf("expected default capacity 1, got %d", cap)
	}
}

func TestHandleMessageErrorStatuses(t *testing.T) {
	testCases := []struct {
		name       string
		queryError error
		queryAddr  string
		wantStatus string
	}{
		{
			name:       "success",
			queryError: nil,
			queryAddr:  "10.0.0.1:8000",
			wantStatus: "OK",
		},
		{
			name:       "not found",
			queryError: registry.ErrNotFound,
			queryAddr:  "",
			wantStatus: "NOTFOUND",
		},
		{
			name:       "no allowed service",
			queryError: registry.ErrNoAllowedService,
			queryAddr:  "10.0.0.1:8000",
			wantStatus: "FORBIDDEN",
		},
		{
			name:       "empty address treated as not found",
			queryError: nil,
			queryAddr:  "",
			wantStatus: "NOTFOUND",
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			reg := &fakeRegistry{
				queryAddress: tc.queryAddr,
				queryError:   tc.queryError,
			}
			
			msg := CentralizedMessage{Command: "QUERY", Task: "ticket"}
			resp := HandleMessage(reg, msg, nil, &net.UDPAddr{}, nil)
			
			if resp.Status != tc.wantStatus {
				t.Errorf("expected status %q, got %q", tc.wantStatus, resp.Status)
			}
		})
	}
}

// ========================================================================
// Broadcast Tests
// ========================================================================

func TestGetLocalIPv4(t *testing.T) {
	ip := getLocalIPv4()
	
	if ip == "" {
		t.Fatal("getLocalIPv4 returned empty string")
	}
	
	if ip == "0.0.0.0" {
		t.Skip("no network interfaces available")
	}
	
	parsed := net.ParseIP(ip)
	if parsed == nil {
		t.Fatalf("getLocalIPv4 returned invalid IP: %q", ip)
	}
	
	if parsed.To4() == nil {
		t.Fatalf("getLocalIPv4 returned non-IPv4: %v", parsed)
	}
	
	if parsed.IsLoopback() {
		t.Logf("Warning: getLocalIPv4 returned loopback: %v", parsed)
	}
	
	t.Logf("Local IPv4: %s", ip)
}

func TestBroadcastServerInfo(t *testing.T) {
	// This test is best-effort since UDP broadcast is unreliable
	// Listen on discovery port
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{
		IP:   net.IPv4zero,
		Port: 5001,
	})
	if err != nil {
		t.Skipf("cannot bind port 5001 (may be in use): %v", err)
	}
	defer conn.Close()
	
	// Trigger broadcast in background
	startTime := time.Now()
	done := make(chan struct{})
	go func() {
		BroadcastServerInfo(5000, 60*time.Second, startTime)
		close(done)
	}()
	
	// Try to read broadcast packet (may receive multiple due to retries)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, _, err := conn.ReadFromUDP(buf)
	if err != nil {
		t.Skipf("did not receive broadcast (may be network configuration): %v", err)
	}
	
	// Parse JSON
	var announcement map[string]interface{}
	if err := json.Unmarshal(buf[:n], &announcement); err != nil {
		t.Fatalf("broadcast not valid JSON: %v", err)
	}
	
	// Verify fields
	if announcement["type"] != "tds_server" {
		t.Errorf("wrong type: %v", announcement["type"])
	}
	if announcement["port"] != float64(5000) {
		t.Errorf("wrong port: %v", announcement["port"])
	}
	if announcement["address"] == nil {
		t.Errorf("missing address field")
	}
	if announcement["heartbeat_timeout_seconds"] != float64(60) {
		t.Errorf("wrong heartbeat timeout: %v", announcement["heartbeat_timeout_seconds"])
	}
	
	<-done
	t.Logf("Broadcast successful: %+v", announcement)
}

// ========================================================================
// Event Callback Tests
// ========================================================================

func TestOnEventCallback(t *testing.T) {
	var events []string
	var mu sync.Mutex
	
	onEvent := func(msg string) {
		mu.Lock()
		events = append(events, msg)
		mu.Unlock()
	}
	
	reg := &fakeRegistry{}
	reg.queryAddress = "10.0.0.1:8000"
	
	// Test REGISTER event
	regMsg := CentralizedMessage{
		Command:  "REGISTER",
		Task:     "ticket",
		Address:  "10.0.0.1:8000",
		Capacity: 3,
	}
	HandleMessage(reg, regMsg, nil, &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 12345}, onEvent)
	
	// Test QUERY event
	queryMsg := CentralizedMessage{
		Command: "QUERY",
		Task:    "ticket",
	}
	HandleMessage(reg, queryMsg, net.ParseIP("192.168.1.2"), &net.UDPAddr{IP: net.ParseIP("192.168.1.2"), Port: 23456}, onEvent)
	
	mu.Lock()
	defer mu.Unlock()
	
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d: %v", len(events), events)
	}
	
	if !strings.Contains(events[0], "REGISTER") || !strings.Contains(events[0], "ticket") {
		t.Errorf("unexpected REGISTER event: %q", events[0])
	}
	
	if !strings.Contains(events[1], "QUERY") || !strings.Contains(events[1], "ticket") {
		t.Errorf("unexpected QUERY event: %q", events[1])
	}
	
	t.Logf("Events: %v", events)
}

// ========================================================================
// TLS Server Tests
// ========================================================================

// generateTransportTestCert creates a self-signed CA and issues a combined
// server/client certificate, writing PEM files to a temp directory.
func generateTransportTestCert(t *testing.T) (certFile, keyFile, caFile string) {
	t.Helper()

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}

	caTemplate := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{Organization: []string{"Test CA"}},
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

	certKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate cert key: %v", err)
	}

	certTemplate := x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{Organization: []string{"Test Cert"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &certTemplate, &caTemplate, &certKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}

	dir := t.TempDir()

	caFile = dir + "/ca.crt"
	f, _ := os.Create(caFile)
	pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: caCertDER})
	f.Close()

	certFile = dir + "/cert.crt"
	f, _ = os.Create(certFile)
	pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	f.Close()

	keyFile = dir + "/cert.key"
	f, _ = os.Create(keyFile)
	pem.Encode(f, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(certKey)})
	f.Close()

	return certFile, keyFile, caFile
}

// buildTLSClientConfig builds a mutual-auth TLS client config using the test certs.
func buildTLSClientConfig(t *testing.T, certFile, keyFile, caFile string) *tls.Config {
	t.Helper()
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatalf("load client cert: %v", err)
	}
	caCert, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatalf("read CA cert: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caCert)
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		ServerName:   "127.0.0.1",
	}
}

func TestStartTCPServerTLSRegisterAndQuery(t *testing.T) {
	certFile, keyFile, caFile := generateTransportTestCert(t)

	reg := registry.NewMemoryRegistry()

	// Bind to a free port for the TLS server.
	tempLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("get free port: %v", err)
	}
	addr := tempLn.Addr().String()
	_, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	tempLn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- StartTCPServerTLSWithContext(ctx, reg, port, 100, certFile, keyFile, caFile, nil)
	}()
	time.Sleep(100 * time.Millisecond)

	tlsCfg := buildTLSClientConfig(t, certFile, keyFile, caFile)

	// REGISTER
	conn, err := tls.Dial("tcp", addr, tlsCfg)
	if err != nil {
		t.Fatalf("TLS dial for REGISTER: %v", err)
	}
	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)
	enc.Encode(CentralizedMessage{Command: "REGISTER", Task: "tls-task", Address: "10.1.1.1:9000", Capacity: 2})
	var resp CentralizedResponse
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("decode REGISTER response: %v", err)
	}
	conn.Close()
	if resp.Status != "OK" {
		t.Errorf("expected OK, got %+v", resp)
	}

	// QUERY
	conn, err = tls.Dial("tcp", addr, tlsCfg)
	if err != nil {
		t.Fatalf("TLS dial for QUERY: %v", err)
	}
	enc = json.NewEncoder(conn)
	dec = json.NewDecoder(conn)
	enc.Encode(CentralizedMessage{Command: "QUERY", Task: "tls-task"})
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("decode QUERY response: %v", err)
	}
	conn.Close()
	if resp.Status != "OK" || resp.Address != "10.1.1.1:9000" {
		t.Errorf("expected OK/10.1.1.1:9000, got %+v", resp)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Error("TLS server did not shut down in time")
	}
}

func TestStartTCPServerTLSWithOnEvent(t *testing.T) {
	certFile, keyFile, caFile := generateTransportTestCert(t)

	var mu sync.Mutex
	var events []string
	onEvent := func(msg string) {
		mu.Lock()
		events = append(events, msg)
		mu.Unlock()
	}

	reg := registry.NewMemoryRegistry()

	tempLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("get free port: %v", err)
	}
	addr := tempLn.Addr().String()
	_, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	tempLn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go StartTCPServerTLSWithContext(ctx, reg, port, 100, certFile, keyFile, caFile, onEvent)
	time.Sleep(100 * time.Millisecond)

	tlsCfg := buildTLSClientConfig(t, certFile, keyFile, caFile)
	conn, err := tls.Dial("tcp", addr, tlsCfg)
	if err != nil {
		t.Fatalf("TLS dial: %v", err)
	}
	json.NewEncoder(conn).Encode(CentralizedMessage{
		Command: "REGISTER", Task: "ev-task", Address: "10.2.2.2:7000", Capacity: 1,
	})
	var resp CentralizedResponse
	json.NewDecoder(conn).Decode(&resp)
	conn.Close()

	// Allow onEvent to fire.
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	evSnapshot := append([]string(nil), events...)
	mu.Unlock()

	found := false
	for _, e := range evSnapshot {
		if strings.Contains(e, "ev-task") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected an onEvent call mentioning ev-task, got events: %v", evSnapshot)
	}
}

func TestStartTCPServerTLSInvalidCerts(t *testing.T) {
	reg := registry.NewMemoryRegistry()
	err := StartTCPServerTLS(reg, 0, 100, "/nonexistent/cert.crt", "/nonexistent/key.key", "/nonexistent/ca.crt", nil)
	if err == nil {
		t.Error("expected error for missing cert files, got nil")
	}
}

func TestStartTCPServerTLSBadCACert(t *testing.T) {
	certFile, keyFile, _ := generateTransportTestCert(t)

	// Write a garbage CA file.
	badCA := certFile + ".badca"
	if err := os.WriteFile(badCA, []byte("not a PEM file"), 0600); err != nil {
		t.Fatalf("write bad CA: %v", err)
	}

	reg := registry.NewMemoryRegistry()
	err := StartTCPServerTLS(reg, 0, 100, certFile, keyFile, badCA, nil)
	if err == nil {
		t.Error("expected error for invalid CA cert, got nil")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("expected parse error, got: %v", err)
	}
}

func TestStartTCPServerTLSEmptyCA(t *testing.T) {
	certFile, keyFile, _ := generateTransportTestCert(t)

	// Write an empty (but technically readable) CA file.
	emptyCA := certFile + ".emptyca"
	if err := os.WriteFile(emptyCA, []byte(""), 0600); err != nil {
		t.Fatalf("write empty CA: %v", err)
	}

	reg := registry.NewMemoryRegistry()
	err := StartTCPServerTLS(reg, 0, 100, certFile, keyFile, emptyCA, nil)
	if err == nil {
		t.Error("expected error for empty CA cert, got nil")
	}
}

// ========================================================================
// Context Cancellation / Shutdown Tests
// ========================================================================

func TestUDPServerContextCancellation(t *testing.T) {
	reg := registry.NewMemoryRegistry()

	// Bind to a free port.
	tempConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("get free UDP port: %v", err)
	}
	addr := tempConn.LocalAddr().String()
	_, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	tempConn.Close()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- StartUDPServerWithContext(ctx, reg, port, 100, nil)
	}()
	time.Sleep(50 * time.Millisecond)

	// Verify the server handles a normal request.
	msg := CentralizedMessage{Command: "REGISTER", Task: "ctx-task", Address: "10.9.9.9:9000"}
	data, _ := json.Marshal(msg)
	udpConn, _ := net.Dial("udp", addr)
	udpConn.Write(data)
	buf := make([]byte, 4096)
	udpConn.SetReadDeadline(time.Now().Add(time.Second))
	n, err := udpConn.Read(buf)
	udpConn.Close()
	if err != nil || n == 0 {
		t.Fatalf("expected response before cancel: err=%v n=%d", err, n)
	}

	// Cancel context and wait for clean exit.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("server returned unexpected error on cancel: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("UDP server did not shut down within 3 s after cancel")
	}
}

func TestTCPServerContextCancellation(t *testing.T) {
	reg := registry.NewMemoryRegistry()

	tempLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("get free TCP port: %v", err)
	}
	addr := tempLn.Addr().String()
	_, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	tempLn.Close()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- StartTCPServerWithContext(ctx, reg, port, 100, nil)
	}()
	time.Sleep(50 * time.Millisecond)

	// Open a connection and send a request.
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("connect to TCP server: %v", err)
	}
	enc := json.NewEncoder(conn)
	dec := bufio.NewReader(conn)
	enc.Encode(CentralizedMessage{Command: "REGISTER", Task: "tp-task", Address: "10.0.0.1:8000"})
	line, err := dec.ReadBytes('\n')
	conn.Close()
	if err != nil || len(line) == 0 {
		t.Fatalf("expected response before cancel: err=%v", err)
	}

	// Cancel and wait.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("server returned unexpected error on cancel: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("TCP server did not shut down within 3 s after cancel")
	}
}

func TestTCPServerTLSContextCancellation(t *testing.T) {
	certFile, keyFile, caFile := generateTransportTestCert(t)
	reg := registry.NewMemoryRegistry()

	tempLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("get free port: %v", err)
	}
	_, portStr, _ := net.SplitHostPort(tempLn.Addr().String())
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	tempLn.Close()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- StartTCPServerTLSWithContext(ctx, reg, port, 100, certFile, keyFile, caFile, nil)
	}()
	time.Sleep(100 * time.Millisecond)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("TLS server returned unexpected error on cancel: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("TLS server did not shut down within 3 s after cancel")
	}
}

// ========================================================================
// Additional handleUDPRequest coverage
// ========================================================================

// TestHandleUDPRequestWriteError confirms the handler does not panic when the
// remote address is unreachable (write error is silently logged).
func TestHandleUDPRequestWriteErrorSilent(t *testing.T) {
	fake := &fakeRegistry{}
	fake.queryAddress = "10.0.0.1:8000"

	// Use a real server conn for writing; point at a port nothing listens on so
	// the write will fail. The handler must not panic.
	serverConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer serverConn.Close()

	unreachable := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1} // port 1 is almost never open

	// Should not panic even with an unreachable destination.
	handleUDPRequest(serverConn, fake, []byte(`{"cmd":"QUERY","task":"ticket"}`), unreachable, nil)
}

// TestStartUDPServerPortInUse verifies StartUDPServer returns an error when the
// requested port is invalid.
func TestStartUDPServerPortInUse(t *testing.T) {
	reg := registry.NewMemoryRegistry()
	// Port -1 is invalid and should fail immediately at the Listen call.
	err := StartUDPServer(reg, -1, 100, nil)
	if err == nil {
		t.Error("expected error for invalid port, got nil")
	}
}

// TestStartTCPServerPortInUse verifies StartTCPServer returns an error when the
// requested port is invalid.
func TestStartTCPServerPortInUse(t *testing.T) {
	reg := registry.NewMemoryRegistry()
	// Port -1 is invalid and should fail immediately at the Listen call.
	err := StartTCPServer(reg, -1, 100, nil)
	if err == nil {
		t.Error("expected error for invalid port, got nil")
	}
}
