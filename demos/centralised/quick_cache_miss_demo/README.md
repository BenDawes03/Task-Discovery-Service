# Quick Cache Miss Demo

A simple, focused demonstration of TDS caching behavior with `cache-max-size=1`.

## Overview

This demo clearly shows the performance difference between:
- **Cache Hits**: Querying the same task repeatedly (sub-millisecond responses)
- **Cache Misses**: Querying different tasks in sequence (requires database lookups)

With `cache-max-size=1`, only ONE task stays cached at a time. This makes cache behavior very obvious!

## Prerequisites

1. A running PostgreSQL database with the TDS schema
2. The TDS server compiled and ready to run

## Running the Demo

### Option 1: Using the PowerShell Script (Recommended)

```powershell
# Navigate to the repo root
cd c:\Users\ben\Documents\FYP\bxd280

# Run the demo script from the subdirectory
.\demos\centralised\quick_cache_miss_demo\run_quick_cache_miss_demo.ps1
```

The script will:
1. Build the demo executable
2. Show setup instructions
3. Wait for your server to be running with correct settings
4. Start the demo

### Option 2: Manual Build & Run

```powershell
# Build the demo
go build -o bin\quick_cache_miss_demo.exe .\demos\centralised\quick_cache_miss_demo

# In one terminal, start the server with cache-max-size=1
.\bin\server.exe --store-url="postgresql://user:pass@localhost:5432/tds" --cache-max-size=1 --tcp

# In another terminal, run the demo
.\bin\quick_cache_miss_demo.exe
```

## What to Expect

### Phase 1: Cache Hits
```
Querying 'task_001' 10 times...
(Expect fast responses - sub-millisecond after first query)

  Query  1: ⏱️  FIRST QUERY   3.2ms   [████████░░░░░░░░░░░░░░░░░░░░░░░░░░░░]
  Query  2: 🔥 CACHE HIT     0.1ms   [░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░]
  Query  3: 🔥 CACHE HIT     0.1ms   [░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░]
  ...
```

You should see:
- First query: Slightly slower (database lookup)
- Subsequent queries: Very fast (all serve from cache)

### Phase 2: Cache Misses
```
Querying 5 different tasks, 10 times each...
(Expect slower responses - each requires database lookup)

  Query  1: 🗄️  CACHE MISS   2.8ms   [██████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░]
  Query  2: 🗄️  CACHE MISS   3.1ms   [██████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░]
  Query  3: 🗄️  CACHE MISS   2.9ms   [██████░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░]
  ...
```

You should see:
- Every query is relatively slow
- Consistent latency across all queries
- No variance like with cache hits

### Phase 3: Comparison
The final screen shows side-by-side statistics demonstrating the speedup from caching.

## Understanding Cache Size = 1

When `--cache-max-size=1`:
- The cache can hold **exactly ONE task** at a time
- Each new task query **evicts the previous task** from the cache
- This demonstrates worst-case caching behavior
- In production, larger cache sizes (10-100) are more typical

## Customization

To modify the demo:

### Change number of tasks
Edit `demos/centralised/quick_cache_miss_demo/main.go`:
```go
taskNames := []string{"task_001", "task_002", "task_003", "task_004", "task_005"}
```

### Change number of queries
Edit the demo calls:
```go
task1Latencies := performConsistentQueries("task_001", 10)  // Change 10
differentTaskLatencies := performRotatingQueries(taskNames, 10)  // Change 10
```

### Try different cache sizes

Run the server with different cache sizes to see the impact:

```powershell
# Very restrictive: cache-max-size=1
.\bin\server.exe --cache-max-size=1 --tcp ...

# Typical: cache-max-size=10
.\bin\server.exe --cache-max-size=10 --tcp ...

# Large: cache-max-size=100
.\bin\server.exe --cache-max-size=100 --tcp ...
```

## Key Learnings

1. **Caching dramatically improves performance** for repeated queries
2. **Cache size matters** - with size=1, any variety in queries defeats the cache
3. **Working set size** - if your queries span more tasks than your cache size, expect many misses
4. **Real-world implications** - understand your query patterns to set appropriate cache size

## Next Steps

- See `demos/cache/` for a more comprehensive caching benchmark
- Read the TDS documentation on cache configuration
- Review cache-related code in `pkg/registry/`
