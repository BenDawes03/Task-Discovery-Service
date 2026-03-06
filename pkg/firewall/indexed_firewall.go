package firewall

import (
	"net"
	"sync"
	"time"
)

// IndexedFirewall provides fast firewall rule checking using an in-memory index.
// Rules are organized by source IP/CIDR for O(1) hash lookup, followed by O(k)
// network checks where k is the number of rules for that source.
// It uses a RulesProvider to load rules, which can be a file or database.
type IndexedFirewall struct {
	provider RulesProvider

	// mu protects the index and stats
	mu sync.RWMutex

	// index maps source CIDR strings to their allowed destination networks
	// Key format: "192.168.1.0/24" or "192.168.1.10" (single IPs stored as /32)
	index map[string][]*net.IPNet

	// stats for observability
	stats FirewallStats
}

// FirewallStats tracks firewall decision statistics
type FirewallStats struct {
	TotalChecks    int64
	TotalAllowed   int64
	TotalBlocked   int64
	LastReloadTime int64 // Unix timestamp
}

// NewIndexedFirewall creates a new IndexedFirewall with the given RulesProvider.
// It builds the index immediately from the provider's rules.
func NewIndexedFirewall(provider RulesProvider) (*IndexedFirewall, error) {
	ifw := &IndexedFirewall{
		provider: provider,
		index:    make(map[string][]*net.IPNet),
	}

	// Build the initial index
	if err := ifw.rebuildIndex(); err != nil {
		return nil, err
	}

	return ifw, nil
}

// rebuildIndex rebuilds the in-memory index from the current set of rules.
// Must be called with the write lock held.
func (ifw *IndexedFirewall) rebuildIndex() error {
	rules, err := ifw.provider.GetRules()
	if err != nil {
		return err
	}

	newIndex := make(map[string][]*net.IPNet)

	for _, rule := range rules {
		// Get the source key (CIDR string or /32)
		sourceKey := ifw.getSourceKey(rule)

		// Get destination network
		var destNet *net.IPNet
		if rule.DestNetwork != nil {
			destNet = rule.DestNetwork
		} else {
			// Convert single IP to /32 or /128
			destNet = ipToNetwork(rule.DestIP)
		}

		newIndex[sourceKey] = append(newIndex[sourceKey], destNet)
	}

	ifw.index = newIndex
	return nil
}

// getSourceKey returns the index key for a rule's source.
// Single IPs are converted to /32 or /128 notation for consistent indexing.
func (ifw *IndexedFirewall) getSourceKey(rule FirewallRule) string {
	if rule.SourceNetwork != nil {
		return rule.SourceNetwork.String()
	}
	return ipToNetwork(rule.SourceIP).String()
}

// ipToNetwork converts a single IP to a /32 or /128 network.
func ipToNetwork(ip net.IP) *net.IPNet {
	if ip.To4() != nil {
		return &net.IPNet{
			IP:   ip,
			Mask: net.CIDRMask(32, 32),
		}
	}
	return &net.IPNet{
		IP:   ip,
		Mask: net.CIDRMask(128, 128),
	}
}

// IsAllowed checks if communication from sourceIP to destIP is allowed
// according to the firewall rules.
func (ifw *IndexedFirewall) IsAllowed(sourceIP, destIP net.IP) bool {
	ifw.mu.RLock()
	defer ifw.mu.RUnlock()

	// Empty index means no rules loaded (permissive by default)
	if len(ifw.index) == 0 {
		ifw.stats.TotalChecks++
		ifw.stats.TotalAllowed++
		return true
	}

	// Try to find a matching rule by iterating index keys
	// This is O(n) in the number of source rules, but typically much smaller
	// than total number of rules since they're grouped by source.
	for sourceKey, destNets := range ifw.index {
		// Check if sourceIP matches this key
		if ifw.sourceMatches(sourceKey, sourceIP) {
			// Check if destIP is in any of this source's allowed destinations
			for _, destNet := range destNets {
				if destNet.Contains(destIP) {
					ifw.stats.TotalChecks++
					ifw.stats.TotalAllowed++
					return true
				}
			}
		}
	}

	ifw.stats.TotalChecks++
	ifw.stats.TotalBlocked++
	return false
}

// sourceMatches checks if an IP matches a source key (CIDR notation).
func (ifw *IndexedFirewall) sourceMatches(sourceKey string, sourceIP net.IP) bool {
	_, network, err := net.ParseCIDR(sourceKey)
	if err != nil {
		// Should not happen if index is built correctly
		return false
	}
	return network.Contains(sourceIP)
}

// FilterAddresses filters a list of addresses, returning only those that the
// sourceIP is allowed to communicate with according to the firewall rules.
func (ifw *IndexedFirewall) FilterAddresses(sourceIP net.IP, addresses []string) []string {
	ifw.mu.RLock()
	defer ifw.mu.RUnlock()

	// Empty index means no rules loaded (permissive by default)
	if len(ifw.index) == 0 {
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

		// Check in-memory index (without updating stats)
		allowed = ifw.filterAddressHelper(sourceIP, destIP, allowed, addr)
	}

	return allowed
}

// filterAddressHelper is a helper for FilterAddresses that checks a single address.
// Called with read lock held.
func (ifw *IndexedFirewall) filterAddressHelper(sourceIP, destIP net.IP, allowed []string, addr string) []string {
	for sourceKey, destNets := range ifw.index {
		if ifw.sourceMatches(sourceKey, sourceIP) {
			for _, destNet := range destNets {
				if destNet.Contains(destIP) {
					return append(allowed, addr)
				}
			}
		}
	}
	return allowed
}

// Reload reloads rules from the provider and rebuilds the index.
// This is useful when the underlying data source has changed.
func (ifw *IndexedFirewall) Reload() error {
	ifw.mu.Lock()
	defer ifw.mu.Unlock()

	if err := ifw.rebuildIndex(); err != nil {
		return err
	}

	ifw.stats.LastReloadTime = getCurrentUnixTime()
	return nil
}

// GetStats returns a copy of the current firewall statistics.
func (ifw *IndexedFirewall) GetStats() FirewallStats {
	ifw.mu.RLock()
	defer ifw.mu.RUnlock()

	return ifw.stats
}

// Close closes the underlying RulesProvider.
func (ifw *IndexedFirewall) Close() error {
	return ifw.provider.Close()
}

// getCurrentUnixTime returns the current time as a Unix timestamp.
func getCurrentUnixTime() int64 {
	return time.Now().Unix()
}
