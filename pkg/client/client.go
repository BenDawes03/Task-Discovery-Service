package client

import (
	"fmt"
	"net"
	"time"
)

// Register sends a REGISTER command to the UDP server. serverAddr like "127.0.0.1:5000".
func Register(serverAddr, task, address string) error {
	conn, err := net.DialTimeout("udp", serverAddr, 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = fmt.Fprintf(conn, "REGISTER %s %s", task, address)
	return err
}

// Query asks the server for a task; returns the address or an empty string on not found.
func Query(serverAddr, task string) (string, error) {
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
	return string(buf[:n]), nil
}
