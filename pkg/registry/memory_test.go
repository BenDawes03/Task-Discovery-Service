package registry

import (
	"testing"
	"time"
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
