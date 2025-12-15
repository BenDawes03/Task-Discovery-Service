package registry

import (
	"sync"
	"time"
)

type MemoryRegistry struct {
	mutex           sync.RWMutex
	services        map[string][]ServiceEntry
	roundRobinIndex map[string]int
	totalQueries    int64
}

func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{
		services:        make(map[string][]ServiceEntry),
		roundRobinIndex: make(map[string]int),
	}
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
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	//check service existence

	entries, found := registry.services[task]
	if !found || len(entries) == 0 {
		return "", ErrNotFound
	}
	idx := registry.roundRobinIndex[task]
	if idx >= len(entries) {
		idx = idx % len(entries)
	}
	selected := entries[idx]
	selected.QueryCount++

	registry.services[task][idx] = selected // Update the slice entry

	registry.totalQueries++
	registry.roundRobinIndex[task] = (idx + 1) % len(entries) // Increment and wrap index

	return selected.Address, nil
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
