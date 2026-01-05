package registry

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"tds/pkg/store"
)

// StoreBackedRegistry implements Registry by delegating to a Store backend for persistence
// while maintaining an in-memory cache for performance.
type StoreBackedRegistry struct {
	store           store.Store
	memCache        *MemoryRegistry // In-memory cache
	cacheMutex      sync.RWMutex
	lastCacheSync   time.Time
	cacheSyncPeriod time.Duration // How often to sync cache from DB
}

// NewStoreBackedRegistry creates a new Registry backed by a Store with in-memory caching.
func NewStoreBackedRegistry(s store.Store) *StoreBackedRegistry {
	return &StoreBackedRegistry{
		store:           s,
		memCache:        NewMemoryRegistry(),
		cacheSyncPeriod: 30 * time.Second,
		lastCacheSync:   time.Now(),
	}
}

// WarmCacheFromDB loads all entries from the database into the in-memory cache.
func (sr *StoreBackedRegistry) WarmCacheFromDB(ctx context.Context) error {
	services, err := sr.store.ListServices(ctx)
	if err != nil {
		return err
	}

	sr.cacheMutex.Lock()
	sr.memCache = NewMemoryRegistry() // Reset cache
	sr.cacheMutex.Unlock()

	// Populate the cache with all services from the database
	for task, entries := range services {
		for _, e := range entries {
			// Register in cache (this updates LastHeartbeat to now, which is fine for initial load)
			sr.memCache.Register(task, e.Address)
		}
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

// GetService retrieves a service from the cache and increments its query count in both cache and DB.
func (sr *StoreBackedRegistry) GetService(task string) (string, error) {
	// Read from in-memory cache (fast path)
	addr, err := sr.memCache.GetService(task)
	if err != nil {
		return "", err
	}

	// Also update in the persistent store asynchronously
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Fetch the entry from DB to increment query count
		_, err := sr.store.GetService(ctx, task)
		if err == nil {
			// Query count was already incremented by GetService in the store
		}
	}()

	return addr, nil
}

// Cleanup removes stale entries from both cache and store.
func (sr *StoreBackedRegistry) Cleanup(timeout time.Duration) int {
	// Remove from cache immediately
	cacheRemoved := sr.memCache.Cleanup(timeout)

	// Also remove from persistent store synchronously
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dbRemoved, err := sr.store.Cleanup(ctx, timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[STORE] Cleanup failed: %v\n", err)
	}

	// Return total removed from both cache and database
	return cacheRemoved + int(dbRemoved)
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
