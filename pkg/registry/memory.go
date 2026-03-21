package registry

import (
	"net"
	"net/url"
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
	totalQueries     atomic.Int64
	firewall         *firewall.Firewall
}

func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{
		services: make(map[string][]ServiceEntry),
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
			return
		}
	}

	// New entry via legacy register path defaults to capacity 1.
	newEntry := ServiceEntry{
		Address:       addr,
		LastHeartbeat: now,
		Capacity:      1,
	}
	registry.services[task] = append(entries, newEntry)
	registry.roundRobinIndex.LoadOrStore(task, &atomic.Int64{})
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
			return
		}
	}
	// not found -> append
	newEntry := ServiceEntry{
		Address:       addr,
		LastHeartbeat: now,
		Capacity:      capacity,
	}
	registry.services[task] = append(entries, newEntry)
	// Ensure a round-robin index exists for the task.
	registry.roundRobinIndex.LoadOrStore(task, &atomic.Int64{})
}

func (registry *MemoryRegistry) GetService(task string) (string, error) {
	return registry.GetServiceForRequestor(task, nil)
}

func (registry *MemoryRegistry) GetServiceForRequestor(task string, requestorIP net.IP) (string, error) {
	task = strings.TrimSpace(task)
	if task == "" {
		return "", ErrInvalidTaskName
	}

	// Copy current addresses under RLock so we don't hold the lock while
	// doing parsing / firewall checks.
	registry.mutex.RLock()
	entries, found := registry.services[task]
	if !found || len(entries) == 0 {
		registry.mutex.RUnlock()
		return "", ErrNotFound
	}
	entryCopy := make([]ServiceEntry, len(entries))
	for i, e := range entries {
		entryCopy[i] = e
	}
	registry.mutex.RUnlock()

	// Filter allowed addresses (if firewall is configured and we know requestor IP).
	allowedEntries := entryCopy
	if registry.firewall != nil && requestorIP != nil {
		allowedEntries = make([]ServiceEntry, 0, len(entryCopy))
		for _, entry := range entryCopy {
			destIP := destinationIPFromAddress(entry.Address)
			if destIP != nil && registry.firewall.IsAllowed(requestorIP, destIP) {
				allowedEntries = append(allowedEntries, entry)
			}
		}
		if len(allowedEntries) == 0 {
			return "", ErrNoAllowedService
		}
	}

	totalWeight := totalServiceWeight(allowedEntries)
	if totalWeight <= 0 {
		return "", ErrNotFound
	}

	// Atomic weighted round-robin selection over the allowed set without
	// materializing an expanded weighted address pool.
	idxVal, _ := registry.roundRobinIndex.LoadOrStore(task, &atomic.Int64{})
	idxPtr := idxVal.(*atomic.Int64)
	slot := int(idxPtr.Add(1)-1) % totalWeight
	selectedAddr, ok := selectWeightedAddress(allowedEntries, slot)
	if !ok {
		return "", ErrNotFound
	}

	// Increment query count atomically (lock-free).
	counterKey := task + ":" + selectedAddr
	counterVal, _ := registry.queryCounters.LoadOrStore(counterKey, &atomic.Int64{})
	counterPtr := counterVal.(*atomic.Int64)
	counterPtr.Add(1)

	// Increment total queries atomically (lock-free).
	registry.totalQueries.Add(1)

	return selectedAddr, nil
}

func destinationIPFromAddress(address string) net.IP {
	addr := strings.TrimSpace(address)
	if addr == "" {
		return nil
	}

	if strings.Contains(addr, "://") {
		u, err := url.Parse(addr)
		if err == nil {
			if host := strings.TrimSpace(u.Hostname()); host != "" {
				if ip := net.ParseIP(host); ip != nil {
					return ip
				}
			}
		}
	}

	if host, _, err := net.SplitHostPort(addr); err == nil {
		host = strings.Trim(host, "[]")
		if ip := net.ParseIP(host); ip != nil {
			return ip
		}
	}

	return net.ParseIP(addr)
}

func totalServiceWeight(entries []ServiceEntry) int {
	total := 0
	for _, entry := range entries {
		total += serviceWeight(entry)
	}
	return total
}

func selectWeightedAddress(entries []ServiceEntry, slot int) (string, bool) {
	running := 0
	for _, entry := range entries {
		running += serviceWeight(entry)
		if slot < running {
			return entry.Address, true
		}
	}
	return "", false
}

func serviceWeight(entry ServiceEntry) int {
	capacity := entry.Capacity
	if capacity <= 0 {
		capacity = 1
	}
	return capacity
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
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()

	// Return a deep copy with current atomic query counts synced into the structs.
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
