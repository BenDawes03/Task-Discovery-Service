package registry

import (
	"sync"
	"sync/atomic"
	"time"
)

type MemoryRegistry struct {
	mutex           sync.RWMutex
	services        map[string][]ServiceEntry
	roundRobinIndex sync.Map // map[string]*atomic.Int64 for lock-free round-robin
	totalQueries    atomic.Int64
}

func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{
		services: make(map[string][]ServiceEntry),
		// roundRobinIndex is initialized as sync.Map (zero value)
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
	// Initialize round-robin index for new task
	registry.roundRobinIndex.LoadOrStore(task, &atomic.Int64{})
}

func (registry *MemoryRegistry) GetService(task string) (string, error) {
	// Use RLock for read-only access to services map (major performance improvement)
	registry.mutex.RLock()
	entries, found := registry.services[task]
	if !found || len(entries) == 0 {
		registry.mutex.RUnlock()
		return "", ErrNotFound
	}
	numEntries := len(entries)
	registry.mutex.RUnlock()

	// Atomic round-robin selection (lock-free)
	idxVal, _ := registry.roundRobinIndex.LoadOrStore(task, &atomic.Int64{})
	idxPtr := idxVal.(*atomic.Int64)
	idx := int(idxPtr.Add(1)-1) % numEntries

	// Now need write lock to increment query count for the selected entry
	registry.mutex.Lock()
	if idx >= len(registry.services[task]) {
		// Race condition: entries changed, recalculate
		idx = idx % len(registry.services[task])
	}
	selectedAddr := registry.services[task][idx].Address
	// Increment the query count for this specific entry
	registry.services[task][idx].QueryCount++
	registry.mutex.Unlock()

	// Increment total queries atomically (lock-free)
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
		TotalQueries: registry.totalQueries.Load(),
		TotalTasks:   len(registry.services),
	}
}
