package firewall

import (
	"net"
	"os"
	"testing"
)

// TestFileRulesProvider tests the FileRulesProvider implementation
func TestFileRulesProvider(t *testing.T) {
	// Create a temporary rules file
	tmpFile, err := os.CreateTemp("", "firewall_rules_*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write some test rules
	_, err = tmpFile.WriteString(`# Test firewall rules
192.168.1.10 10.0.0.5
192.168.1.0/24 10.0.0.0/24
`)
	if err != nil {
		t.Fatalf("Failed to write rules: %v", err)
	}
	tmpFile.Close()

	// Load provider
	provider, err := NewFileRulesProvider(tmpFile.Name())
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}
	defer provider.Close()

	// Get rules
	rules, err := provider.GetRules()
	if err != nil {
		t.Fatalf("Failed to get rules: %v", err)
	}

	if len(rules) != 2 {
		t.Errorf("Expected 2 rules, got %d", len(rules))
	}

	// Verify first rule
	if !rules[0].SourceIP.Equal(net.ParseIP("192.168.1.10")) {
		t.Errorf("First rule source IP mismatch")
	}
	if !rules[0].DestIP.Equal(net.ParseIP("10.0.0.5")) {
		t.Errorf("First rule dest IP mismatch")
	}

	// Verify second rule
	if rules[1].SourceNetwork == nil {
		t.Errorf("Second rule should have source network")
	}
	if rules[1].DestNetwork == nil {
		t.Errorf("Second rule should have dest network")
	}
}

// TestIndexedFirewall tests the IndexedFirewall implementation
func TestIndexedFirewall(t *testing.T) {
	// Create a temporary rules file
	tmpFile, err := os.CreateTemp("", "firewall_rules_*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write test rules
	tmpFile.WriteString(`
192.168.1.10 10.0.0.5
192.168.1.0/24 10.0.0.0/24
`)
	tmpFile.Close()

	// Create provider and indexed firewall
	provider, _ := NewFileRulesProvider(tmpFile.Name())
	defer provider.Close()

	fw, err := NewIndexedFirewall(provider)
	if err != nil {
		t.Fatalf("Failed to create indexed firewall: %v", err)
	}
	defer fw.Close()

	tests := []struct {
		name     string
		sourceIP string
		destIP   string
		expected bool
	}{
		{
			name:     "Exact match",
			sourceIP: "192.168.1.10",
			destIP:   "10.0.0.5",
			expected: true,
		},
		{
			name:     "CIDR match",
			sourceIP: "192.168.1.50",
			destIP:   "10.0.0.50",
			expected: true,
		},
		{
			name:     "No match",
			sourceIP: "172.16.0.1",
			destIP:   "10.0.0.5",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srcIP := net.ParseIP(tt.sourceIP)
			dstIP := net.ParseIP(tt.destIP)
			result := fw.IsAllowed(srcIP, dstIP)

			if result != tt.expected {
				t.Errorf("IsAllowed(%s, %s) = %v, want %v",
					tt.sourceIP, tt.destIP, result, tt.expected)
			}
		})
	}
}

// TestIndexedFirewallFilterAddresses tests address filtering
func TestIndexedFirewallFilterAddresses(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "firewall_rules_*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	tmpFile.WriteString(`
192.168.1.10 10.0.0.5
192.168.1.10 10.0.0.6
`)
	tmpFile.Close()

	provider, _ := NewFileRulesProvider(tmpFile.Name())
	defer provider.Close()

	fw, _ := NewIndexedFirewall(provider)
	defer fw.Close()

	sourceIP := net.ParseIP("192.168.1.10")
	addresses := []string{"10.0.0.5:8080", "10.0.0.6:8080", "172.16.0.100:8080"}

	filtered := fw.FilterAddresses(sourceIP, addresses)

	if len(filtered) != 2 {
		t.Errorf("Expected 2 allowed addresses, got %d", len(filtered))
	}

	expectedAddrs := map[string]bool{"10.0.0.5:8080": true, "10.0.0.6:8080": true}
	for _, addr := range filtered {
		if !expectedAddrs[addr] {
			t.Errorf("Unexpected address in filtered list: %s", addr)
		}
	}
}

// TestIndexedFirewallStats tests statistics tracking
func TestIndexedFirewallStats(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "firewall_rules_*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	tmpFile.WriteString("192.168.1.10 10.0.0.5\n")
	tmpFile.Close()

	provider, _ := NewFileRulesProvider(tmpFile.Name())
	defer provider.Close()

	fw, _ := NewIndexedFirewall(provider)
	defer fw.Close()

	srcIP := net.ParseIP("192.168.1.10")
	dstIP := net.ParseIP("10.0.0.5")
	otherIP := net.ParseIP("172.16.0.1")

	// Make some queries
	fw.IsAllowed(srcIP, dstIP)   // Allow
	fw.IsAllowed(otherIP, dstIP) // Block
	fw.IsAllowed(srcIP, otherIP) // Block

	stats := fw.GetStats()

	if stats.TotalChecks != 3 {
		t.Errorf("Expected 3 total checks, got %d", stats.TotalChecks)
	}
	if stats.TotalAllowed != 1 {
		t.Errorf("Expected 1 allowed, got %d", stats.TotalAllowed)
	}
	if stats.TotalBlocked != 2 {
		t.Errorf("Expected 2 blocked, got %d", stats.TotalBlocked)
	}
}

// TestIndexedFirewallEmptyRules tests with no rules (should allow all)
func TestIndexedFirewallEmptyRules(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "firewall_rules_*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Empty file
	tmpFile.Close()

	provider, _ := NewFileRulesProvider(tmpFile.Name())
	defer provider.Close()

	fw, _ := NewIndexedFirewall(provider)
	defer fw.Close()

	// With no rules, everything should be allowed (permissive mode)
	result := fw.IsAllowed(net.ParseIP("192.168.1.1"), net.ParseIP("10.0.0.1"))
	if !result {
		t.Errorf("Empty rules should allow all connections")
	}
}

// ---------------------------------------------------------------------------
// canonicalIPString
// ---------------------------------------------------------------------------

func TestCanonicalIPString(t *testing.T) {
	cases := []struct {
		in   net.IP
		want string
	}{
		{nil, ""},
		{net.ParseIP("10.0.0.1"), "10.0.0.1"},
		// IPv4-mapped IPv6 → should return IPv4 string.
		{net.ParseIP("::ffff:10.0.0.1"), "10.0.0.1"},
		// Pure IPv6
		{net.ParseIP("2001:db8::1"), "2001:db8::1"},
	}

	for _, tc := range cases {
		got := canonicalIPString(tc.in)
		if got != tc.want {
			t.Errorf("canonicalIPString(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// allowedDestinationNetworksLocked – nil source IP
// ---------------------------------------------------------------------------

func TestAllowedDestinationNetworksLockedNilSource(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "fw_nil_*.txt")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString("192.168.1.1 10.0.0.1\n")
	tmpFile.Close()

	provider, _ := NewFileRulesProvider(tmpFile.Name())
	defer provider.Close()
	fw, _ := NewIndexedFirewall(provider)
	defer fw.Close()

	fw.mu.RLock()
	nets := fw.allowedDestinationNetworksLocked(nil)
	fw.mu.RUnlock()

	if len(nets) != 0 {
		t.Errorf("expected nil source to return empty slice, got %v", nets)
	}
}

// ---------------------------------------------------------------------------
// ipToNetwork edge cases
// ---------------------------------------------------------------------------

func TestIpToNetworkNilReturnsNil(t *testing.T) {
	if got := ipToNetwork(nil); got != nil {
		t.Errorf("expected nil for nil IP, got %v", got)
	}
}

func TestIpToNetworkIPv4(t *testing.T) {
	ip := net.ParseIP("10.0.0.1")
	n := ipToNetwork(ip)
	if n == nil {
		t.Fatal("expected non-nil network for IPv4 IP")
	}
	ones, bits := n.Mask.Size()
	if ones != 32 || bits != 32 {
		t.Errorf("expected /32 mask for IPv4, got /%d", ones)
	}
}

func TestIpToNetworkIPv6(t *testing.T) {
	ip := net.ParseIP("2001:db8::1")
	n := ipToNetwork(ip)
	if n == nil {
		t.Fatal("expected non-nil network for IPv6 IP")
	}
	ones, bits := n.Mask.Size()
	if ones != 128 || bits != 128 {
		t.Errorf("expected /128 mask for IPv6, got /%d", ones)
	}
}
