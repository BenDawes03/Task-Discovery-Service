package registry

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"tds/pkg/firewall"
	"tds/pkg/store"
)

// StoreBackedRegistry implements Registry by delegating to a Store backend for persistence
// while maintaining an in-memory LFU cache for performance.
type StoreBackedRegistry struct {
	store           store.Store
	memCache        *MemoryRegistry // In-memory LFU cache (top X most queried)
	cacheMutex      sync.RWMutex
	lastCacheSync   time.Time
	cacheSyncPeriod time.Duration // How often to sync cache from DB
	cacheMaxSize    int           // Maximum number of task entries to cache (0 = unlimited)
}

// NewStoreBackedRegistry creates a new Registry backed by a Store with in-memory LFU caching.
// cacheMaxSize: maximum number of tasks to keep in cache (0 = unlimited, loads all from DB)
func NewStoreBackedRegistry(s store.Store, cacheMaxSize int) *StoreBackedRegistry {
	return &StoreBackedRegistry{
		store:           s,
		memCache:        NewMemoryRegistry(),
		cacheSyncPeriod: 30 * time.Second,
		lastCacheSync:   time.Now(),
		cacheMaxSize:    cacheMaxSize,
	}
}

// WarmCacheFromDB loads the top X most queried entries from the database into the in-memory cache.
// If cacheMaxSize is 0, loads all entries (backward compatible behavior).
func (sr *StoreBackedRegistry) WarmCacheFromDB(ctx context.Context) error {
	services, err := sr.store.ListServices(ctx)
	if err != nil {
		return err
	}

	sr.cacheMutex.Lock()
	sr.memCache = NewMemoryRegistry() // Reset cache
	sr.cacheMutex.Unlock()

	// If no limit, load everything (backward compatible)
	if sr.cacheMaxSize <= 0 {
		for task, entries := range services {
			for _, e := range entries {
				sr.memCache.Register(task, e.Address)
			}
		}
	} else {
		// LFU: Select top X tasks by total query count
		type taskStats struct {
			task       string
			totalCount int64
			entries    []store.ServiceEntry
		}

		taskList := make([]taskStats, 0, len(services))
		for task, entries := range services {
			totalCount := int64(0)
			for _, e := range entries {
				totalCount += e.QueryCount
			}
			taskList = append(taskList, taskStats{
				task:       task,
				totalCount: totalCount,
				entries:    entries,
			})
		}

		// Sort by query count descending (most queried first)
		for i := 0; i < len(taskList); i++ {
			for j := i + 1; j < len(taskList); j++ {
				if taskList[j].totalCount > taskList[i].totalCount {
					taskList[i], taskList[j] = taskList[j], taskList[i]
				}
			}
		}

		// Take top cacheMaxSize tasks
		limit := sr.cacheMaxSize
		if limit > len(taskList) {
			limit = len(taskList)
		}

		for i := 0; i < limit; i++ {
			ts := taskList[i]
			for _, e := range ts.entries {
				sr.memCache.Register(ts.task, e.Address)
			}
		}

		fmt.Fprintf(os.Stderr, "[CACHE] Loaded top %d/%d tasks (max=%d)\n", limit, len(taskList), sr.cacheMaxSize)
	}

	sr.cacheMutex.Lock()
	sr.lastCacheSync = time.Now()
	sr.cacheMutex.Unlock()

	return nil
}

// Register adds or updates a service entry in both the cache and the store.
func (sr *StoreBackedRegistry) Register(task, addr string) {
	// Write to in-memory cache immediately for fast access
	sr.memCache.Register(task, addr)

	// Also write to persistent store synchronously
	entry := &store.ServiceEntry{
		Address:       addr,
		LastHeartbeat: time.Now(),
		QueryCount:    0,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sr.store.Register(ctx, task, entry); err != nil {
		// Log to stderr so we can see failures
		fmt.Fprintf(os.Stderr, "[STORE] Register failed: %v\n", err)
	}
}

// GetService retrieves a service from the cache first (fast path).
// On cache miss, fetches from DB and potentially evicts least-used from cache.
func (sr *StoreBackedRegistry) GetService(task string) (string, error) {
	return sr.GetServiceForRequestor(task, nil)
}

// GetServiceForRequestor retrieves a service that the requestor is allowed to reach.
func (sr *StoreBackedRegistry) GetServiceForRequestor(task string, requestorIP net.IP) (string, error) {
	// Try cache first (fast path)
	addr, err := sr.memCache.GetServiceForRequestor(task, requestorIP)
	if err == nil {
		// Cache hit - also update query count in DB asynchronously
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = sr.store.GetService(ctx, task)
		}()
		return addr, nil
	}

	// Cache miss - fetch from database
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	entry, err := sr.store.GetService(ctx, task)
	if err != nil {
		return "", err
	}

	// Add to cache (will be included in next sync if frequently used)
	sr.memCache.Register(task, entry.Address)
	fmt.Fprintf(os.Stderr, "[CACHE] Miss for task '%s', fetched from DB: %s\n", task, entry.Address)

	// Now check firewall rules if requestor IP is provided
	if requestorIP != nil {
		addr, err = sr.memCache.GetServiceForRequestor(task, requestorIP)
		if err != nil {
			return "", err
		}
		return addr, nil
	}

	return entry.Address, nil
}

// SetFirewall configures the firewall rules for this registry.
func (sr *StoreBackedRegistry) SetFirewall(fw *firewall.Firewall) {
	sr.memCache.SetFirewall(fw)
}

// Cleanup removes stale entries from the in-memory cache.
// For the persistent store, entries are marked as inactive rather than deleted,
// preserving them for logging and audit purposes.
func (sr *StoreBackedRegistry) Cleanup(timeout time.Duration) int {
	// Remove from cache immediately
	cacheRemoved := sr.memCache.Cleanup(timeout)

	// Mark as inactive in persistent store (not deleted, for logging/audit)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dbFlagged, err := sr.store.Cleanup(ctx, timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[STORE] Cleanup failed: %v\n", err)
	}

	// Return total: removed from cache + flagged as inactive in DB
	return cacheRemoved + int(dbFlagged)
}

// ListServices returns services from the in-memory cache.
// Periodically syncs with the database to pick up changes from other servers.
func (sr *StoreBackedRegistry) ListServices() map[string][]ServiceEntry {
	// Check if we should sync from DB (every cacheSyncPeriod)
	sr.cacheMutex.RLock()
	shouldSync := time.Since(sr.lastCacheSync) > sr.cacheSyncPeriod
	sr.cacheMutex.RUnlock()

	if shouldSync {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = sr.WarmCacheFromDB(ctx)
		cancel()
	}

	// Return from cache (fast path)
	return sr.memCache.ListServices()
}

// GetStats returns statistics from the cache.
func (sr *StoreBackedRegistry) GetStats() Stats {
	return sr.memCache.GetStats()
}

// Ensure StoreBackedRegistry implements Registry.
var _ Registry = (*StoreBackedRegistry)(nil)
