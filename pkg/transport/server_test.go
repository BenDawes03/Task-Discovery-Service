package transport

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
	"sync"
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
	if responses[6].Status != "ERR" || !strings.Contains(responses[6].Error, "invalid JSON") {
		t.Fatalf("expected invalid JSON ERR, got %+v", responses[6])
	}
}

func TestHandleTCPConnForbiddenMapping(t *testing.T) {
	fake := &fakeRegistry{queryAddress: "10.9.9.9:9000", queryError: registry.ErrNoAllowedService}
	responses := runTCPConversation(t, fake, `{"cmd":"QUERY","task":"blocked"}`)
	if responses[0].Status != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN, got %+v", responses[0])
	}
}
