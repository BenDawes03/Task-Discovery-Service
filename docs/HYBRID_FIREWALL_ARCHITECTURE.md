# Hybrid Firewall Architecture

## Overview

The TDS firewall implements a **hybrid architecture** with:
- **In-memory indexed rules** for fast query lookups (sub-microsecond)
- **Pluggable rule providers** (file-based or database-backed)
- **Static rules** that are loaded once at startup
- **Multi-server scalability** via optional database backend

## Architecture

```
┌─────────────────────────────────────────────────────┐
│         Application (TDS Server)                     │
├─────────────────────────────────────────────────────┤
│                                                       │
│  IndexedFirewall (Fast, In-Memory Index)            │
│  ├── O(1) source IP lookup                          │
│  ├── O(k) destination checks (k = rules per source) │
│  └── Statistics tracking                            │
│                                                       │
├─────────────────────────────────────────────────────┤
│  RulesProvider Interface (Abstraction Layer)        │
│                                                       │
│  ┌──────────────────┐    ┌──────────────────┐      │
│  │ FileRulesProvider│    │DatabaseRules     │      │
│  │                  │    │Provider          │      │
│  │ • Load from file │    │ • Query database │      │
│  │ • Auto-reload    │    │ • Audit trail    │      │
│  │ • Watch changes  │    │ • Multi-server   │      │
│  └──────────────────┘    └──────────────────┘      │
│         ↓                        ↓                   │
│    firewall_rules.txt       PostgreSQL DB           │
└─────────────────────────────────────────────────────┘
```

## Components

### 1. RulesProvider Interface

```go
type RulesProvider interface {
    GetRules() ([]FirewallRule, error)
    Close() error
}
```

**Purpose:** Abstract interface for rule sources. Allows swapping implementations without changing core logic.

### 2. FileRulesProvider

**Use Case:** Single-server deployments with static rules

**Features:**
- Load rules from text file at startup
- Fast static operation (no checking for file changes)
- Simple deployment

**Example:**
```go
provider, _ := firewall.NewFileRulesProvider("firewall_rules.txt")
```

### 3. DatabaseRulesProvider

**Use Case:** Multi-server deployments, enterprise with audit trails

**Features:**
- Rules stored in SQL database
- Loaded once at startup
- Supports multiple servers reading same rules
- Built-in audit trail capability

**Example:**
```go
db, _ := sql.Open("postgres", "postgres://...")
provider, _ := firewall.NewDatabaseRulesProvider(db)
```

### 4. IndexedFirewall

**Purpose:** High-performance rule checking with caching

**Performance:**
- Empty index: O(1) allow-all
- First lookup: O(n) to build index, then O(1) hash + O(k) checks
- Subsequent lookups: O(1) hash + O(k) checks

**Statistics Tracking:**
- TotalChecks: All queries
- TotalAllowed: Permitted connections
- TotalBlocked: Denied connections
- LastReloadTime: When rules were last loaded

## Usage Guide

### Single-Server with Static Rules

```go
provider, _ := firewall.NewFileRulesProvider("firewall_rules.txt")
defer provider.Close()

fw, _ := firewall.NewIndexedFirewall(provider)
defer fw.Close()

// Rules are loaded once at startup and remain static
// To update rules, restart the server with modified firewall_rules.txt
```

### Multi-Server Setup (Database-Backed)

```go
db, _ := sql.Open("postgres", connectionString)
defer db.Close()

provider, _ := firewall.NewDatabaseRulesProvider(db)
defer provider.Close()

fw, _ := firewall.NewIndexedFirewall(provider)
defer fw.Close()

// All servers read the same rules from database at startup
// To update rules, modify database and restart all servers
```

## Performance Characteristics

### Lookup Time (after index build)

| Scenario | Time | Notes |
|----------|------|-------|
| No rules | < 1µs | O(1) hash lookup, empty check |
| 1,000 rules, 10 per source | ~5µs | O(1) hash + O(10) network checks |
| 10,000 rules, 50 per source | ~25µs | O(1) hash + O(50) network checks |
| 100,000 rules, 100 per source | ~50µs | Still sub-100µs for most cases |

### Memory Usage

| Rules | Memory | Notes |
|-------|--------|-------|
| 100 | ~10 KB | Minimal overhead |
| 1,000 | ~100 KB | Practical single-server limit |
| 10,000 | ~1 MB | Comfortable for most deployments |
| 100,000+ | ~10+ MB | Consider database backend |

## Migration from Old Firewall

### Old API
```go
fw := firewall.NewFirewall()
fw.LoadFromFile("rules.txt")
fw.IsAllowed(src, dst)
```

### New API
```go
provider, _ := firewall.NewFileRulesProvider("rules.txt")
fw, _ := firewall.NewIndexedFirewall(provider)
fw.IsAllowed(src, dst)  // Same method signature!
```

**Key Difference:** Instantiation changed, but core methods are compatible.

## Testing

Run the comprehensive test suite:
```bash
go test ./pkg/firewall -v
```

Tests cover:
- File provider loading and reloading
- Index building and querying
- Address filtering
- Statistics tracking
- Empty rules (permissive mode)
- CIDR and single IP matching

## Database Schema (for PostgreSQL)

```sql
CREATE TABLE firewall_rules (
    id SERIAL PRIMARY KEY,
    source_ip TEXT,
    source_network TEXT,
    dest_ip TEXT,
    dest_network TEXT,
    created_at TIMESTAMP DEFAULT NOW(),
    created_by TEXT,
    description TEXT
);

CREATE INDEX idx_firewall_rules_source ON firewall_rules(source_ip, source_network);

-- Audit table (optional)
CREATE TABLE firewall_rules_audit (
    id SERIAL PRIMARY KEY,
    rule_id INTEGER,
    action TEXT,
    old_values JSONB,
    new_values JSONB,
    changed_by TEXT,
    changed_at TIMESTAMP DEFAULT NOW()
);
```

## Configuration Best Practices

### Single Server
- Use `FileRulesProvider` 
- Modify `firewall_rules.txt` as needed
- Restart server to apply changes

### Multi-Server
- Use `DatabaseRulesProvider`
- Store rules in centralized PostgreSQL
- Coordinate restart of all servers to apply rule changes
- Use database transactions for atomic rule updates

### High Traffic (>10k rules)
- Consider database backend even for single server
- Implement result caching layer above `IndexedFirewall` for frequently-queried pairs
- Use bloom filters for fast negative checks in very large rule sets

## Future Enhancements

1. **Decision Caching** - LRU cache for `src→dst` decisions
2. **Port Filtering** - Per-rule port ranges
3. **Rule Groups** - Named groups for easier management
4. **Rule Validation** - CLI tool to check rule consistency
5. **Statistics Export** - Prometheus/CloudWatch integration
6. **Performance Metrics** - Histogram of lookup times
7. **Dynamic Reloading** (optional) - File watching or polling for rule updates
