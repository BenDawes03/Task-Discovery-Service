package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
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

// Status constants for proxy responses
const (
	StatusOK       = "OK"
	StatusErr      = "ERR"
	StatusNotFound = "NOTFOUND"
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

// requestHandler processes a proxy request and returns a result
type requestHandler func(req proxyRequest, source string) proxyResult

// runUDPProxy is a generic UDP proxy loop that listens on listenAddr and delegates to a handler.
// It respects ctx cancellation and manages error/success counting.
func runUDPProxy(ctx context.Context, listenAddr string, logPrefix string, handler requestHandler) error {
	udpAddr, err := net.ResolveUDPAddr("udp", listenAddr)
	if err != nil {
		return fmt.Errorf("resolve listen addr: %w", err)
	}
	conn, err := netutil.ListenUDP(udpAddr.String())
	if err != nil {
		return fmt.Errorf("listen udp: %w", err)
	}
	defer conn.Close()
	logger.Printf("%s listening %s", logPrefix, listenAddr)

	buf := make([]byte, 2048)
	for {
		// allow periodic wake to check ctx
		conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				select {
				case <-ctx.Done():
					logger.Printf("shutting down %s", logPrefix)
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
		go handlePacketGeneric(conn, addr, data, handler)
	}
}

// handlePacketGeneric processes a UDP packet by parsing the request and delegating to a handler.
func handlePacketGeneric(conn *net.UDPConn, src *net.UDPAddr, data string, handler requestHandler) {
	req, err := parseProxyRequest(data)
	if err != nil {
		atomic.AddUint64(&errorCount, 1)
		writeResult(conn, src, req.JSON, proxyResult{Status: StatusErr, Error: err.Error()})
		return
	}

	result := handler(req, src.String())
	if result.Status == StatusErr {
		atomic.AddUint64(&errorCount, 1)
	}
	writeResult(conn, src, req.JSON, result)
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

	handler := func(req proxyRequest, source string) proxyResult {
		return handleCentralizedRequest(req, serverAddr, backendProto, source)
	}

	return runUDPProxy(ctx, listenAddr, "proxy", handler)
}

func writeUDP(conn *net.UDPConn, addr *net.UDPAddr, msg string) {
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err := conn.WriteToUDP([]byte(msg), addr)
	if err != nil {
		logger.Printf("write udp to %s: %v", addr.String(), err)
		atomic.AddUint64(&errorCount, 1)
	}
}

// writeResult sends a result to a UDP client in either JSON or text mode.
func writeResult(conn *net.UDPConn, addr *net.UDPAddr, jsonMode bool, result proxyResult) {
	if jsonMode {
		resp := transport.CentralizedResponse{Status: result.Status, Address: result.Address, Error: result.Error}
		data, err := json.Marshal(resp)
		if err != nil {
			writeUDP(conn, addr, StatusErr+" marshal response")
			atomic.AddUint64(&errorCount, 1)
			return
		}
		writeUDP(conn, addr, string(data))
		return
	}

	if result.Status == StatusOK {
		if result.Address != "" {
			writeUDP(conn, addr, result.Address)
			return
		}
		writeUDP(conn, addr, StatusOK)
		return
	}
	if result.Status == StatusNotFound {
		writeUDP(conn, addr, StatusNotFound)
		return
	}
	writeUDP(conn, addr, StatusErr+" "+result.Error)
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
			writeTCPResultGeneric(w, req.JSON, proxyResult{Status: StatusErr, Error: err.Error()})
			continue
		}

		result := handleCentralizedRequest(req, serverAddr, "tcp", remote)
		if result.Status == StatusErr {
			atomic.AddUint64(&errorCount, 1)
		}
		writeTCPResultGeneric(w, req.JSON, result)
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
	handler := func(req proxyRequest, source string) proxyResult {
		return handleP2PRequest(req, dhtRegistry, source)
	}

	return runUDPProxy(ctx, listenAddr, "P2P proxy", handler)
}

func parseProxyRequest(data string) (proxyRequest, error) {
	data = strings.TrimSpace(data)
	req := proxyRequest{Capacity: 1, JSON: true}
	if data == "" {
		return req, fmt.Errorf("empty command")
	}

	if !strings.HasPrefix(data, "{") {
		return req, fmt.Errorf("JSON required")
	}

	var msg transport.CentralizedMessage
	if err := json.Unmarshal([]byte(data), &msg); err != nil {
		return req, fmt.Errorf("invalid JSON: %w", err)
	}
	req.Command = strings.ToUpper(strings.TrimSpace(msg.Command))
	req.Task = strings.TrimSpace(msg.Task)
	req.Address = strings.TrimSpace(msg.Address)
	if msg.Capacity < 0 {
		return req, fmt.Errorf("capacity must be a positive integer")
	}
	if msg.Capacity > 0 {
		req.Capacity = msg.Capacity
	}

	return req, nil
}

func handleCentralizedRequest(req proxyRequest, serverAddr, backendProto, source string) proxyResult {
	switch req.Command {
	case "REGISTER":
		if req.Task == "" || req.Address == "" {
			return proxyResult{Status: StatusErr, Error: "task and address required"}
		}
		logger.Printf("REGISTER %s -> %s cap=%d (from %s)", req.Task, req.Address, req.Capacity, source)

		var err error
		if backendProto == "tcp" {
			err = RegisterTCPWithCapacity(serverAddr, req.Task, req.Address, req.Capacity)
		} else {
			err = RegisterUDPWithCapacity(serverAddr, req.Task, req.Address, req.Capacity)
		}
		if err != nil {
			return proxyResult{Status: StatusErr, Error: err.Error()}
		}
		atomic.AddUint64(&regCount, 1)
		return proxyResult{Status: StatusOK}

	case "QUERY":
		if req.Task == "" {
			return proxyResult{Status: StatusErr, Error: "task required"}
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
			return proxyResult{Status: StatusErr, Error: err.Error()}
		}
		atomic.AddUint64(&queryCount, 1)
		if addr == "" {
			return proxyResult{Status: StatusNotFound}
		}
		return proxyResult{Status: StatusOK, Address: addr}
	}

	return proxyResult{Status: StatusErr, Error: "unknown command"}
}

func handleP2PRequest(req proxyRequest, dhtRegistry DHTRegistry, source string) proxyResult {
	switch req.Command {
	case "REGISTER":
		if req.Task == "" || req.Address == "" {
			return proxyResult{Status: StatusErr, Error: "task and address required"}
		}
		logger.Printf("P2P REGISTER %s -> %s (from %s)", req.Task, req.Address, source)
		if err := dhtRegistry.Register(req.Task, req.Address); err != nil {
			return proxyResult{Status: StatusErr, Error: err.Error()}
		}
		atomic.AddUint64(&regCount, 1)
		return proxyResult{Status: StatusOK}

	case "QUERY":
		if req.Task == "" {
			return proxyResult{Status: StatusErr, Error: "task required"}
		}
		logger.Printf("P2P QUERY %s (from %s)", req.Task, source)
		addr, err := dhtRegistry.Query(req.Task)
		if err != nil {
			return proxyResult{Status: StatusErr, Error: err.Error()}
		}
		atomic.AddUint64(&queryCount, 1)
		if addr == "" {
			return proxyResult{Status: StatusNotFound}
		}
		return proxyResult{Status: StatusOK, Address: addr}
	}

	return proxyResult{Status: StatusErr, Error: "unknown command"}
}

// writeTCPResultGeneric sends a result to a TCP client in either JSON or text mode.
func writeTCPResultGeneric(w *bufio.Writer, jsonMode bool, result proxyResult) {
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

	if result.Status == StatusOK {
		if result.Address != "" {
			fmt.Fprintf(w, "%s\n", result.Address)
		} else {
			fmt.Fprint(w, StatusOK+"\n")
		}
		_ = w.Flush()
		return
	}
	if result.Status == StatusNotFound {
		fmt.Fprint(w, StatusNotFound+"\n")
		_ = w.Flush()
		return
	}
	fmt.Fprintf(w, "%s %s\n", StatusErr, result.Error)
	_ = w.Flush()
}
