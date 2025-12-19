package client

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

var logger = log.New(os.Stdout, "[client-proxy] ", log.LstdFlags)

// atomic counters
var (
	regCount   uint64
	queryCount uint64
	errorCount uint64
)

// RunProxy starts a UDP proxy that listens on listenAddr (e.g. ":5100")
// and forwards REGISTER/QUERY commands to the configured serverAddr.
// It respects ctx cancellation and will exit when ctx is done.
func RunProxy(ctx context.Context, listenAddr string) error {
	serverAddr := os.Getenv("TDS_SERVER_ADDR")
	if serverAddr == "" {
		serverAddr = "127.0.0.1:5000"
	}

	udpAddr, err := net.ResolveUDPAddr("udp", listenAddr)
	if err != nil {
		return fmt.Errorf("resolve listen addr: %w", err)
	}
	conn, err := net.ListenUDP("udp", udpAddr)
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
		go handlePacket(conn, addr, data, serverAddr)
	}
}

func handlePacket(conn *net.UDPConn, src *net.UDPAddr, data string, serverAddr string) {
	parts := strings.Fields(data)
	if len(parts) == 0 {
		writeUDP(conn, src, "ERR empty command")
		atomic.AddUint64(&errorCount, 1)
		return
	}
	cmd := strings.ToUpper(parts[0])
	switch cmd {
	case "REGISTER":
		if len(parts) < 3 {
			writeUDP(conn, src, "ERR usage: REGISTER <task> <address>")
			atomic.AddUint64(&errorCount, 1)
			return
		}
		task := parts[1]
		address := parts[2]
		logger.Printf("REGISTER %s -> %s (from %s)", task, address, src.String())
		if err := Register(serverAddr, task, address); err != nil {
			writeUDP(conn, src, "ERR "+err.Error())
			atomic.AddUint64(&errorCount, 1)
			return
		}
		atomic.AddUint64(&regCount, 1)
		writeUDP(conn, src, "OK")
	case "QUERY":
		if len(parts) < 2 {
			writeUDP(conn, src, "ERR usage: QUERY <task>")
			atomic.AddUint64(&errorCount, 1)
			return
		}
		task := parts[1]
		logger.Printf("QUERY %s (from %s)", task, src.String())
		addr, err := Query(serverAddr, task)
		if err != nil {
			writeUDP(conn, src, "ERR "+err.Error())
			atomic.AddUint64(&errorCount, 1)
			return
		}
		atomic.AddUint64(&queryCount, 1)
		if addr == "" {
			writeUDP(conn, src, "NOTFOUND")
			return
		}
		writeUDP(conn, src, addr)
	default:
		writeUDP(conn, src, "ERR unknown command")
		atomic.AddUint64(&errorCount, 1)
	}
}

func writeUDP(conn *net.UDPConn, addr *net.UDPAddr, msg string) {
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err := conn.WriteToUDP([]byte(msg), addr)
	if err != nil {
		logger.Printf("write udp to %s: %v", addr.String(), err)
		atomic.AddUint64(&errorCount, 1)
	}
}

// Stats returns a simple snapshot of proxy counters.
func Stats() (regs, queries, errs uint64) {
	regs = atomic.LoadUint64(&regCount)
	queries = atomic.LoadUint64(&queryCount)
	errs = atomic.LoadUint64(&errorCount)
	return
}
