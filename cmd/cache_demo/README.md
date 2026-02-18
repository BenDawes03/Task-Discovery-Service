# TDS Cache Performance Demo

This demo demonstrates the performance difference between cache hits and cache misses in the TDS server with database persistence.

## Prerequisites

1. **PostgreSQL Database**: The server needs a running PostgreSQL database
2. **TDS Server**: Must be running with database persistence and a limited cache size

## Setup

### 1. Start PostgreSQL
If you don't have PostgreSQL running, start it with Docker:
```powershell
docker run --name tds-postgres -e POSTGRES_PASSWORD=mysecretpassword -e POSTGRES_DB=tds -p 5432:5432 -d postgres:15
```

### 2. Start TDS Server with Cache
Start the server with a limited cache size (e.g., 10 tasks):
```powershell
cd cmd/server
go run main.go --store-url="postgresql://postgres:mysecretpassword@localhost:5432/tds?sslmode=disable" --cache-max-size=10 --tcp
```

Or if you've built the server:
```powershell
.\server.exe --store-url="postgresql://postgres:mysecretpassword@localhost:5432/tds?sslmode=disable" --cache-max-size=10 --tcp
```

### 3. Run the Cache Demo
In a separate terminal:
```powershell
cd cmd/cache_demo
go run main.go
```

Or build and run:
```powershell
go build -o cache_demo.exe .\cmd\cache_demo
.\cache_demo.exe
```

## What the Demo Does

1. **Registers 30 Tasks** with 3 services each (90 total services)
   - With cache-max-size=10, only the 10 most queried tasks stay in memory

2. **Tests Cache Hits** (Popular Tasks)
   - Queries the first 10 tasks repeatedly
   - These should be in the cache after initial queries
   - Demonstrates fast in-memory lookups

3. **Tests Cache Misses** (Unpopular Tasks)
   - Queries the last 10 tasks (less popular)
   - These likely aren't in the cache
   - Requires database lookups, showing slower response times

4. **Mixed Workload Test**
   - 80% queries to popular tasks (cache hits)
   - 20% queries to unpopular tasks (cache misses)
   - Simulates real-world usage patterns

5. **Performance Comparison**
   - Shows average latency for each scenario
   - Displays 95th percentile latencies
   - Calculates cache speedup factor
   - Visual bar charts for easy comparison

## Expected Results

You should see:
- **Cache Hits**: Very fast (typically < 1ms)
- **Cache Misses**: Slower (database lookup overhead)
- **Mixed Workload**: Between the two, closer to cache hit performance
- **Speedup**: Cache hits should be 2-10x faster depending on DB performance

## Configuration

Edit [main.go](main.go) to customize:
- `serverAddr`: Server address (default: "localhost:5000")
- `protocol`: "tcp" or "udp" (default: "tcp")
- `numTasks`: Number of tasks to register (default: 30)
- `servicesPerTask`: Services per task (default: 3)

## Troubleshooting

**Server not reachable:**
- Ensure the server is running
- Check that the protocol matches (TCP vs UDP)
- Verify the port is correct

**All queries are cache hits:**
- Increase the number of tasks (`numTasks`)
- Reduce the server's `--cache-max-size` parameter
- Ensure database persistence is enabled

**All queries are cache misses:**
- The cache might be too small or disabled
- Check server logs to verify cache is working
- Try querying the same tasks multiple times to warm the cache
