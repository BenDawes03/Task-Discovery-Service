package client

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// Register sends a REGISTER command to the server. Protocol is selected via
// the TDS_SERVER_PROTO environment variable ("tcp" or "udp", default "udp").
func Register(serverAddr, task, address string) error {
	proto := strings.ToLower(os.Getenv("TDS_SERVER_PROTO"))
	if proto == "tcp" {
		return RegisterTCP(serverAddr, task, address)
	}
	return RegisterUDP(serverAddr, task, address)
}

// RegisterUDP performs a UDP register (legacy behaviour).
func RegisterUDP(serverAddr, task, address string) error {
	conn, err := net.DialTimeout("udp", serverAddr, 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = fmt.Fprintf(conn, "REGISTER %s %s", task, address)
	return err
}

// RegisterTCP performs a TCP register and reads the server response.
func RegisterTCP(serverAddr, task, address string) error {
	conn, err := net.DialTimeout("tcp", serverAddr, 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	fmt.Fprintf(conn, "REGISTER %s %s\n", task, address)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	r := bufio.NewReader(conn)
	resp, err := r.ReadString('\n')
	if err != nil {
		return err
	}
	resp = strings.TrimSpace(resp)
	if resp == "OK" {
		return nil
	}
	return fmt.Errorf("server error: %s", resp)
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

// QueryUDP performs the legacy UDP query.
func QueryUDP(serverAddr, task string) (string, error) {
	conn, err := net.DialTimeout("udp", serverAddr, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_, err = fmt.Fprintf(conn, "QUERY %s", task)
	if err != nil {
		return "", err
	}
	buf := make([]byte, 1024)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		return "", err
	}
	resp := strings.TrimSpace(string(buf[:n]))
	if resp == "NOTFOUND" {
		return "", nil
	}
	return resp, nil
}

// QueryTCP performs the query over TCP and interprets server replies.
func QueryTCP(serverAddr, task string) (string, error) {
	conn, err := net.DialTimeout("tcp", serverAddr, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	fmt.Fprintf(conn, "QUERY %s\n", task)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	r := bufio.NewReader(conn)
	resp, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	resp = strings.TrimSpace(resp)
	if resp == "NOTFOUND" {
		return "", nil
	}
	if resp == "ERR" {
		return "", fmt.Errorf("server error")
	}
	return resp, nil
}
