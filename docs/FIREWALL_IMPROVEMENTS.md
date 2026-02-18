# Firewall Mode Improvements

## Current Implementation Analysis

### Strengths
✅ Simple, clear rule format  
✅ Supports both IP and CIDR notation  
✅ Integration with registry is clean  
✅ Permissive default (won't break existing setups)  

### Weaknesses & Improvement Opportunities

---

## 1. 🔴 **Performance: Linear Rule Search**

**Current Issue:**
```go
func (fw *Firewall) IsAllowed(sourceIP, destIP net.IP) bool {
    for _, rule := range fw.rules {  // O(n) linear search
        if fw.matchesSource(rule, sourceIP) && fw.matchesDest(rule, destIP) {
            return true
        }
    }
    return false
}
```

Every query checks ALL rules linearly. With 1000 rules and 10k queries/sec, that's 10M comparisons/second.

**Solutions:**

### Option A: Indexed Firewall (RECOMMENDED)
```go
type IndexedFirewall struct {
    // Map: sourceIP -> []destNetworks
    ipRules map[string][]*net.IPNet
    // Map: sourceCIDR -> []destNetworks  
    cidrRules map[string][]*net.IPNet
    // Catch-all rules
    wildcardRules []*net.IPNet
}

func (fw *IndexedFirewall) IsAllowed(sourceIP, destIP net.IP) bool {
    // O(1) hash lookup + O(k) network checks (k = rules for this source)
    if dests, ok := fw.ipRules[sourceIP.String()]; ok {
        for _, destNet := range dests {
            if destNet.Contains(destIP) {
                return true
            }
        }
    }
    // Check CIDR rules...
}
```

**Performance:** O(n) → O(k) where k << n (rules per source IP)

### Option B: Decision Cache
```go
type CachedFirewall struct {
    fw    *Firewall
    cache sync.Map // key: "srcIP:dstIP" -> bool
}

func (cfw *CachedFirewall) IsAllowed(sourceIP, destIP net.IP) bool {
    key := sourceIP.String() + ":" + destIP.String()
    if cached, ok := cfw.cache.Load(key); ok {
        return cached.(bool)
    }
    
    result := cfw.fw.IsAllowed(sourceIP, destIP)
    cfw.cache.Store(key, result)
    return result
}
```

**Benefit:** Repeated queries for same src→dst pair are instant

---

## 2. 🟡 **Dynamic Rule Reloading**

**Current:** Requires server restart to update rules

**Improvement:**
```go
type Firewall struct {
    mutex    sync.RWMutex
    rules    []FirewallRule
    filePath string
    lastMod  time.Time
}

func (fw *Firewall) ReloadIfChanged() error {
    info, err := os.Stat(fw.filePath)
    if err != nil {
        return err
    }
    
    fw.mutex.RLock()
    needReload := info.ModTime().After(fw.lastMod)
    fw.mutex.RUnlock()
    
    if needReload {
        return fw.Reload()
    }
    return nil
}

func (fw *Firewall) Reload() error {
    newRules, err := loadRulesFromFile(fw.filePath)
    if err != nil {
        return err
    }
    
    fw.mutex.Lock()
    fw.rules = newRules
    fw.lastMod = time.Now()
    fw.mutex.Unlock()
    
    return nil
}
```

**Usage:** Background goroutine checks file every 30s

---

## 3. 🟢 **Enhanced Rule Features**

### A. Bidirectional Rules
```
# Current: One-way only
192.168.1.10 10.0.0.5

# Enhanced: Bidirectional
192.168.1.10 <-> 10.0.0.5
```

### B. Port-Level Filtering
```
# Allow specific port
192.168.1.10 10.0.0.5:8080

# Allow port range
192.168.1.10 10.0.0.5:8000-9000

# Any port (current behavior)
192.168.1.10 10.0.0.5:*
```

### C. Wildcard/Any Rules
```
# Allow from any source
* 10.0.0.5

# Allow to any destination
192.168.1.10 *

# Allow everything (explicit)
* *
```

### D. Deny Rules (Explicit Deny)
```
# Syntax: DENY source dest
DENY 192.168.1.100 10.0.0.5
ALLOW 192.168.1.0/24 10.0.0.5
```

**Rule priority:** First match wins (like iptables)

---

## 4. 🟡 **Observability & Metrics**

### A. Rule Hit Counters
```go
type FirewallRule struct {
    SourceIP      net.IP
    SourceNetwork *net.IPNet
    DestIP        net.IP
    DestNetwork   *net.IPNet
    
    // Metrics
    HitCount    atomic.Int64
    LastHit     atomic.Int64 // Unix timestamp
    BlockCount  atomic.Int64 // If it's a deny rule
}
```

### B. Blocked Request Logging
```go
func (fw *Firewall) IsAllowed(sourceIP, destIP net.IP) bool {
    allowed := fw.checkRules(sourceIP, destIP)
    
    if !allowed && fw.logBlocked {
        log.Printf("[FIREWALL] BLOCKED: %s → %s", sourceIP, destIP)
    }
    
    return allowed
}
```

### C. Statistics Endpoint
```go
type FirewallStats struct {
    TotalChecks     int64
    TotalAllowed    int64
    TotalBlocked    int64
    TopBlockedPairs []BlockedPair
    RuleHitCounts   map[int]int64
}

func (fw *Firewall) GetStats() FirewallStats
```

---

## 5. 🟢 **Rule Validation & Testing**

### A. Syntax Validation
```go
func ValidateRules(filePath string) error {
    rules, err := LoadFromFile(filePath)
    if err != nil {
        return err
    }
    
    // Check for contradictions
    for i, r1 := range rules.rules {
        for j, r2 := range rules.rules[i+1:] {
            if hasOverlap(r1, r2) {
                log.Warnf("Rule %d overlaps with rule %d", i, i+j+1)
            }
        }
    }
    
    return nil
}
```

### B. Dry-Run Test Mode
```go
// Test rules without applying
tdsServer --firewall-rules rules.txt --firewall-dry-run

// Logs what WOULD be blocked without actually blocking
```

### C. Rule Testing Utility
```go
// Test specific src→dst pairs against rules
func TestFirewallRule(rulesFile, srcIP, dstIP string) bool
```

---

## 6. 🟡 **Configuration Improvements**

### A. Default Policy Configuration
```yaml
# firewall_config.yaml
default_policy: deny  # or "allow"
rules_file: firewall_rules.txt
log_blocked: true
log_allowed: false
cache_enabled: true
```

### B. Multiple Rule Files
```bash
--firewall-rules /etc/tds/base_rules.txt,/etc/tds/custom_rules.txt
```

### C. Environment-Based Rules
```bash
# Development: permissive
--firewall-mode=dev

# Production: strict
--firewall-mode=prod --firewall-rules prod_rules.txt
```

---

## 7. 🟢 **Advanced Features**

### A. Time-Based Rules
```
# Allow only during business hours
192.168.1.10 10.0.0.5 HOURS=09:00-17:00

# Allow on specific days
192.168.1.10 10.0.0.5 DAYS=MON-FRI
```

### B. Rate Limiting Integration
```
# Allow with rate limit
192.168.1.10 10.0.0.5 RATE=100/minute
```

### C. Rule Groups & Aliases
```
# Define groups
@clients = 192.168.1.0/24
@servers = 10.0.0.0/24
@admin = 192.168.1.100

# Use in rules
@clients @servers
@admin *
```

### D. Geolocation-Based Rules
```
# Block specific countries (requires GeoIP database)
DENY country:CN *
DENY country:RU *
```

---

## Implementation Priority

### 🔴 High Priority (Immediate Impact)
1. **Rule indexing/caching** - 10-100x performance improvement
2. **Dynamic reload** - Operational necessity
3. **Metrics & logging** - Visibility into firewall operation

### 🟡 Medium Priority (Enhanced Functionality)
4. **Bidirectional rules** - Common use case
5. **Port-level filtering** - More granular control
6. **Deny rules** - Explicit deny capability
7. **Default policy config** - Security best practice

### 🟢 Low Priority (Nice to Have)
8. **Wildcard rules** - Convenience feature
9. **Rule validation** - Development aid
10. **Advanced features** - Complex scenarios

---

## Example: Enhanced Firewall Rules File

```
# TDS Enhanced Firewall Rules
# Version: 2.0

# Configuration
DEFAULT_POLICY: deny
LOG_BLOCKED: true

# Rule Groups
@web_clients = 192.168.1.0/24
@api_servers = 10.0.0.0/24
@admin = 192.168.1.100

# Allow web clients to access API servers (bidirectional)
@web_clients <-> @api_servers:8080-8090

# Admin has full access
@admin *

# Deny suspicious IP
DENY 192.168.1.99 *

# Allow specific service pairs
192.168.1.10 10.0.0.5:8080
192.168.1.11 10.0.0.6:8080

# Time-based rule
192.168.1.50 10.0.0.10 HOURS=09:00-17:00 DAYS=MON-FRI
```

---

## Code Structure Improvements

### Suggested Package Organization
```
pkg/firewall/
├── firewall.go          # Core firewall logic
├── indexed.go           # IndexedFirewall implementation
├── cached.go            # CachedFirewall implementation
├── parser.go            # Rule parsing
├── validator.go         # Rule validation
├── metrics.go           # Statistics & metrics
├── reload.go            # Dynamic reloading
└── advanced.go          # Advanced features (groups, time, etc.)
```

---

## Backwards Compatibility

All enhancements should be backwards compatible:
- Existing simple rules continue to work
- New features are opt-in via syntax or flags
- Default behavior unchanged (permissive when no rules)

---

## Testing Strategy

```go
// Benchmark current vs optimized
func BenchmarkFirewallLinear(b *testing.B)
func BenchmarkFirewallIndexed(b *testing.B)
func BenchmarkFirewallCached(b *testing.B)

// Load test
func TestFirewallUnderLoad(t *testing.T) {
    // 1000 rules, 10k concurrent queries
}

// Correctness tests
func TestFirewallBidirectional(t *testing.T)
func TestFirewallPortFiltering(t *testing.T)
func TestFirewallDenyRules(t *testing.T)
```

---

## Migration Path

### Phase 1: Performance (Week 1)
- Implement indexed firewall
- Add decision cache
- Benchmark improvements

### Phase 2: Operations (Week 2)
- Dynamic reload
- Metrics & logging
- Configuration file

### Phase 3: Features (Week 3-4)
- Bidirectional rules
- Port filtering
- Deny rules
- Rule validation

### Phase 4: Advanced (Future)
- Time-based rules
- Rate limiting
- Rule groups
