package registry

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
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
	cacheMissCounts sync.Map      // map[string]*atomic.Int64 tracking consecutive misses per task
	fallbackCursor  sync.Map      // map[string]*atomic.Int64 weighted cursor for non-admitted misses
	admitAfterMiss  atomic.Int64  // Number of misses before admitting a task into cache
	logger          func(string)
}

func (sr *StoreBackedRegistry) currentMemCache() *MemoryRegistry {
	sr.cacheMutex.RLock()
	defer sr.cacheMutex.RUnlock()
	return sr.memCache
}

// NewStoreBackedRegistry creates a new Registry backed by a Store with in-memory LFU caching.
// cacheMaxSize: maximum number of tasks to keep in cache (0 = unlimited, loads all from DB)
func NewStoreBackedRegistry(s store.Store, cacheMaxSize int) *StoreBackedRegistry {
	sr := &StoreBackedRegistry{
		store:           s,
		memCache:        NewMemoryRegistry(),
		cacheSyncPeriod: 30 * time.Second,
		lastCacheSync:   time.Now(),
		cacheMaxSize:    cacheMaxSize,
	}
	sr.admitAfterMiss.Store(1)
	return sr
}

// SetCacheAdmissionMissThreshold sets misses required before a task is admitted to cache.
// Values <= 1 preserve current behavior (admit on first miss).
func (sr *StoreBackedRegistry) SetCacheAdmissionMissThreshold(threshold int) {
	if threshold <= 1 {
		threshold = 1
	}
	sr.admitAfterMiss.Store(int64(threshold))
}

// SetLogger configures an optional event logger used for store/cache warnings.
// When unset, messages fall back to stderr.
func (sr *StoreBackedRegistry) SetLogger(logger func(string)) {
	sr.cacheMutex.Lock()
	defer sr.cacheMutex.Unlock()
	sr.logger = logger
}

func (sr *StoreBackedRegistry) logMessage(format string, args ...any) {
	message := fmt.Sprintf(format, args...)

	sr.cacheMutex.RLock()
	logger := sr.logger
	sr.cacheMutex.RUnlock()

	if logger != nil {
		logger(message)
		return
	}

	fmt.Fprintln(os.Stderr, message)
}

// syncQueryCountsToDB writes query counts from cache to database before cache warming.
// This ensures LFU cache selection picks tasks based on current query activity.
func (sr *StoreBackedRegistry) syncQueryCountsToDB(ctx context.Context) error {
	// Get current cached services with their query counts
	cachedServices := sr.currentMemCache().ListServices()

	for task, entries := range cachedServices {
		for _, entry := range entries {
			if entry.QueryCount > 0 {
				// Write query count to DB
				storeEntry := &store.ServiceEntry{
					Address:       entry.Address,
					LastHeartbeat: entry.LastHeartbeat,
					QueryCount:    entry.QueryCount,
					Capacity:      entry.Capacity,
				}
				if err := sr.store.Register(ctx, task, storeEntry); err != nil {
					sr.logMessage("[STORE] Failed to sync query count for %s/%s: %v", task, entry.Address, err)
				}
			}
		}
	}

	return nil
}

// WarmCacheFromDB loads the top X most queried entries from the database into the in-memory cache.
// If cacheMaxSize is 0, loads all entries (backward compatible behavior).
func (sr *StoreBackedRegistry) WarmCacheFromDB(ctx context.Context) error {
	// First, sync query counts from cache to DB so LFU selection uses current data
	if err := sr.syncQueryCountsToDB(ctx); err != nil {
		sr.logMessage("[STORE] Warning: failed to sync query counts: %v", err)
	}

	newCache := NewMemoryRegistry()
	oldCache := sr.currentMemCache()
	oldCache.mutex.RLock()
	fw := oldCache.firewall
	oldCache.mutex.RUnlock()
	newCache.SetFirewall(fw)

	// If no limit, load everything (backward compatible)
	if sr.cacheMaxSize <= 0 {
		services, err := sr.store.ListServices(ctx)
		if err != nil {
			return err
		}

		for task, entries := range services {
			for _, e := range entries {
				// Directly populate cache with query counts from DB
				sr.populateCacheEntry(newCache, task, e.Address, e.QueryCount, e.LastHeartbeat, e.Capacity)
			}
		}
	} else {
		topTasks, err := sr.store.ListTopTasksByQueryCount(ctx, sr.cacheMaxSize)
		if err != nil {
			return err
		}

		servicesByTask, err := sr.store.ListServicesForTasks(ctx, topTasks)
		if err != nil {
			return err
		}

		for _, task := range topTasks {
			for _, e := range servicesByTask[task] {
				sr.populateCacheEntry(newCache, task, e.Address, e.QueryCount, e.LastHeartbeat, e.Capacity)
			}
		}

		sr.logMessage("[CACHE] Loaded top %d tasks (max=%d)", len(topTasks), sr.cacheMaxSize)
	}

	// Preserve weighted round-robin cursors for tasks that exist in the new cache.
	// Without this, every warm cycle resets selection to the first slot and can skew
	// short query bursts even when no external traffic is present.
	sr.copyRoundRobinState(oldCache, newCache)

	sr.cacheMutex.Lock()
	sr.memCache = newCache
	sr.lastCacheSync = time.Now()
	sr.cacheMutex.Unlock()

	return nil
}

func (sr *StoreBackedRegistry) copyRoundRobinState(from, to *MemoryRegistry) {
	from.roundRobinIndex.Range(func(key, value any) bool {
		task, ok := key.(string)
		if !ok {
			return true
		}

		to.mutex.RLock()
		_, taskInNewCache := to.services[task]
		to.mutex.RUnlock()
		if !taskInNewCache {
			return true
		}

		cursorPtr, ok := value.(*atomic.Int64)
		if !ok || cursorPtr == nil {
			return true
		}

		copiedCursor := &atomic.Int64{}
		copiedCursor.Store(cursorPtr.Load())
		to.roundRobinIndex.Store(task, copiedCursor)
		return true
	})
}

// populateCacheEntry directly adds an entry to cache with existing query count and heartbeat.
// This is used during cache warming to preserve query counts from the database.
func (sr *StoreBackedRegistry) populateCacheEntry(targetCache *MemoryRegistry, task, addr string, queryCount int64, lastHeartbeat time.Time, capacity int) {
	if capacity <= 0 {
		capacity = 1
	}
	targetCache.mutex.Lock()
	defer targetCache.mutex.Unlock()

	entries := targetCache.services[task]
	// Check if entry already exists
	for i, e := range entries {
		if e.Address == addr {
			// Update with DB values
			entries[i].LastHeartbeat = lastHeartbeat
			entries[i].ParsedIP = parseDestinationIP(addr)
			entries[i].Capacity = capacity
			targetCache.services[task] = entries
			// Set atomic query counter
			counterKey := task + ":" + addr
			counterVal, _ := targetCache.queryCounters.LoadOrStore(counterKey, &atomic.Int64{})
			counterPtr := counterVal.(*atomic.Int64)
			counterPtr.Store(queryCount)
			return
		}
	}

	// New entry - add with DB values
	newEntry := ServiceEntry{
		Address:       addr,
		ParsedIP:      parseDestinationIP(addr),
		LastHeartbeat: lastHeartbeat,
		Capacity:      capacity,
	}
	targetCache.services[task] = append(entries, newEntry)
	targetCache.roundRobinIndex.LoadOrStore(task, &atomic.Int64{})

	// Set atomic query counter
	counterKey := task + ":" + addr
	counterVal, _ := targetCache.queryCounters.LoadOrStore(counterKey, &atomic.Int64{})
	counterPtr := counterVal.(*atomic.Int64)
	counterPtr.Store(queryCount)
}

// Register adds or updates a service entry in both the cache and the store.
func (sr *StoreBackedRegistry) Register(task, addr string) {
	sr.RegisterWithCapacity(task, addr, 1)
}

func (sr *StoreBackedRegistry) RegisterWithCapacity(task, addr string, capacity int) {
	task = strings.TrimSpace(task)
	addr = strings.TrimSpace(addr)
	if task == "" || addr == "" {
		return
	}

	if capacity <= 0 {
		capacity = 1
	}
	// Write to in-memory cache immediately for fast access
	sr.currentMemCache().RegisterWithCapacity(task, addr, capacity)

	// Also write to persistent store synchronously
	// Note: QueryCount is not set here - it defaults to 0 for new entries
	// and is preserved for existing entries by the ON CONFLICT clause
	entry := &store.ServiceEntry{
		Address:       addr,
		LastHeartbeat: time.Now(),
		Capacity:      capacity,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sr.store.Register(ctx, task, entry); err != nil {
		sr.logMessage("[STORE] Register failed: %v", err)
	}
}

// GetService retrieves a service from the cache first (fast path).
// On cache miss, fetches from DB and potentially evicts least-used from cache.
func (sr *StoreBackedRegistry) GetService(task string) (string, error) {
	return sr.GetServiceForRequestor(task, nil)
}

// GetServiceForRequestor retrieves a service that the requestor is allowed to reach.
func (sr *StoreBackedRegistry) GetServiceForRequestor(task string, requestorIP net.IP) (string, error) {
	task = strings.TrimSpace(task)
	if task == "" {
		return "", ErrInvalidTaskName
	}

	// Try cache first (fast path)
	cache := sr.currentMemCache()
	addr, err := cache.GetServiceForRequestor(task, requestorIP)
	if err == nil {
		// Cache hit - return immediately (query count tracked in cache)
		return addr, nil
	}

	// Cache miss - fetch this task from the store, then select/admit via cache policy.
	// This keeps a single balancer authority (cache cursor) for both hits and misses.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	entries, err := sr.store.ListTaskServices(ctx, task)
	if err != nil {
		return "", err
	}

	if len(entries) == 0 {
		return "", ErrNotFound
	}

	if !sr.shouldAdmitTaskOnMiss(task) {
		selectedAddr, err := sr.selectFromStoreEntries(task, entries, requestorIP)
		if err != nil {
			return "", err
		}
		if err := sr.store.IncrementQueryCount(ctx, task, selectedAddr); err != nil {
			return "", err
		}
		return selectedAddr, nil
	}

	for _, entry := range entries {
		sr.populateCacheEntry(cache, task, entry.Address, entry.QueryCount, entry.LastHeartbeat, entry.Capacity)
	}
	sr.cacheMissCounts.Delete(task)

	return cache.GetServiceForRequestor(task, requestorIP)
}

func (sr *StoreBackedRegistry) shouldAdmitTaskOnMiss(task string) bool {
	threshold := sr.admitAfterMiss.Load()
	if threshold <= 1 {
		return true
	}

	counterVal, _ := sr.cacheMissCounts.LoadOrStore(task, &atomic.Int64{})
	counter := counterVal.(*atomic.Int64)
	missCount := counter.Add(1)
	return missCount >= threshold
}

func (sr *StoreBackedRegistry) selectFromStoreEntries(task string, entries []store.ServiceEntry, requestorIP net.IP) (string, error) {
	cache := sr.currentMemCache()
	cache.mutex.RLock()
	fw := cache.firewall
	cache.mutex.RUnlock()

	allowed := make([]ServiceEntry, 0, len(entries))
	for _, entry := range entries {
		candidate := ServiceEntry{
			Address:  entry.Address,
			ParsedIP: parseDestinationIP(entry.Address),
			Capacity: entry.Capacity,
		}

		if fw != nil && requestorIP != nil {
			destIP := candidate.ParsedIP
			if destIP == nil || !fw.IsAllowed(requestorIP, destIP) {
				continue
			}
		}

		allowed = append(allowed, candidate)
	}

	if len(allowed) == 0 {
		if fw != nil && requestorIP != nil {
			return "", ErrNoAllowedService
		}
		return "", ErrNotFound
	}

	totalWeight := totalServiceWeight(allowed)
	if totalWeight <= 0 {
		return "", ErrNotFound
	}

	cursorVal, _ := sr.fallbackCursor.LoadOrStore(task, &atomic.Int64{})
	cursor := cursorVal.(*atomic.Int64)
	slot := int(cursor.Add(1)-1) % totalWeight
	selectedAddr, ok := selectWeightedAddress(allowed, slot)
	if !ok {
		return "", ErrNotFound
	}

	return selectedAddr, nil
}

// SetFirewall configures the firewall rules for this registry.
func (sr *StoreBackedRegistry) SetFirewall(fw firewall.Evaluator) {
	sr.currentMemCache().SetFirewall(fw)
}

// Cleanup removes stale entries from the in-memory cache.
// For the persistent store, entries are marked as inactive rather than deleted,
// preserving them for logging and audit purposes.
func (sr *StoreBackedRegistry) Cleanup(timeout time.Duration) int {
	// Remove from cache immediately
	cacheRemoved := sr.currentMemCache().Cleanup(timeout)

	// Mark as inactive in persistent store (not deleted, for logging/audit)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dbFlagged, err := sr.store.Cleanup(ctx, timeout)
	if err != nil {
		sr.logMessage("[STORE] Cleanup failed: %v", err)
	}

	// Return total: removed from cache + flagged as inactive in DB
	return cacheRemoved + int(dbFlagged)
}

// ListServicesForDashboard returns a UI snapshot sourced from active DB rows.
// This is intentionally DB-backed (not cache-backed) so the TUI reflects the
// complete active task set rather than bounded cache contents.
func (sr *StoreBackedRegistry) ListServicesForDashboard(ctx context.Context) map[string][]ServiceEntry {
	dbServices, err := sr.store.ListServices(ctx)
	if err != nil {
		sr.logMessage("[CACHE] Dashboard DB snapshot failed: %v", err)
		return map[string][]ServiceEntry{}
	}

	result := make(map[string][]ServiceEntry, len(dbServices))
	for task, entries := range dbServices {
		converted := make([]ServiceEntry, len(entries))
		for i, entry := range entries {
			converted[i] = ServiceEntry{
				Address:       entry.Address,
				ParsedIP:      parseDestinationIP(entry.Address),
				LastHeartbeat: entry.LastHeartbeat,
				QueryCount:    entry.QueryCount,
				Capacity:      entry.Capacity,
			}
		}
		result[task] = converted
	}

	return result
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
		defer cancel()
		if err := sr.WarmCacheFromDB(ctx); err != nil {
			sr.logMessage("[CACHE] WarmCacheFromDB failed: %v", err)
		}
	}

	// Return from cache (fast path)
	return sr.currentMemCache().ListServices()
}

// GetStats returns statistics from the cache.
func (sr *StoreBackedRegistry) GetStats() Stats {
	return sr.currentMemCache().GetStats()
}

// Ensure StoreBackedRegistry implements Registry.
var _ Registry = (*StoreBackedRegistry)(nil)
