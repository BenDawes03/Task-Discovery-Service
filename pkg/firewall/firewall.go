package firewall

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
)

// FirewallRule represents a rule that allows communication from a source to a destination
type FirewallRule struct {
	SourceIP      net.IP
	SourceNetwork *net.IPNet
	DestIP        net.IP
	DestNetwork   *net.IPNet
}

// Firewall manages firewall rules and provides filtering capabilities
type Firewall struct {
	rules []FirewallRule
}

// NewFirewall creates a new empty Firewall
func NewFirewall() *Firewall {
	return &Firewall{
		rules: make([]FirewallRule, 0),
	}
}

// LoadFromFile loads firewall rules from a file.
// The file format is one rule per line:
//   source_ip_or_cidr destination_ip_or_cidr
// Lines starting with # are comments and are ignored.
// Empty lines are ignored.
// Example:
//   192.168.1.10 10.0.0.5
//   192.168.1.0/24 10.0.0.0/24
//   # Allow client subnet to access server subnet
func LoadFromFile(filePath string) (*Firewall, error) {
	fw := NewFirewall()
	
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open firewall rules file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		
		parts := strings.Fields(line)
		if len(parts) != 2 {
			return nil, fmt.Errorf("line %d: expected 2 fields (source dest), got %d", lineNum, len(parts))
		}
		
		rule, err := parseRule(parts[0], parts[1])
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNum, err)
		}
		
		fw.rules = append(fw.rules, rule)
	}
	
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading firewall rules: %w", err)
	}
	
	return fw, nil
}

// parseRule parses a source and destination string into a FirewallRule.
// Each string can be either an IP address or CIDR notation.
func parseRule(sourceStr, destStr string) (FirewallRule, error) {
	rule := FirewallRule{}
	
	// Parse source
	if strings.Contains(sourceStr, "/") {
		_, network, err := net.ParseCIDR(sourceStr)
		if err != nil {
			return rule, fmt.Errorf("invalid source CIDR %s: %w", sourceStr, err)
		}
		rule.SourceNetwork = network
	} else {
		ip := net.ParseIP(sourceStr)
		if ip == nil {
			return rule, fmt.Errorf("invalid source IP %s", sourceStr)
		}
		rule.SourceIP = ip
	}
	
	// Parse destination
	if strings.Contains(destStr, "/") {
		_, network, err := net.ParseCIDR(destStr)
		if err != nil {
			return rule, fmt.Errorf("invalid destination CIDR %s: %w", destStr, err)
		}
		rule.DestNetwork = network
	} else {
		ip := net.ParseIP(destStr)
		if ip == nil {
			return rule, fmt.Errorf("invalid destination IP %s", destStr)
		}
		rule.DestIP = ip
	}
	
	return rule, nil
}

// IsAllowed checks if communication from sourceIP to destIP is allowed by the firewall rules.
// Returns true if any rule allows the communication, false otherwise.
// If there are no rules loaded, returns true (permissive by default).
func (fw *Firewall) IsAllowed(sourceIP, destIP net.IP) bool {
	// If no rules are loaded, allow everything (permissive mode)
	if len(fw.rules) == 0 {
		return true
	}
	
	// Check each rule to see if it allows this communication
	for _, rule := range fw.rules {
		if fw.matchesSource(rule, sourceIP) && fw.matchesDest(rule, destIP) {
			return true
		}
	}
	
	return false
}

// FilterAddresses filters a list of addresses, returning only those that the sourceIP
// is allowed to communicate with according to the firewall rules.
// Each address string should be in the format "ip:port" or just "ip".
func (fw *Firewall) FilterAddresses(sourceIP net.IP, addresses []string) []string {
	if len(fw.rules) == 0 {
		// No rules, allow everything
		return addresses
	}
	
	allowed := make([]string, 0, len(addresses))
	for _, addr := range addresses {
		// Extract IP from address (handle "ip:port" format)
		hostPart, _, err := net.SplitHostPort(addr)
		if err != nil {
			// No port, treat the whole string as IP
			hostPart = addr
		}
		
		destIP := net.ParseIP(hostPart)
		if destIP == nil {
			// Invalid IP, skip it
			continue
		}
		
		if fw.IsAllowed(sourceIP, destIP) {
			allowed = append(allowed, addr)
		}
	}
	
	return allowed
}

func (fw *Firewall) matchesSource(rule FirewallRule, sourceIP net.IP) bool {
	if rule.SourceNetwork != nil {
		return rule.SourceNetwork.Contains(sourceIP)
	}
	return rule.SourceIP.Equal(sourceIP)
}

func (fw *Firewall) matchesDest(rule FirewallRule, destIP net.IP) bool {
	if rule.DestNetwork != nil {
		return rule.DestNetwork.Contains(destIP)
	}
	return rule.DestIP.Equal(destIP)
}

// RuleCount returns the number of rules loaded
func (fw *Firewall) RuleCount() int {
	return len(fw.rules)
}
