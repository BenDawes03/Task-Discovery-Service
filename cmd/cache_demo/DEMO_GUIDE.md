# Running the Cache Performance Demo

## Quick Start

### Option 1: Using the PowerShell Script (Easiest)

```powershell
.\cmd\cache_demo\run_cache_demo.ps1
```

Follow the on-screen instructions to:
1. Start PostgreSQL (if not running)
2. Start the TDS server with cache enabled
3. Run the demo automatically

### Option 2: Manual Setup

#### Terminal 1: Start the Server
```powershell
.\cmd\server\server.exe --store-url="postgresql://postgres:mysecretpassword@localhost:5432/tds?sslmode=disable" --cache-max-size=10 --tcp
```

#### Terminal 2: Run the Demo
```powershell
.\cmd\cache_demo\cache_demo.exe
```

## What You'll See

The demo progresses through several phases:

### Phase 1: Registration
- Registers 30 tasks with 3 services each (90 total services)
- Since cache-max-size=10, only 10 tasks can be in memory

### Phase 2: Cache Hit Test
- Queries the first 10 tasks repeatedly (50 queries per task)
- After initial queries, these tasks are cached in memory
- Shows **FAST** response times (< 1ms typically)

### Phase 3: Cache Miss Test
- Queries the last 10 tasks repeatedly (50 queries per task)
- These tasks aren't popular enough to stay in the cache
- Requires database lookups - **SLOWER** response times

### Phase 4: Mixed Workload
- 80% queries to popular tasks (cache hits)
- 20% queries to unpopular tasks (cache misses)
- Simulates realistic usage patterns

### Phase 5: Comparison
- Side-by-side comparison of all scenarios
- Shows average latency and percentiles
- Displays cache speedup factor
- Visual bar charts

## Understanding the Results

### Typical Results
```
Average Latency:
  Cache Hits:     0.45ms
  Cache Misses:   3.2ms
  Mixed:          0.9ms

Cache Speedup: 7.11x faster
```

### What This Means
- **Cache hits** are served from memory - very fast
- **Cache misses** require database queries - added latency
- **Mixed workload** closely matches real-world scenarios
- The cache significantly improves performance for popular tasks

## Customization

### Adjust Cache Size
Make the cache smaller to see more dramatic differences:
```powershell
.\cmd\server\server.exe --store-url="..." --cache-max-size=5 --tcp
```

### Change Task Count
Edit [cmd/cache_demo/main.go](cmd/cache_demo/main.go):
```go
numTasks := 50 // Increase for more tasks
servicesPerTask := 5 // More services per task
```

### Protocol Selection
Switch to UDP by changing the `protocol` variable in main.go:
```go
protocol = "udp" // or "tcp"
```

Then restart the server with UDP:
```powershell
.\cmd\server\server.exe --store-url="..." --cache-max-size=10 --udp
```

## Interpreting Performance Metrics

### Latency Metrics
- **Average**: Mean response time across all queries
- **Median (p50)**: 50% of queries complete faster than this
- **95th percentile (p95)**: 95% of queries complete faster than this
- **99th percentile (p99)**: 99% of queries complete faster than this

### Why p95/p99 Matter
- Average can hide outliers
- p95/p99 show worst-case performance
- Important for understanding user experience

### Queries/Second
- How many queries the system can handle
- Higher is better
- Cache hits enable much higher throughput

## Troubleshooting

### "Server not reachable"
✓ Check the server is running
✓ Verify protocol matches (TCP vs UDP)
✓ Ensure port 5000 is not blocked

### "All queries are cache hits"
✓ Increase `numTasks` in the demo
✓ Reduce server's `--cache-max-size`
✓ Check server logs to verify DB is connected

### "All queries are cache misses"
✓ Cache might be disabled (check --cache-max-size)
✓ Database might not be connected
✓ Check server logs for errors

### Database Connection Issues
```powershell
# Test PostgreSQL connection
docker ps  # Check if container is running
docker logs tds-postgres  # Check for errors

# Restart PostgreSQL
docker stop tds-postgres
docker rm tds-postgres
docker run --name tds-postgres -e POSTGRES_PASSWORD=mysecretpassword -e POSTGRES_DB=tds -p 5432:5432 -d postgres:15
```

## Advanced: Monitoring the Cache

While the demo runs, watch the server TUI to see:
- Task list showing which tasks are registered
- The cache filling up with popular tasks
- Unpopular tasks getting evicted from cache
- Query counts per service

Look for:
- First 10 tasks having high query counts (cache hits)
- Last 10 tasks having lower query counts (cache misses)
- Cache dynamically adjusting based on query patterns
