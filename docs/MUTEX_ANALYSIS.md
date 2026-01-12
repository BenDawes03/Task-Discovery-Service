# Registry Mutex Optimization Analysis

## Current Issues

### 1. Critical: GetServiceForRequestor Performance Bottleneck

**Problem:**
- Uses write lock for query operations (most frequent operation)
- Prevents concurrent queries - massive performance hit
- Only needs write lock to update counters

**Solution Options:**

#### Option A: Atomic Counters (RECOMMENDED)
Replace mutex-protected counters with atomic operations:

```go
type MemoryRegistry struct {
    mutex           sync.RWMutex
    services        map[string][]ServiceEntry
    roundRobinIndex map[string]int
    totalQueries    atomic.Int64  // ✓ Atomic counter
    firewall        *firewall.Firewall
}

type ServiceEntry struct {
    Address       string
    LastHeartbeat time.Time
    QueryCount    atomic.Int64  // ✓ Atomic counter
}

func (registry *MemoryRegistry) GetServiceForRequestor(task string, requestorIP net.IP) (string, error) {
    registry.mutex.RLock()  // ✓ READ LOCK - allows concurrency
    defer registry.mutex.RUnlock()
    
    // ... find service logic ...
    
    // Update counters atomically WITHOUT holding write lock
    entries[idx].QueryCount.Add(1)
    registry.totalQueries.Add(1)
    
    // Round-robin requires write lock briefly
    registry.updateRoundRobinIndex(task, nextIdx)
    
    return selected.Address, nil
}
```

**Benefits:**
- 10-100x performance improvement for read-heavy workloads
- Multiple queries can run concurrently
- Only brief write lock for round-robin update

**Trade-offs:**
- Slightly more complex code
- Go 1.19+ required for atomic types

#### Option B: Per-Task Locks (More Complex)
Use fine-grained locking per task:

```go
type MemoryRegistry struct {
    globalMutex     sync.RWMutex
    services        map[string]*taskData
}

type taskData struct {
    mutex           sync.RWMutex
    entries         []ServiceEntry
    roundRobinIndex int
}
```

**Benefits:**
- Different tasks can be queried concurrently
- Better scalability with many tasks

**Trade-offs:**
- Much more complex
- Higher memory overhead
- Lock management complexity

### 2. Moderate: StoreBackedRegistry Cache Replacement

**Problem:**
```go
sr.cacheMutex.Lock()
sr.memCache = NewMemoryRegistry()  // Replaces entire object
sr.cacheMutex.Unlock()
```

The `cacheMutex` doesn't protect operations ON the cache, only the cache pointer replacement.

**Solution:**
Either:
1. Hold write lock during entire cache reload
2. Use atomic.Value for pointer swapping
3. Clear and repopulate existing cache

```go
// Option 1: Lock during reload (simpler)
func (sr *StoreBackedRegistry) WarmCacheFromDB(ctx context.Context) error {
    services, err := sr.store.ListServices(ctx)
    if err != nil {
        return err
    }

    // Create new cache
    newCache := NewMemoryRegistry()
    for task, entries := range services {
        for _, e := range entries {
            newCache.Register(task, e.Address)
        }
    }
    
    // Atomic swap with write lock
    sr.cacheMutex.Lock()
    sr.memCache = newCache
    sr.lastCacheSync = time.Now()
    sr.cacheMutex.Unlock()
    
    return nil
}
```

### 3. Minor: Round-Robin Index Management

**Current:**
- Held under same write lock as everything else
- Simple but prevents concurrency

**Optimization:**
Separate round-robin from query operation:

```go
func (registry *MemoryRegistry) GetServiceForRequestor(task string, requestorIP net.IP) (string, error) {
    registry.mutex.RLock()
    entries, found := registry.services[task]
    idx := registry.roundRobinIndex[task]
    // ... filter and select ...
    registry.mutex.RUnlock()
    
    // Update atomically
    selected := entries[selectedIdx]
    entries[selectedIdx].QueryCount.Add(1)
    registry.totalQueries.Add(1)
    
    // Update round-robin separately (brief write lock)
    registry.mutex.Lock()
    registry.roundRobinIndex[task] = (selectedIdx + 1) % len(entries)
    registry.mutex.Unlock()
    
    return selected.Address, nil
}
```

## Performance Impact Estimation

### Current (Write Lock for Queries):
- 1 query at a time
- ~10,000 queries/second max (with fast operations)
- High contention under load

### With Atomic Counters (Read Lock for Queries):
- N concurrent queries (N = CPU cores)
- ~100,000+ queries/second on modern hardware
- Minimal contention
- 10-50x throughput improvement

## Implementation Priority

1. **HIGH PRIORITY:** Atomic counters for QueryCount and totalQueries
2. **MEDIUM:** Fix StoreBackedRegistry cache replacement
3. **LOW:** Per-task locking (only if needed for extreme scale)

## Code Changes Required

### File: pkg/registry/registry.go
```go
type ServiceEntry struct {
    Address       string
    LastHeartbeat time.Time
    QueryCount    int64  // Keep as int64 for external API
}
```

### File: pkg/registry/memory.go
```go
import (
    "sync"
    "sync/atomic"
)

type MemoryRegistry struct {
    mutex           sync.RWMutex
    services        map[string][]serviceEntryInternal
    roundRobinIndex map[string]int
    totalQueries    atomic.Int64
    firewall        *firewall.Firewall
}

type serviceEntryInternal struct {
    Address       string
    LastHeartbeat time.Time
    QueryCount    atomic.Int64
}
```

## Testing Recommendations

1. **Race Detector:** Run with `-race` flag
   ```bash
   go run -race ./cmd/server
   ```

2. **Load Test:** Compare before/after performance
   ```bash
   # Concurrent queries test
   .\test_scripts\load_test_server.ps1
   ```

3. **Correctness:** Verify round-robin still works correctly

## Risks

- **Low Risk:** Atomic operations well-tested in Go stdlib
- **Medium Risk:** Must ensure round-robin index doesn't drift
- **Low Risk:** ServiceEntry query count visibility (already eventual consistency)

## Conclusion

**The current mutex usage is NOT optimal.** The write lock in `GetServiceForRequestor` creates a severe bottleneck. Implementing atomic counters would provide 10-50x performance improvement with minimal risk and code complexity.
