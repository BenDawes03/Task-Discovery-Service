package registry

import (
	"net"
	"sync"
	"sync/atomic"
	"tds/pkg/firewall"
	"time"
)

type MemoryRegistry struct {
	mutex           sync.RWMutex
	services        map[string][]ServiceEntry
	roundRobinIndex sync.Map // map[string]*atomic.Int64 for lock-free round-robin
	queryCounters   sync.Map // map[string]*atomic.Int64 keyed by "task:address" for lock-free query counting
	totalQueries    atomic.Int64
	firewall        *firewall.Firewall
}

func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{
		services: make(map[string][]ServiceEntry),
		firewall: nil, // No firewall by default
	}
}

// SetFirewall configures the firewall rules for this registry.
// If fw is nil, firewall filtering is disabled.
func (registry *MemoryRegistry) SetFirewall(fw *firewall.Firewall) {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	registry.firewall = fw
}

func (registry *MemoryRegistry) Register(task, addr string) {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	now := time.Now()
	entries := registry.services[task]
	for i, e := range entries {
		if e.Address == addr {
			// update heartbeat
			entries[i].LastHeartbeat = now
			registry.services[task] = entries
			return
		}
	}
	// not found -> append
	newEntry := ServiceEntry{
		Address:       addr,
		LastHeartbeat: now,
		QueryCount:    0,
	}
	registry.services[task] = append(entries, newEntry)
	// Ensure a round-robin index exists for the task.
	registry.roundRobinIndex.LoadOrStore(task, &atomic.Int64{})
}

func (registry *MemoryRegistry) GetService(task string) (string, error) {
	return registry.GetServiceForRequestor(task, nil)
}

func (registry *MemoryRegistry) GetServiceForRequestor(task string, requestorIP net.IP) (string, error) {
	// Copy current addresses under RLock so we don't hold the lock while
	// doing parsing / firewall checks.
	registry.mutex.RLock()
	entries, found := registry.services[task]
	if !found || len(entries) == 0 {
		registry.mutex.RUnlock()
		return "", ErrNotFound
	}
	addrs := make([]string, len(entries))
	for i, e := range entries {
		addrs[i] = e.Address
	}
	registry.mutex.RUnlock()

	// Filter allowed addresses (if firewall is configured and we know requestor IP).
	allowedAddrs := addrs
	if registry.firewall != nil && requestorIP != nil {
		allowedAddrs = allowedAddrs[:0]
		for _, addr := range addrs {
			hostPart, _, err := net.SplitHostPort(addr)
			if err != nil {
				hostPart = addr
			}
			destIP := net.ParseIP(hostPart)
			if destIP != nil && registry.firewall.IsAllowed(requestorIP, destIP) {
				allowedAddrs = append(allowedAddrs, addr)
			}
		}
		if len(allowedAddrs) == 0 {
			return "", ErrNoAllowedService
		}
	}

	// Atomic round-robin selection over the allowed set.
	idxVal, _ := registry.roundRobinIndex.LoadOrStore(task, &atomic.Int64{})
	idxPtr := idxVal.(*atomic.Int64)
	idx := int(idxPtr.Add(1)-1) % len(allowedAddrs)
	selectedAddr := allowedAddrs[idx]

	// Increment query count atomically (lock-free).
	counterKey := task + ":" + selectedAddr
	counterVal, _ := registry.queryCounters.LoadOrStore(counterKey, &atomic.Int64{})
	counterPtr := counterVal.(*atomic.Int64)
	counterPtr.Add(1)

	// Increment total queries atomically (lock-free).
	registry.totalQueries.Add(1)

	return selectedAddr, nil
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
				removed++
			}
		}
		if len(kept) == 0 {
			delete(registry.services, task)
			registry.roundRobinIndex.Delete(task)
		} else {
			registry.services[task] = kept
			// Reset round-robin index if it's out of bounds
			if idxVal, ok := registry.roundRobinIndex.Load(task); ok {
				idxPtr := idxVal.(*atomic.Int64)
				if int(idxPtr.Load()) >= len(kept) {
					idxPtr.Store(0)
				}
			}
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
