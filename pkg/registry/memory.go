package registry

import (
	"net"
	"sync"
	"tds/pkg/firewall"
	"time"
)

type MemoryRegistry struct {
	mutex           sync.RWMutex
	services        map[string][]ServiceEntry
	roundRobinIndex map[string]int
	totalQueries    int64
	firewall        *firewall.Firewall
}

func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{
		services:        make(map[string][]ServiceEntry),
		roundRobinIndex: make(map[string]int),
		firewall:        nil, // No firewall by default
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
	// ensure round robin index exists
	if _, ok := registry.roundRobinIndex[task]; !ok {
		registry.roundRobinIndex[task] = 0
	}
}

func (registry *MemoryRegistry) GetService(task string) (string, error) {
	return registry.GetServiceForRequestor(task, nil)
}

func (registry *MemoryRegistry) GetServiceForRequestor(task string, requestorIP net.IP) (string, error) {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()

	entries, found := registry.services[task]
	if !found || len(entries) == 0 {
		return "", ErrNotFound
	}

	// If firewall is configured and requestorIP is provided, filter entries
	var allowedEntries []int // indices of entries allowed by firewall
	if registry.firewall != nil && requestorIP != nil {
		for i, entry := range entries {
			// Extract IP from entry address (handle "ip:port" format)
			hostPart, _, err := net.SplitHostPort(entry.Address)
			if err != nil {
				// No port, treat the whole string as IP
				hostPart = entry.Address
			}
			
			destIP := net.ParseIP(hostPart)
			if destIP != nil && registry.firewall.IsAllowed(requestorIP, destIP) {
				allowedEntries = append(allowedEntries, i)
			}
		}
		
		if len(allowedEntries) == 0 {
			return "", ErrNoAllowedService
		}
	} else {
		// No firewall or no requestor IP, all entries are allowed
		allowedEntries = make([]int, len(entries))
		for i := range entries {
			allowedEntries[i] = i
		}
	}

	// Round-robin selection from allowed entries
	idx := registry.roundRobinIndex[task]
	if idx >= len(entries) {
		idx = idx % len(entries)
	}
	
	// Find the next allowed entry starting from idx
	attempts := 0
	for attempts < len(entries) {
		// Check if current idx is in allowedEntries
		for _, allowedIdx := range allowedEntries {
			if allowedIdx == idx {
				// Found an allowed entry
				selected := entries[idx]
				selected.QueryCount++
				registry.services[task][idx] = selected
				registry.totalQueries++
				registry.roundRobinIndex[task] = (idx + 1) % len(entries)
				return selected.Address, nil
			}
		}
		// Not allowed, try next
		idx = (idx + 1) % len(entries)
		attempts++
	}

	// Should not reach here if allowedEntries is not empty
	return "", ErrNoAllowedService
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
			delete(registry.roundRobinIndex, task)
		} else {
			registry.services[task] = kept
			// clamp roundRobinIndex
			if idx, ok := registry.roundRobinIndex[task]; ok {
				if idx >= len(kept) {
					registry.roundRobinIndex[task] = idx % len(kept)
				}
			}
		}
	}
	return removed
}

func (registry *MemoryRegistry) ListServices() map[string][]ServiceEntry {
	registry.mutex.RLock() // Read lock allows concurrent access from multiple readers (e.g., TUI)
	defer registry.mutex.RUnlock()

	// CRITICAL: Must return a copy to prevent the UI from modifying the underlying data (race condition)
	copyMap := make(map[string][]ServiceEntry)
	for k, v := range registry.services {
		sliceCopy := make([]ServiceEntry, len(v))
		copy(sliceCopy, v)
		copyMap[k] = sliceCopy
	}
	return copyMap
}

func (registry *MemoryRegistry) GetStats() Stats {
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()
	return Stats{
		TotalQueries: registry.totalQueries,
		TotalTasks:   len(registry.services),
	}
}
