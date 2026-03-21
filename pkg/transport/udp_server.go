package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
	"tds/pkg/registry"
)

// StartUDPServer starts a JSON-based UDP server that accepts commands:
//
//	{"cmd": "REGISTER", "task": "taskname", "address": "ip:port"}
//	{"cmd": "QUERY", "task": "taskname"}
//
// Responds with JSON:
//
//	{"status": "OK"}
//	{"status": "NOTFOUND"}
//	{"status": "FORBIDDEN"}
//	{"status": "ERR", "error": "..."}
//	{"status": "OK", "address": "ip:port"}
//
// maxConcurrent limits the number of concurrent request handlers. If 0, defaults to 1000.
// onEvent, if non-nil, will be called with short human-readable messages for UI/logging.
func StartUDPServer(reg registry.Registry, port int, maxConcurrent int64, onEvent func(string)) error {
	return StartUDPServerWithContext(context.Background(), reg, port, maxConcurrent, onEvent)
}

// StartUDPServerWithContext starts a UDP server that shuts down when ctx is canceled.
func StartUDPServerWithContext(ctx context.Context, reg registry.Registry, port int, maxConcurrent int64, onEvent func(string)) error {
	if maxConcurrent <= 0 {
		maxConcurrent = 1000 // Default limit
	}

	addr := net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: port}
	conn, err := net.ListenUDP("udp", &addr)
	if err != nil {
		return fmt.Errorf("listen udp: %w", err)
	}
	defer conn.Close()

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	sem := semaphore.NewWeighted(maxConcurrent)
	const acquireTimeout = 100 * time.Millisecond
	var handlers sync.WaitGroup

	for {
		buf := make([]byte, 4096)
		n, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				handlers.Wait()
				return nil
			}
			// transient read error: log and continue
			transportEvent(onEvent, "udp read error: %v", err)
			continue
		}

		// Handle each request concurrently with semaphore limiting.
		data := make([]byte, n)
		copy(data, buf[:n])

		ctx, cancel := context.WithTimeout(context.Background(), acquireTimeout)
		if err := sem.Acquire(ctx, 1); err == nil {
			handlers.Add(1)
			go func(d []byte, r *net.UDPAddr) {
				defer handlers.Done()
				defer sem.Release(1)
				handleUDPRequest(conn, reg, d, r, onEvent)
			}(data, remote)
		} // else: drop request due to overload
		cancel()
	}
}

func handleUDPRequest(conn *net.UDPConn, reg registry.Registry, data []byte, remote *net.UDPAddr, onEvent func(string)) {
	var msg CentralizedMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		resp := CentralizedResponse{Status: "ERR", Error: "invalid JSON: " + err.Error()}
		respData, err := json.Marshal(resp)
		if err != nil {
			transportEvent(onEvent, "udp marshal error: %v", err)
			return
		}
		if _, err := conn.WriteToUDP(respData, remote); err != nil {
			transportEvent(onEvent, "udp write error (invalid JSON response): %v", err)
		}
		return
	}

	resp := HandleMessage(reg, msg, remote.IP, remote, onEvent)
	respData, err := json.Marshal(resp)
	if err != nil {
		transportEvent(onEvent, "udp marshal error: %v", err)
		return
	}
	if _, err := conn.WriteToUDP(respData, remote); err != nil {
		transportEvent(onEvent, "udp write error: %v", err)
	}
}
