package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"tds/pkg/netutil"
	"tds/pkg/transport"
)

var logger = log.New(os.Stdout, "[client-proxy] ", log.LstdFlags)

// atomic counters
var (
	regCount   uint64
	queryCount uint64
	errorCount uint64
)

type proxyRequest struct {
	Command  string
	Task     string
	Address  string
	Capacity int
	JSON     bool
}

type proxyResult struct {
	Status  string
	Address string
	Error   string
}

// RunProxy starts a UDP proxy that listens on listenAddr (e.g. ":5100")
// and forwards REGISTER/QUERY commands to the configured serverAddr.
// It respects ctx cancellation and will exit when ctx is done.
func RunProxy(ctx context.Context, listenAddr string) error {
	serverAddr := os.Getenv("TDS_SERVER_ADDR")
	if serverAddr == "" {
		serverAddr = "127.0.0.1:5000"
	}
	backendProto := strings.ToLower(strings.TrimSpace(os.Getenv("TDS_SERVER_PROTO")))
	if backendProto == "" {
		backendProto = "udp"
	}

	udpAddr, err := net.ResolveUDPAddr("udp", listenAddr)
	if err != nil {
		return fmt.Errorf("resolve listen addr: %w", err)
	}
	conn, err := netutil.ListenUDP(udpAddr.String())
	if err != nil {
		return fmt.Errorf("listen udp: %w", err)
	}
	defer conn.Close()
	logger.Printf("listening %s, forwarding to %s", listenAddr, serverAddr)

	buf := make([]byte, 2048)
	for {
		// allow periodic wake to check ctx
		conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				select {
				case <-ctx.Done():
					logger.Println("shutting down proxy")
					return nil
				default:
					continue
				}
			}
			logger.Printf("read udp: %v", err)
			atomic.AddUint64(&errorCount, 1)
			continue
		}
		data := strings.TrimSpace(string(buf[:n]))
		go handlePacket(conn, addr, data, serverAddr, backendProto)
	}
}

func handlePacket(conn *net.UDPConn, src *net.UDPAddr, data string, serverAddr string, backendProto string) {
	req, err := parseProxyRequest(data)
	if err != nil {
		atomic.AddUint64(&errorCount, 1)
		writeUDPResult(conn, src, req.JSON, proxyResult{Status: "ERR", Error: err.Error()})
		return
	}

	result := handleCentralizedRequest(req, serverAddr, backendProto, src.String())
	if result.Status == "ERR" {
		atomic.AddUint64(&errorCount, 1)
	}
	writeUDPResult(conn, src, req.JSON, result)
}

func writeUDP(conn *net.UDPConn, addr *net.UDPAddr, msg string) {
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err := conn.WriteToUDP([]byte(msg), addr)
	if err != nil {
		logger.Printf("write udp to %s: %v", addr.String(), err)
		atomic.AddUint64(&errorCount, 1)
	}
}

// RunProxyTCP starts a simple TCP proxy that listens on listenAddr (e.g. ":5100")
// and forwards each incoming line to the configured serverAddr over TCP. It
// respects ctx cancellation and will exit when ctx is done.
func RunProxyTCP(ctx context.Context, listenAddr string) error {
	serverAddr := os.Getenv("TDS_SERVER_ADDR")
	if serverAddr == "" {
		serverAddr = "127.0.0.1:5000"
	}

	ln, err := netutil.ListenTCP(listenAddr)
	if err != nil {
		return fmt.Errorf("listen tcp: %w", err)
	}
	defer ln.Close()
	logger.Printf("listening tcp %s, forwarding to %s", listenAddr, serverAddr)

	// close listener when context is done so Accept returns
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				logger.Println("shutting down tcp proxy")
				return nil
			default:
				logger.Printf("tcp accept error: %v", err)
				atomic.AddUint64(&errorCount, 1)
				continue
			}
		}
		go handleTCPProxyConn(conn, serverAddr)
	}
}

func handleTCPProxyConn(conn net.Conn, serverAddr string) {
	defer conn.Close()
	remote := conn.RemoteAddr().String()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		req, err := parseProxyRequest(line)
		if err != nil {
			atomic.AddUint64(&errorCount, 1)
			writeTCPResult(w, req.JSON, proxyResult{Status: "ERR", Error: err.Error()})
			continue
		}

		result := handleCentralizedRequest(req, serverAddr, "tcp", remote)
		if result.Status == "ERR" {
			atomic.AddUint64(&errorCount, 1)
		}
		writeTCPResult(w, req.JSON, result)
	}
}

// Stats returns a simple snapshot of proxy counters.
func Stats() (regs, queries, errs uint64) {
	regs = atomic.LoadUint64(&regCount)
	queries = atomic.LoadUint64(&queryCount)
	errs = atomic.LoadUint64(&errorCount)
	return
}

// DHTRegistry interface defines what we need from a DHT implementation
type DHTRegistry interface {
	Register(task, address string) error
	Query(task string) (string, error)
	QueryAll(task string) ([]string, error)
}

// RunProxyP2P starts a UDP proxy that uses DHT for distributed task registration
func RunProxyP2P(ctx context.Context, listenAddr string, dhtRegistry DHTRegistry) error {
	udpAddr, err := net.ResolveUDPAddr("udp", listenAddr)
	if err != nil {
		return fmt.Errorf("resolve listen addr: %w", err)
	}
	conn, err := netutil.ListenUDP(udpAddr.String())
	if err != nil {
		return fmt.Errorf("listen udp: %w", err)
	}
	defer conn.Close()
	logger.Printf("P2P proxy listening %s", listenAddr)

	buf := make([]byte, 2048)
	for {
		// allow periodic wake to check ctx
		conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				select {
				case <-ctx.Done():
					logger.Println("shutting down P2P proxy")
					return nil
				default:
					continue
				}
			}
			logger.Printf("read udp: %v", err)
			atomic.AddUint64(&errorCount, 1)
			continue
		}
		data := strings.TrimSpace(string(buf[:n]))
		go handlePacketP2P(conn, addr, data, dhtRegistry)
	}
}

func handlePacketP2P(conn *net.UDPConn, src *net.UDPAddr, data string, dhtRegistry DHTRegistry) {
	req, err := parseProxyRequest(data)
	if err != nil {
		atomic.AddUint64(&errorCount, 1)
		writeUDPResult(conn, src, req.JSON, proxyResult{Status: "ERR", Error: err.Error()})
		return
	}

	result := handleP2PRequest(req, dhtRegistry, src.String())
	if result.Status == "ERR" {
		atomic.AddUint64(&errorCount, 1)
	}
	writeUDPResult(conn, src, req.JSON, result)
}

func parseProxyRequest(data string) (proxyRequest, error) {
	data = strings.TrimSpace(data)
	req := proxyRequest{Capacity: 1}
	if data == "" {
		return req, fmt.Errorf("empty command")
	}

	if strings.HasPrefix(data, "{") {
		req.JSON = true
		var msg transport.CentralizedMessage
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			return req, fmt.Errorf("invalid JSON: %w", err)
		}
		req.Command = strings.ToUpper(strings.TrimSpace(msg.Command))
		req.Task = strings.TrimSpace(msg.Task)
		req.Address = strings.TrimSpace(msg.Address)
		if msg.Capacity > 0 {
			req.Capacity = msg.Capacity
		}
		return req, nil
	}

	parts := strings.Fields(data)
	if len(parts) == 0 {
		return req, fmt.Errorf("empty command")
	}
	req.Command = strings.ToUpper(parts[0])
	switch req.Command {
	case "REGISTER":
		if len(parts) < 3 {
			return req, fmt.Errorf("usage: REGISTER <task> <address> [capacity]")
		}
		req.Task = parts[1]
		req.Address = parts[2]
		if len(parts) >= 4 {
			capVal, err := strconv.Atoi(parts[3])
			if err != nil || capVal <= 0 {
				return req, fmt.Errorf("capacity must be a positive integer")
			}
			req.Capacity = capVal
		}
	case "QUERY":
		if len(parts) < 2 {
			return req, fmt.Errorf("usage: QUERY <task>")
		}
		req.Task = parts[1]
	default:
		return req, fmt.Errorf("unknown command")
	}

	return req, nil
}

func handleCentralizedRequest(req proxyRequest, serverAddr, backendProto, source string) proxyResult {
	switch req.Command {
	case "REGISTER":
		if req.Task == "" || req.Address == "" {
			return proxyResult{Status: "ERR", Error: "task and address required"}
		}
		logger.Printf("REGISTER %s -> %s cap=%d (from %s)", req.Task, req.Address, req.Capacity, source)

		var err error
		if backendProto == "tcp" {
			err = RegisterTCPWithCapacity(serverAddr, req.Task, req.Address, req.Capacity)
		} else {
			err = RegisterUDPWithCapacity(serverAddr, req.Task, req.Address, req.Capacity)
		}
		if err != nil {
			return proxyResult{Status: "ERR", Error: err.Error()}
		}
		atomic.AddUint64(&regCount, 1)
		return proxyResult{Status: "OK"}

	case "QUERY":
		if req.Task == "" {
			return proxyResult{Status: "ERR", Error: "task required"}
		}
		logger.Printf("QUERY %s (from %s)", req.Task, source)

		var (
			addr string
			err  error
		)
		if backendProto == "tcp" {
			addr, err = QueryTCP(serverAddr, req.Task)
		} else {
			addr, err = QueryUDP(serverAddr, req.Task)
		}
		if err != nil {
			return proxyResult{Status: "ERR", Error: err.Error()}
		}
		atomic.AddUint64(&queryCount, 1)
		if addr == "" {
			return proxyResult{Status: "NOTFOUND"}
		}
		return proxyResult{Status: "OK", Address: addr}
	}

	return proxyResult{Status: "ERR", Error: "unknown command"}
}

func handleP2PRequest(req proxyRequest, dhtRegistry DHTRegistry, source string) proxyResult {
	switch req.Command {
	case "REGISTER":
		if req.Task == "" || req.Address == "" {
			return proxyResult{Status: "ERR", Error: "task and address required"}
		}
		logger.Printf("P2P REGISTER %s -> %s (from %s)", req.Task, req.Address, source)
		if err := dhtRegistry.Register(req.Task, req.Address); err != nil {
			return proxyResult{Status: "ERR", Error: err.Error()}
		}
		atomic.AddUint64(&regCount, 1)
		return proxyResult{Status: "OK"}

	case "QUERY":
		if req.Task == "" {
			return proxyResult{Status: "ERR", Error: "task required"}
		}
		logger.Printf("P2P QUERY %s (from %s)", req.Task, source)
		addr, err := dhtRegistry.Query(req.Task)
		if err != nil {
			return proxyResult{Status: "ERR", Error: err.Error()}
		}
		atomic.AddUint64(&queryCount, 1)
		if addr == "" {
			return proxyResult{Status: "NOTFOUND"}
		}
		return proxyResult{Status: "OK", Address: addr}
	}

	return proxyResult{Status: "ERR", Error: "unknown command"}
}

func writeUDPResult(conn *net.UDPConn, addr *net.UDPAddr, jsonMode bool, result proxyResult) {
	if jsonMode {
		resp := transport.CentralizedResponse{Status: result.Status, Address: result.Address, Error: result.Error}
		data, err := json.Marshal(resp)
		if err != nil {
			writeUDP(conn, addr, "ERR marshal response")
			atomic.AddUint64(&errorCount, 1)
			return
		}
		writeUDP(conn, addr, string(data))
		return
	}

	if result.Status == "OK" {
		if result.Address != "" {
			writeUDP(conn, addr, result.Address)
			return
		}
		writeUDP(conn, addr, "OK")
		return
	}
	if result.Status == "NOTFOUND" {
		writeUDP(conn, addr, "NOTFOUND")
		return
	}
	writeUDP(conn, addr, "ERR "+result.Error)
}

func writeTCPResult(w *bufio.Writer, jsonMode bool, result proxyResult) {
	if jsonMode {
		resp := transport.CentralizedResponse{Status: result.Status, Address: result.Address, Error: result.Error}
		data, err := json.Marshal(resp)
		if err != nil {
			fmt.Fprint(w, `{"status":"ERR","error":"marshal response"}`+"\n")
			_ = w.Flush()
			atomic.AddUint64(&errorCount, 1)
			return
		}
		fmt.Fprintf(w, "%s\n", string(data))
		_ = w.Flush()
		return
	}

	if result.Status == "OK" {
		if result.Address != "" {
			fmt.Fprintf(w, "%s\n", result.Address)
		} else {
			fmt.Fprint(w, "OK\n")
		}
		_ = w.Flush()
		return
	}
	if result.Status == "NOTFOUND" {
		fmt.Fprint(w, "NOTFOUND\n")
		_ = w.Flush()
		return
	}
	fmt.Fprintf(w, "ERR %s\n", result.Error)
	_ = w.Flush()
}
