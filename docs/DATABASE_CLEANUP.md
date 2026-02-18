# Database Cleanup and Historical Data

## Overview

The TDS database-backed registry now preserves historical service registration data when performing cleanup operations. Instead of deleting stale entries, they are marked as inactive, allowing them to be excluded from queries while remaining available for logging, auditing, and historical analysis.

## How It Works

### Active vs Inactive Services

Services in the database have an `is_active` boolean flag:

- **Active (`is_active = TRUE`)**: Service is currently registered and will be returned by queries
- **Inactive (`is_active = FALSE`)**: Service has been cleaned up due to timeout, preserved for historical purposes

### Cleanup Behavior

When cleanup runs:

1. **In-Memory Cache**: Stale entries are removed completely (for performance)
2. **Database**: Stale entries are marked `is_active = FALSE` (preserved for history)

```sql
-- What cleanup does now:
UPDATE services 
SET is_active = FALSE, updated_at = NOW()
WHERE last_heartbeat < $cutoff AND is_active = TRUE;

-- What cleanup used to do:
DELETE FROM services WHERE last_heartbeat < $cutoff;
```

### Re-Registration

If a service that was marked inactive re-registers:

```sql
INSERT INTO services (..., is_active) VALUES (..., TRUE)
ON CONFLICT (task, address) DO UPDATE
SET is_active = TRUE, last_heartbeat = EXCLUDED.last_heartbeat, updated_at = NOW();
```

The service becomes active again, preserving its historical query count and created_at timestamp.

## Querying Services

### Active Services Only (Normal Operation)

All standard registry operations automatically filter for active services:

```go
// GetService - only returns active services
entry, err := store.GetService(ctx, "my-task")

// ListServices - only returns active services
services, err := store.ListServices(ctx)
```

The SQL queries include `WHERE is_active = TRUE`:

```sql
SELECT address, last_heartbeat, query_count
FROM services
WHERE task = $1 AND is_active = TRUE
ORDER BY address ASC;
```

### Historical/Inactive Services (For Logging)

New methods are available for querying inactive services:

```go
// Get all inactive services
inactive, err := postgresStore.ListInactiveServices(ctx)

// Get full history for a specific task (active + inactive)
history, err := postgresStore.GetServiceHistory(ctx, "my-task")
```

**Note**: These methods are specific to `PostgresStore` and not part of the generic `Store` interface, as they're optional features for logging/debugging.

## Database Schema

### Updated Schema

```sql
CREATE TABLE services (
    id SERIAL PRIMARY KEY,
    task TEXT NOT NULL,
    address TEXT NOT NULL,
    last_heartbeat TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    query_count BIGINT NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,  -- NEW
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(task, address)
);

CREATE INDEX idx_services_task ON services(task);
CREATE INDEX idx_services_last_heartbeat ON services(last_heartbeat);
CREATE INDEX idx_services_is_active ON services(is_active);  -- NEW
```

### Migration

For existing databases, run the migration:

```sql
-- From: pkg/store/postgres/migrations/0002_add_is_active.sql
ALTER TABLE services ADD COLUMN is_active BOOLEAN NOT NULL DEFAULT TRUE;
CREATE INDEX IF NOT EXISTS idx_services_is_active ON services(is_active);
```

Or use the built-in migration method:

```go
err := postgresStore.Migrate(ctx)
```

The `Migrate()` method is idempotent and will only add the column if it doesn't exist.

## Use Cases

### 1. Audit Trail

Track when services were registered and when they became inactive:

```sql
SELECT task, address, created_at, updated_at, last_heartbeat
FROM services
WHERE is_active = FALSE
ORDER BY updated_at DESC
LIMIT 100;
```

### 2. Service Uptime Analysis

Calculate how long services were active:

```sql
SELECT 
    task,
    address,
    created_at,
    updated_at AS deactivated_at,
    updated_at - created_at AS uptime
FROM services
WHERE is_active = FALSE
ORDER BY uptime DESC;
```

### 3. Query Popularity

See which services were queried most before becoming inactive:

```sql
SELECT task, address, query_count, last_heartbeat
FROM services
WHERE is_active = FALSE
ORDER BY query_count DESC
LIMIT 20;
```

### 4. Debugging Service Failures

Check if a service was previously registered but is now inactive:

```go
history, _ := postgresStore.GetServiceHistory(ctx, "failing-service")
for _, entry := range history {
    fmt.Printf("Address: %s, Last seen: %s, Queries: %d\n",
        entry.Address, entry.LastHeartbeat, entry.QueryCount)
}
```

## Performance Considerations

### Index Usage

The `is_active` index ensures filtering is efficient:

```sql
EXPLAIN ANALYZE 
SELECT * FROM services WHERE task = 'my-task' AND is_active = TRUE;

-- Index Scan using idx_services_is_active
-- Filter: (task = 'my-task')
```

### Storage Growth

Inactive records accumulate over time. Consider periodic archival or deletion:

```sql
-- Archive inactive records older than 90 days
DELETE FROM services 
WHERE is_active = FALSE 
  AND updated_at < NOW() - INTERVAL '90 days';

-- Or move to archive table
INSERT INTO services_archive 
SELECT * FROM services 
WHERE is_active = FALSE AND updated_at < NOW() - INTERVAL '90 days';

DELETE FROM services 
WHERE is_active = FALSE AND updated_at < NOW() - INTERVAL '90 days';
```

### Cache Behavior

- **Cache doesn't store inactive flag**: In-memory cache removes entries completely on cleanup
- **Cache sync only loads active services**: `WarmCacheFromDB()` filters `WHERE is_active = TRUE`
- **No performance impact on active operations**: Queries always include `is_active = TRUE` filter

## Configuration

No configuration changes needed. The new behavior is automatic once the migration is applied.

### Cleanup Interval

Cleanup frequency is controlled by the server flag:

```bash
go run ./cmd/server --cleanup-interval 10s
```

### Heartbeat Timeout

Services are marked inactive after missing heartbeat deadline:

```bash
go run ./cmd/server --heartbeat-timeout 60s
```

## Monitoring

Track cleanup operations in server logs:

```
[15:04:05] Cleanup marked 5 services as inactive
[STORE] Cleanup flagged 5 entries in database
```

The number reported is services marked inactive, not deleted.

## Backward Compatibility

- **Existing databases**: Migration adds `is_active` column, defaults to `TRUE` for all existing records
- **No API changes**: Standard registry methods work identically
- **Optional features**: Historical query methods are additions, not replacements

## Future Enhancements

Possible improvements:

- **Soft delete timestamp**: Track when service became inactive
- **Reactivation count**: Track how many times a service has cycled active/inactive
- **Inactive reason**: Distinguish between timeout cleanup vs manual deactivation
- **Auto-archival**: Scheduled job to move old inactive records to separate table
