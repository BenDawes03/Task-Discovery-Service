package registry

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tds/pkg/firewall"
)

func TestRegisterPreservesCapacityOnHeartbeatUpdate(t *testing.T) {
	r := NewMemoryRegistry()
	task := "checkout"
	addr := "10.0.0.1:8080"

	r.RegisterWithCapacity(task, addr, 5)
	r.Register(task, addr)

	services := r.ListServices()
	entries := services[task]
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Capacity != 5 {
		t.Fatalf("expected capacity to be preserved at 5, got %d", entries[0].Capacity)
	}
}

func TestWeightedSelectionByCapacity(t *testing.T) {
	r := NewMemoryRegistry()
	task := "tickets"
	high := "10.0.0.10:8080"
	low := "10.0.0.11:8080"

	r.RegisterWithCapacity(task, high, 3)
	r.RegisterWithCapacity(task, low, 1)

	counts := map[string]int{}
	for i := 0; i < 8; i++ {
		selected, err := r.GetService(task)
		if err != nil {
			t.Fatalf("unexpected GetService error: %v", err)
		}
		counts[selected]++
	}

	if counts[high] != 6 || counts[low] != 2 {
		t.Fatalf("expected weighted 6:2 split, got %s=%d %s=%d", high, counts[high], low, counts[low])
	}
}

func TestWeightedSelectionByHeartbeatFreshness(t *testing.T) {
	r := NewMemoryRegistry()
	task := "routing"
	fresh := "10.0.0.20:8080"
	stale := "10.0.0.21:8080"

	// Same capacity; heartbeat freshness should bias selection.
	r.RegisterWithCapacity(task, fresh, 3)
	r.RegisterWithCapacity(task, stale, 3)

	r.mutex.Lock()
	now := time.Now()
	for i := range r.services[task] {
		if r.services[task][i].Address == fresh {
			r.services[task][i].LastHeartbeat = now
		}
		if r.services[task][i].Address == stale {
			r.services[task][i].LastHeartbeat = now.Add(-90 * time.Second)
		}
	}
	r.mutex.Unlock()

	counts := map[string]int{}
	for i := 0; i < 8; i++ {
		selected, err := r.GetService(task)
		if err != nil {
			t.Fatalf("unexpected GetService error: %v", err)
		}
		counts[selected]++
	}

	if counts[fresh] != 6 || counts[stale] != 2 {
		t.Fatalf("expected freshness-weighted 6:2 split, got %s=%d %s=%d", fresh, counts[fresh], stale, counts[stale])
	}
}

func TestCleanupRemovesStaleQueryCounters(t *testing.T) {
	r := NewMemoryRegistry()
	task := "analytics"
	addr := "10.0.0.30:8080"
	counterKey := task + ":" + addr

	r.RegisterWithCapacity(task, addr, 2)
	if _, err := r.GetService(task); err != nil {
		t.Fatalf("unexpected GetService error: %v", err)
	}
	if _, ok := r.queryCounters.Load(counterKey); !ok {
		t.Fatalf("expected query counter to exist before cleanup")
	}

	r.mutex.Lock()
	r.services[task][0].LastHeartbeat = time.Now().Add(-2 * time.Minute)
	r.mutex.Unlock()

	removed := r.Cleanup(30 * time.Second)
	if removed != 1 {
		t.Fatalf("expected 1 removed entry, got %d", removed)
	}

	if _, ok := r.queryCounters.Load(counterKey); ok {
		t.Fatalf("expected stale query counter to be removed")
	}
	if _, ok := r.services[task]; ok {
		t.Fatalf("expected task to be removed after cleanup")
	}
}

func TestRegisterWithInvalidTaskOrAddressIsIgnored(t *testing.T) {
	r := NewMemoryRegistry()
	r.Register("", "10.0.0.50:8080")
	r.Register("task-a", "")
	r.Register("   ", "10.0.0.50:8080")
	r.Register("task-a", "   ")

	services := r.ListServices()
	if len(services) != 0 {
		t.Fatalf("expected invalid registrations to be ignored, got %+v", services)
	}
}

func TestGetServiceWithInvalidTaskReturnsErrInvalidTaskName(t *testing.T) {
	r := NewMemoryRegistry()
	_, err := r.GetService("   ")
	if err != ErrInvalidTaskName {
		t.Fatalf("expected ErrInvalidTaskName, got %v", err)
	}
}

func TestHeartbeatFromUnregisteredServiceCreatesEntry(t *testing.T) {
	r := NewMemoryRegistry()
	task := "heartbeat-task"
	addr := "10.0.0.60:8080"

	// In this system, register is also the heartbeat signal.
	// A heartbeat for an unknown service is treated as first-time registration.
	r.Register(task, addr)

	services := r.ListServices()
	entries, ok := services[task]
	if !ok || len(entries) != 1 {
		t.Fatalf("expected heartbeat/register to create one service entry")
	}
	if entries[0].Address != addr {
		t.Fatalf("expected registered address %q, got %q", addr, entries[0].Address)
	}
	if entries[0].Capacity != 1 {
		t.Fatalf("expected default capacity 1, got %d", entries[0].Capacity)
	}
}

func TestGetServiceForRequestorFiltersByFirewall(t *testing.T) {
	r := NewMemoryRegistry()
	task := "firewall-task"
	allowedAddr := "10.10.0.1:8080"
	deniedAddr := "10.10.0.2:8080"

	r.RegisterWithCapacity(task, allowedAddr, 1)
	r.RegisterWithCapacity(task, deniedAddr, 1)
	r.SetDisableFreshness(true)

	rulesPath := filepath.Join(t.TempDir(), "rules.txt")
	if err := os.WriteFile(rulesPath, []byte("192.168.1.10 10.10.0.1\n"), 0o600); err != nil {
		t.Fatalf("failed to write firewall rules: %v", err)
	}

	fw, err := firewall.LoadFromFile(rulesPath)
	if err != nil {
		t.Fatalf("failed to load firewall rules: %v", err)
	}
	r.SetFirewall(fw)

	addr, err := r.GetServiceForRequestor(task, net.ParseIP("192.168.1.10"))
	if err != nil {
		t.Fatalf("unexpected GetServiceForRequestor error: %v", err)
	}
	if addr != allowedAddr {
		t.Fatalf("expected allowed address %q, got %q", allowedAddr, addr)
	}

	_, err = r.GetServiceForRequestor(task, net.ParseIP("192.168.1.11"))
	if err != ErrNoAllowedService {
		t.Fatalf("expected ErrNoAllowedService, got %v", err)
	}
}

func TestGetServiceForRequestorKeepsWeightedRotationWithinAllowedSubset(t *testing.T) {
	r := NewMemoryRegistry()
	task := "firewall-weighted-task"
	heavyAllowed := "10.20.0.1:8080"
	lightAllowed := "10.20.0.2:8080"
	denied := "10.20.0.3:8080"

	r.RegisterWithCapacity(task, heavyAllowed, 3)
	r.RegisterWithCapacity(task, lightAllowed, 1)
	r.RegisterWithCapacity(task, denied, 10)
	r.SetDisableFreshness(true)

	rulesPath := filepath.Join(t.TempDir(), "rules.txt")
	if err := os.WriteFile(rulesPath, []byte("192.168.1.10 10.20.0.1\n192.168.1.10 10.20.0.2\n"), 0o600); err != nil {
		t.Fatalf("failed to write firewall rules: %v", err)
	}

	fw, err := firewall.LoadFromFile(rulesPath)
	if err != nil {
		t.Fatalf("failed to load firewall rules: %v", err)
	}
	r.SetFirewall(fw)

	counts := map[string]int{}
	for i := 0; i < 8; i++ {
		selected, err := r.GetServiceForRequestor(task, net.ParseIP("192.168.1.10"))
		if err != nil {
			t.Fatalf("unexpected GetServiceForRequestor error: %v", err)
		}
		counts[selected]++
	}

	if counts[heavyAllowed] != 6 || counts[lightAllowed] != 2 {
		t.Fatalf("expected allowed subset weighted 6:2 split, got %s=%d %s=%d denied=%d", heavyAllowed, counts[heavyAllowed], lightAllowed, counts[lightAllowed], counts[denied])
	}
	if counts[denied] != 0 {
		t.Fatalf("expected denied address to never be selected, got %d", counts[denied])
	}
}

func TestParsedDestinationCachedOnRegisterAndRemovedOnCleanup(t *testing.T) {
	r := NewMemoryRegistry()
	task := "parsed-cache-task"
	addr := "10.30.0.1:8080"
	key := task + ":" + addr

	r.RegisterWithCapacity(task, addr, 1)

	v, ok := r.parsedDestIPs.Load(key)
	if !ok {
		t.Fatalf("expected parsed destination to be cached for %s", key)
	}
	ip, ok := v.(net.IP)
	if !ok || ip == nil || !ip.Equal(net.ParseIP("10.30.0.1")) {
		t.Fatalf("expected cached destination IP 10.30.0.1, got %#v", v)
	}

	r.mutex.Lock()
	r.services[task][0].LastHeartbeat = time.Now().Add(-2 * time.Minute)
	r.mutex.Unlock()

	removed := r.Cleanup(30 * time.Second)
	if removed != 1 {
		t.Fatalf("expected one removed entry, got %d", removed)
	}

	if _, ok := r.parsedDestIPs.Load(key); ok {
		t.Fatalf("expected parsed destination cache to be removed for %s", key)
	}
}

func TestListServicesReturnsDeepCopyAndSyncedQueryCounts(t *testing.T) {
	r := NewMemoryRegistry()
	task := "copy-task"
	addr := "10.0.0.70:8080"

	r.RegisterWithCapacity(task, addr, 2)
	if _, err := r.GetService(task); err != nil {
		t.Fatalf("unexpected GetService error: %v", err)
	}

	services := r.ListServices()
	if services[task][0].QueryCount != 1 {
		t.Fatalf("expected query count 1, got %d", services[task][0].QueryCount)
	}
	services[task][0].Address = "mutated"
	services[task] = append(services[task], ServiceEntry{Address: "extra"})

	refreshed := r.ListServices()
	if len(refreshed[task]) != 1 {
		t.Fatalf("expected internal service slice to remain unchanged, got %d entries", len(refreshed[task]))
	}
	if refreshed[task][0].Address != addr {
		t.Fatalf("expected original address %q, got %q", addr, refreshed[task][0].Address)
	}

	stats := r.GetStats()
	if stats.TotalQueries != 1 || stats.TotalTasks != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestWeightHelpersCoverEdgeCases(t *testing.T) {
	now := time.Now()
	entries := []ServiceEntry{
		{Address: "future", LastHeartbeat: now.Add(10 * time.Second), Capacity: 0},
		{Address: "stale", LastHeartbeat: now.Add(-90 * time.Second), Capacity: 3},
	}

	if got := serviceWeight(entries[0], now); got != 1 {
		t.Fatalf("expected zero capacity to normalize to weight 1, got %d", got)
	}
	if got := serviceWeightOpts(entries[1], now, true); got != 3 {
		t.Fatalf("expected no-freshness weight to equal capacity, got %d", got)
	}
	if got := totalServiceWeight(entries, now); got != 2 {
		t.Fatalf("expected total weight 2 with freshness decay, got %d", got)
	}
	if got := totalServiceWeightOpts(entries, now, true); got != 4 {
		t.Fatalf("expected total weight 4 without freshness decay, got %d", got)
	}

	addr, ok := selectWeightedAddress(entries, now, 0)
	if !ok || addr != "future" {
		t.Fatalf("expected slot 0 to choose future entry, got %q ok=%v", addr, ok)
	}
	addr, ok = selectWeightedAddressOpts(entries, now, 3, true)
	if !ok || addr != "stale" {
		t.Fatalf("expected slot 3 to choose stale entry without freshness decay, got %q ok=%v", addr, ok)
	}
	if _, ok := selectWeightedAddressOpts(entries, now, 4, true); ok {
		t.Fatalf("expected out-of-range slot selection to fail")
	}
}

func TestCleanupKeepsFreshEntries(t *testing.T) {
	r := NewMemoryRegistry()
	r.RegisterWithCapacity("mixed", "10.0.0.80:8080", 1)
	r.RegisterWithCapacity("mixed", "10.0.0.81:8080", 1)

	r.mutex.Lock()
	r.services["mixed"][0].LastHeartbeat = time.Now().Add(-2 * time.Minute)
	r.services["mixed"][1].LastHeartbeat = time.Now()
	r.mutex.Unlock()

	removed := r.Cleanup(30 * time.Second)
	if removed != 1 {
		t.Fatalf("expected one stale entry removed, got %d", removed)
	}
	services := r.ListServices()
	if len(services["mixed"]) != 1 || services["mixed"][0].Address != "10.0.0.81:8080" {
		t.Fatalf("expected fresh entry to remain, got %+v", services["mixed"])
	}
}
