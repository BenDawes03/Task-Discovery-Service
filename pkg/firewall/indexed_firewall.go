package firewall

import (
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type sourceDestRule struct {
	sourceNet *net.IPNet
	destNets  []*net.IPNet
}

// IndexedFirewall provides fast firewall rule checking using an in-memory source index.
// Exact source IP rules are O(1) hash lookups; CIDR source rules are matched in O(k_cidr).
type IndexedFirewall struct {
	provider RulesProvider

	// mu protects index structures during reload.
	mu sync.RWMutex

	// index maps exact source IP string -> allowed destination networks.
	index map[string][]*net.IPNet

	// cidrRules holds source CIDR rules that cannot be exact-hashed.
	cidrRules []sourceDestRule

	// stats for observability (atomic to avoid lock contention/races on read path).
	totalChecks    atomic.Int64
	totalAllowed   atomic.Int64
	totalBlocked   atomic.Int64
	lastReloadTime atomic.Int64
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
	ifw.mu.Lock()
	if err := ifw.rebuildIndex(); err != nil {
		ifw.mu.Unlock()
		return nil, err
	}
	ifw.mu.Unlock()

	return ifw, nil
}

// rebuildIndex rebuilds the in-memory index from the current set of rules.
// Caller must ensure synchronization if concurrent readers/writers may exist.
func (ifw *IndexedFirewall) rebuildIndex() error {
	rules, err := ifw.provider.GetRules()
	if err != nil {
		return err
	}

	newIndex := make(map[string][]*net.IPNet)
	newCIDR := make([]sourceDestRule, 0)
	cidrPos := make(map[string]int)

	for _, rule := range rules {
		// Normalize destination to network form for unified matching.
		var destNet *net.IPNet
		if rule.DestNetwork != nil {
			destNet = rule.DestNetwork
		} else {
			destNet = ipToNetwork(rule.DestIP)
		}
		if destNet == nil {
			continue
		}

		if rule.SourceNetwork != nil {
			key := rule.SourceNetwork.String()
			idx, ok := cidrPos[key]
			if !ok {
				newCIDR = append(newCIDR, sourceDestRule{sourceNet: rule.SourceNetwork})
				idx = len(newCIDR) - 1
				cidrPos[key] = idx
			}
			newCIDR[idx].destNets = append(newCIDR[idx].destNets, destNet)
			continue
		}

		sourceKey := canonicalIPString(rule.SourceIP)
		if sourceKey == "" {
			continue
		}
		newIndex[sourceKey] = append(newIndex[sourceKey], destNet)
	}

	ifw.index = newIndex
	ifw.cidrRules = newCIDR
	return nil
}

func canonicalIPString(ip net.IP) string {
	if ip == nil {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	if v16 := ip.To16(); v16 != nil {
		return v16.String()
	}
	return ""
}

// ipToNetwork converts a single IP to a /32 or /128 network.
func ipToNetwork(ip net.IP) *net.IPNet {
	if ip == nil {
		return nil
	}
	if ip.To4() != nil {
		return &net.IPNet{
			IP:   ip.To4(),
			Mask: net.CIDRMask(32, 32),
		}
	}
	return &net.IPNet{
		IP:   ip.To16(),
		Mask: net.CIDRMask(128, 128),
	}
}

// IsAllowed checks if communication from sourceIP to destIP is allowed
// according to the firewall rules.
func (ifw *IndexedFirewall) IsAllowed(sourceIP, destIP net.IP) bool {
	ifw.mu.RLock()
	allowed := ifw.isAllowedLocked(sourceIP, destIP)
	ifw.mu.RUnlock()

	ifw.totalChecks.Add(1)
	if allowed {
		ifw.totalAllowed.Add(1)
		return true
	}
	ifw.totalBlocked.Add(1)
	return false
}

func (ifw *IndexedFirewall) isAllowedLocked(sourceIP, destIP net.IP) bool {
	// Empty index means no rules loaded (permissive by default).
	if len(ifw.index) == 0 && len(ifw.cidrRules) == 0 {
		return true
	}

	destNets := ifw.allowedDestinationNetworksLocked(sourceIP)
	for _, destNet := range destNets {
		if destNet.Contains(destIP) {
			return true
		}
	}
	return false
}

func (ifw *IndexedFirewall) allowedDestinationNetworksLocked(sourceIP net.IP) []*net.IPNet {
	if sourceIP == nil {
		return nil
	}

	allowed := make([]*net.IPNet, 0)
	if exact := ifw.index[canonicalIPString(sourceIP)]; len(exact) > 0 {
		allowed = append(allowed, exact...)
	}

	for _, rule := range ifw.cidrRules {
		if rule.sourceNet.Contains(sourceIP) {
			allowed = append(allowed, rule.destNets...)
		}
	}

	return allowed
}

// sourceMatches checks if an IP matches a source key (CIDR notation).
// Kept for backward compatibility with tests/helpers.
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
	// Empty index means no rules loaded (permissive by default)
	if len(ifw.index) == 0 && len(ifw.cidrRules) == 0 {
		ifw.mu.RUnlock()
		return addresses
	}

	allowedDests := ifw.allowedDestinationNetworksLocked(sourceIP)
	ifw.mu.RUnlock()

	if len(allowedDests) == 0 {
		return []string{}
	}

	allowed := make([]string, 0, len(addresses))
	for _, addr := range addresses {
		hostPart, _, err := net.SplitHostPort(addr)
		if err != nil {
			hostPart = addr
		}

		destIP := net.ParseIP(hostPart)
		if destIP == nil {
			continue
		}

		for _, destNet := range allowedDests {
			if destNet.Contains(destIP) {
				allowed = append(allowed, addr)
				break
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

	ifw.lastReloadTime.Store(getCurrentUnixTime())
	return nil
}

// GetStats returns a copy of the current firewall statistics.
func (ifw *IndexedFirewall) GetStats() FirewallStats {
	return FirewallStats{
		TotalChecks:    ifw.totalChecks.Load(),
		TotalAllowed:   ifw.totalAllowed.Load(),
		TotalBlocked:   ifw.totalBlocked.Load(),
		LastReloadTime: ifw.lastReloadTime.Load(),
	}
}

// Close closes the underlying RulesProvider.
func (ifw *IndexedFirewall) Close() error {
	return ifw.provider.Close()
}

// getCurrentUnixTime returns the current time as a Unix timestamp.
func getCurrentUnixTime() int64 {
	return time.Now().Unix()
}
