package transport

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"
)

// discovery port used for server announcements
const discoveryPort = 5001

// BroadcastServerInfo sends a JSON announcement about the server via UDP broadcast.
// It does a few repeated sends to increase likelihood of delivery on boot.
func BroadcastServerInfo(listenPort int, heartbeatTimeout time.Duration, startTime time.Time) {
	ip := getLocalIPv4()
	announcement := map[string]interface{}{
		"type":                      "tds_server",
		"address":                   ip,
		"port":                      listenPort,
		"start_time":                startTime.Format(time.RFC3339),
		"heartbeat_timeout_seconds": int(heartbeatTimeout.Seconds()),
	}
	data, err := json.Marshal(announcement)
	if err != nil {
		fmt.Fprintln(os.Stderr, "broadcast: marshal error:", err)
		return
	}

	// dial the IPv4 broadcast address
	baddr := &net.UDPAddr{IP: net.IPv4bcast, Port: discoveryPort}
	conn, err := net.DialUDP("udp4", nil, baddr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "broadcast: dial error:", err)
		return
	}
	defer conn.Close()

	// enable SO_BROADCAST on the socket so writes to the broadcast address succeed
	if err := enableBroadcast(conn); err != nil {
		fmt.Fprintln(os.Stderr, "broadcast: enable broadcast:", err)
	}

	// send the announcement a few times
	for i := 0; i < 3; i++ {
		if _, err := conn.Write(data); err != nil {
			fmt.Fprintln(os.Stderr, "broadcast: write error:", err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	fmt.Fprintln(os.Stderr, "broadcast: sent announcement to", baddr.IP.String(), baddr.Port)
}

// getLocalIPv4 returns the first non-loopback IPv4 address found, or 0.0.0.0.
func getLocalIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "0.0.0.0"
	}
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
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			ip = ip.To4()
			if ip == nil {
				continue
			}
			return ip.String()
		}
	}
	return "0.0.0.0"
}
