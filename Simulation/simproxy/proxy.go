package simproxy

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

var ErrNotFound = errors.New("service not found")

type Client struct {
	Addr    string
	Proto   string // "udp" or "tcp"
	Timeout time.Duration
}

// EnsureHTTPBase normalizes an address into an HTTP base URL.
// If the address already includes a scheme (http/https), it's returned as-is.
// Otherwise it is treated as host:port and prefixed with http://.
func EnsureHTTPBase(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return addr
	}
	return "http://" + addr
}

// DeriveHTTPAdvertise returns an HTTP base URL suitable for service registration.
//
// If listenAddr already has http/https scheme, it's returned as-is.
// Otherwise it treats listenAddr as host:port. If host is empty or a wildcard
// address (":<port>", "0.0.0.0:<port>", "[::]:<port>"), it tries to pick a
// real non-loopback IP address from active network interfaces.
//
// If no non-loopback IP can be found, it falls back to http://localhost:<port>.
func DeriveHTTPAdvertise(listenAddr string) string {
	listenAddr = strings.TrimSpace(listenAddr)
	if listenAddr == "" {
		return ""
	}
	if strings.HasPrefix(listenAddr, "http://") || strings.HasPrefix(listenAddr, "https://") {
		return listenAddr
	}

	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		// Best effort: treat it as already-advertisable.
		return EnsureHTTPBase(listenAddr)
	}

	host = strings.TrimSpace(host)
	if host == "" || host == "0.0.0.0" || host == "::" {
		ip := firstNonLoopbackIP()
		if ip != "" {
			host = ip
		} else {
			host = "localhost"
		}
	}

	// Bracket IPv6.
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("http://%s:%s", host, port)
}

func firstNonLoopbackIP() string {
	// Allow explicit override (useful when multiple NICs exist).
	if v := strings.TrimSpace(os.Getenv("SIM_ADVERTISE_IP")); v != "" {
		return v
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	var v6Candidate string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			default:
				continue
			}
			if ip == nil {
				continue
			}
			if ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() {
				continue
			}
			if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
				continue
			}
			if !ip.IsGlobalUnicast() {
				continue
			}

			if ip4 := ip.To4(); ip4 != nil {
				return ip4.String()
			}
			// Keep first global v6 as fallback if no v4 exists.
			if v6Candidate == "" {
				v6Candidate = ip.String()
			}
		}
	}
	return v6Candidate
}

func (c Client) normalized() Client {
	out := c
	out.Addr = strings.TrimSpace(out.Addr)
	out.Proto = strings.ToLower(strings.TrimSpace(out.Proto))
	if out.Proto == "" {
		out.Proto = "udp"
	}
	if out.Timeout <= 0 {
		out.Timeout = 5 * time.Second
	}
	if out.Addr == "" {
		out.Addr = "127.0.0.1:5100"
	}
	return out
}

func (c Client) Register(task, address string) error {
	c = c.normalized()
	task = strings.TrimSpace(task)
	address = strings.TrimSpace(address)
	if task == "" || address == "" {
		return fmt.Errorf("missing task or address")
	}
	cmd := fmt.Sprintf("REGISTER %s %s", task, address)
	resp, err := c.roundTrip(cmd)
	if err != nil {
		return err
	}
	if resp == "OK" {
		return nil
	}
	if strings.HasPrefix(resp, "ERR ") {
		return errors.New(strings.TrimSpace(strings.TrimPrefix(resp, "ERR ")))
	}
	return fmt.Errorf("unexpected proxy response: %q", resp)
}

func (c Client) Query(task string) (string, error) {
	c = c.normalized()
	task = strings.TrimSpace(task)
	if task == "" {
		return "", fmt.Errorf("missing task")
	}
	cmd := fmt.Sprintf("QUERY %s", task)
	resp, err := c.roundTrip(cmd)
	if err != nil {
		return "", err
	}
	if resp == "NOTFOUND" {
		return "", ErrNotFound
	}
	if strings.HasPrefix(resp, "ERR ") {
		return "", errors.New(strings.TrimSpace(strings.TrimPrefix(resp, "ERR ")))
	}
	return strings.TrimSpace(resp), nil
}

func (c Client) roundTrip(cmd string) (string, error) {
	if c.Proto == "tcp" {
		return c.roundTripTCP(cmd)
	}
	return c.roundTripUDP(cmd)
}

func (c Client) roundTripUDP(cmd string) (string, error) {
	raddr, err := net.ResolveUDPAddr("udp", c.Addr)
	if err != nil {
		return "", fmt.Errorf("resolve proxy addr: %w", err)
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return "", fmt.Errorf("dial proxy udp: %w", err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(c.Timeout))
	if _, err := conn.Write([]byte(cmd)); err != nil {
		return "", fmt.Errorf("write udp: %w", err)
	}

	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil {
		return "", fmt.Errorf("read udp: %w", err)
	}
	return strings.TrimSpace(string(buf[:n])), nil
}

func (c Client) roundTripTCP(cmd string) (string, error) {
	conn, err := net.DialTimeout("tcp", c.Addr, c.Timeout)
	if err != nil {
		return "", fmt.Errorf("dial proxy tcp: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(c.Timeout))

	if !strings.HasSuffix(cmd, "\n") {
		cmd += "\n"
	}
	if _, err := conn.Write([]byte(cmd)); err != nil {
		return "", fmt.Errorf("write tcp: %w", err)
	}

	r := bufio.NewReader(conn)
	line, err := r.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read tcp: %w", err)
	}
	return strings.TrimSpace(line), nil
}
