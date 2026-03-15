package registry

import (
	"math"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"tds/pkg/firewall"
	"time"
)

type MemoryRegistry struct {
	mutex            sync.RWMutex
	services         map[string][]ServiceEntry
	roundRobinIndex  sync.Map // map[string]*atomic.Int64 for lock-free weighted round-robin cursor
	queryCounters    sync.Map // map[string]*atomic.Int64 keyed by "task:address" for lock-free query counting
	parsedDestIPs    sync.Map // map[string]net.IP keyed by "task:address" for parse-once firewall checks
	totalQueries     atomic.Int64
	firewall         *firewall.Firewall
	disableFreshness atomic.Bool // when true, weight = capacity only (no heartbeat age decay)
}

func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{
		services: make(map[string][]ServiceEntry),
		firewall: nil, // No firewall by default
	}
}

// SetDisableFreshness controls whether heartbeat-age freshness decay is applied
// when computing weighted round-robin slot sizes. When true every registered
// instance receives weight equal to its capacity only, giving perfectly even
// round-robin regardless of when each instance last heartbeat'd.
func (registry *MemoryRegistry) SetDisableFreshness(v bool) {
	registry.disableFreshness.Store(v)
}

// SetFirewall configures the firewall rules for this registry.
// If fw is nil, firewall filtering is disabled.
func (registry *MemoryRegistry) SetFirewall(fw *firewall.Firewall) {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	registry.firewall = fw
}

func (registry *MemoryRegistry) Register(task, addr string) {
	task = strings.TrimSpace(task)
	addr = strings.TrimSpace(addr)
	if task == "" || addr == "" {
		return
	}

	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	now := time.Now()
	entries := registry.services[task]
	for i, e := range entries {
		if e.Address == addr {
			// Heartbeat-only update: preserve configured capacity.
			entries[i].LastHeartbeat = now
			registry.services[task] = entries
			registry.cacheParsedDestination(task, addr)
			return
		}
	}

	// New entry via legacy register path defaults to capacity 1.
	newEntry := ServiceEntry{
		Address:       addr,
		LastHeartbeat: now,
		QueryCount:    0,
		Capacity:      1,
	}
	registry.services[task] = append(entries, newEntry)
	registry.roundRobinIndex.LoadOrStore(task, &atomic.Int64{})
	registry.cacheParsedDestination(task, addr)
}

func (registry *MemoryRegistry) RegisterWithCapacity(task, addr string, capacity int) {
	task = strings.TrimSpace(task)
	addr = strings.TrimSpace(addr)
	if task == "" || addr == "" {
		return
	}

	if capacity <= 0 {
		capacity = 1
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	now := time.Now()
	entries := registry.services[task]
	for i, e := range entries {
		if e.Address == addr {
			// update heartbeat
			entries[i].LastHeartbeat = now
			entries[i].Capacity = capacity
			registry.services[task] = entries
			registry.cacheParsedDestination(task, addr)
			return
		}
	}
	// not found -> append
	newEntry := ServiceEntry{
		Address:       addr,
		LastHeartbeat: now,
		QueryCount:    0,
		Capacity:      capacity,
	}
	registry.services[task] = append(entries, newEntry)
	// Ensure a round-robin index exists for the task.
	registry.roundRobinIndex.LoadOrStore(task, &atomic.Int64{})
	registry.cacheParsedDestination(task, addr)
}

func (registry *MemoryRegistry) GetService(task string) (string, error) {
	return registry.GetServiceForRequestor(task, nil)
}

func (registry *MemoryRegistry) GetServiceForRequestor(task string, requestorIP net.IP) (string, error) {
	task = strings.TrimSpace(task)
	if task == "" {
		return "", ErrInvalidTaskName
	}

	now := time.Now()
	noFreshness := registry.disableFreshness.Load()

	registry.mutex.RLock()
	entries, found := registry.services[task]
	if !found || len(entries) == 0 {
		registry.mutex.RUnlock()
		return "", ErrNotFound
	}

	if registry.firewall == nil || requestorIP == nil {
		totalWeight := totalServiceWeightOpts(entries, now, noFreshness)
		if totalWeight <= 0 {
			registry.mutex.RUnlock()
			return "", ErrNotFound
		}

		idxVal, _ := registry.roundRobinIndex.LoadOrStore(task, &atomic.Int64{})
		idxPtr := idxVal.(*atomic.Int64)
		slot := int(idxPtr.Add(1)-1) % totalWeight
		selectedAddr, ok := selectWeightedAddressOpts(entries, now, slot, noFreshness)
		registry.mutex.RUnlock()
		if !ok {
			return "", ErrNotFound
		}
		return registry.recordSelection(task, selectedAddr), nil
	}

	fw := registry.firewall
	entryCopy := make([]ServiceEntry, len(entries))
	copy(entryCopy, entries)
	registry.mutex.RUnlock()

	totalAllowedWeight := 0
	allowedCount := 0
	for _, entry := range entryCopy {
		destIP := registry.getParsedDestination(task, entry.Address)
		if destIP == nil || !fw.IsAllowed(requestorIP, destIP) {
			continue
		}
		allowedCount++
		totalAllowedWeight += serviceWeightOpts(entry, now, noFreshness)
	}
	if allowedCount == 0 {
		return "", ErrNoAllowedService
	}
	if totalAllowedWeight <= 0 {
		return "", ErrNotFound
	}

	idxVal, _ := registry.roundRobinIndex.LoadOrStore(task, &atomic.Int64{})
	idxPtr := idxVal.(*atomic.Int64)
	slot := int(idxPtr.Add(1)-1) % totalAllowedWeight

	running := 0
	for _, entry := range entryCopy {
		destIP := registry.getParsedDestination(task, entry.Address)
		if destIP == nil || !fw.IsAllowed(requestorIP, destIP) {
			continue
		}
		running += serviceWeightOpts(entry, now, noFreshness)
		if slot < running {
			return registry.recordSelection(task, entry.Address), nil
		}
	}

	return "", ErrNotFound
}

func parseDestinationIP(address string) net.IP {
	hostPart, _, err := net.SplitHostPort(address)
	if err != nil {
		hostPart = address
	}

	return net.ParseIP(hostPart)
}

func parsedDestKey(task, address string) string {
	return task + ":" + address
}

func (registry *MemoryRegistry) cacheParsedDestination(task, address string) {
	key := parsedDestKey(task, address)
	destIP := parseDestinationIP(address)
	if destIP == nil {
		registry.parsedDestIPs.Delete(key)
		return
	}
	registry.parsedDestIPs.Store(key, destIP)
}

func (registry *MemoryRegistry) getParsedDestination(task, address string) net.IP {
	key := parsedDestKey(task, address)
	if v, ok := registry.parsedDestIPs.Load(key); ok {
		if ip, ok := v.(net.IP); ok {
			return ip
		}
	}
	destIP := parseDestinationIP(address)
	if destIP != nil {
		registry.parsedDestIPs.Store(key, destIP)
	}
	return destIP
}

func (registry *MemoryRegistry) recordSelection(task, selectedAddr string) string {
	counterKey := task + ":" + selectedAddr
	counterVal, _ := registry.queryCounters.LoadOrStore(counterKey, &atomic.Int64{})
	counterPtr := counterVal.(*atomic.Int64)
	counterPtr.Add(1)

	registry.totalQueries.Add(1)

	return selectedAddr
}

func totalServiceWeight(entries []ServiceEntry, now time.Time) int {
	return totalServiceWeightOpts(entries, now, false)
}

func totalServiceWeightOpts(entries []ServiceEntry, now time.Time, noFreshness bool) int {
	total := 0
	for _, entry := range entries {
		weight := serviceWeightOpts(entry, now, noFreshness)
		total += weight
	}
	return total
}

func selectWeightedAddress(entries []ServiceEntry, now time.Time, slot int) (string, bool) {
	return selectWeightedAddressOpts(entries, now, slot, false)
}

func selectWeightedAddressOpts(entries []ServiceEntry, now time.Time, slot int, noFreshness bool) (string, bool) {
	running := 0
	for _, entry := range entries {
		running += serviceWeightOpts(entry, now, noFreshness)
		if slot < running {
			return entry.Address, true
		}
	}
	return "", false
}

func serviceWeight(entry ServiceEntry, now time.Time) int {
	return serviceWeightOpts(entry, now, false)
}

func serviceWeightOpts(entry ServiceEntry, now time.Time, noFreshness bool) int {
	capacity := entry.Capacity
	if capacity <= 0 {
		capacity = 1
	}

	if noFreshness {
		return capacity
	}

	ageSeconds := now.Sub(entry.LastHeartbeat).Seconds()
	if ageSeconds < 0 {
		ageSeconds = 0
	}

	// Decay weight with heartbeat age so fresher instances receive more traffic.
	// Age 0s => factor 1.0, 30s => 0.5, 60s => 0.33, etc.
	freshness := 1.0 / (1.0 + ageSeconds/30.0)
	effectiveWeight := int(math.Round(float64(capacity) * freshness))
	if effectiveWeight < 1 {
		return 1
	}
	return effectiveWeight
}

func (registry *MemoryRegistry) Cleanup(timeout time.Duration) int {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	now := time.Now()
	removed := 0
	for task, entries := range registry.services {
		kept := make([]ServiceEntry, 0, len(entries))
		for _, e := range entries {
			if e.LastHeartbeat.Add(timeout).After(now) {
				kept = append(kept, e)
			} else {
				counterKey := task + ":" + e.Address
				registry.queryCounters.Delete(counterKey)
				registry.parsedDestIPs.Delete(counterKey)
				removed++
			}
		}
		if len(kept) == 0 {
			delete(registry.services, task)
			registry.roundRobinIndex.Delete(task)
		} else {
			registry.services[task] = kept
		}
	}
	return removed
}

func (registry *MemoryRegistry) ListServices() map[string][]ServiceEntry {
	registry.mutex.RLock() // Read lock allows concurrent access from multiple readers (e.g., TUI)
	defer registry.mutex.RUnlock()

	// CRITICAL: Must return a deep copy to prevent callers from mutating internal slices.
	// Also sync atomic query counts into the returned ServiceEntry structs.
	copyMap := make(map[string][]ServiceEntry)
	for task, entries := range registry.services {
		sliceCopy := make([]ServiceEntry, len(entries))
		for i, entry := range entries {
			sliceCopy[i] = entry
			counterKey := task + ":" + entry.Address
			if counterVal, ok := registry.queryCounters.Load(counterKey); ok {
				counterPtr := counterVal.(*atomic.Int64)
				sliceCopy[i].QueryCount = counterPtr.Load()
			}
		}
		copyMap[task] = sliceCopy
	}
	return copyMap
}

func (registry *MemoryRegistry) GetStats() Stats {
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()
	return Stats{
		TotalQueries: registry.totalQueries.Load(),
		TotalTasks:   len(registry.services),
	}
}
