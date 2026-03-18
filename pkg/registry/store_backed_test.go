package registry

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tds/pkg/firewall"
	"tds/pkg/store"
)

type fakeStore struct {
	mu                sync.Mutex
	registerCalls     []fakeRegisterCall
	incrementCalls    []fakeIncrementCall
	registerErrByTask map[string]error
	incrementErrByKey map[string]error
	getEntries        map[string]*store.ServiceEntry
	getErrByTask      map[string]error
	listServices      map[string][]store.ServiceEntry
	listErr           error
	listCalls         int
	listTaskCalls     int
	topTasksCalls     int
	listSubsetCalls   int
	cleanupRemoved    int64
	cleanupErr        error
}

type fakeRegisterCall struct {
	task  string
	entry store.ServiceEntry
}

type fakeIncrementCall struct {
	task    string
	address string
}

func (f *fakeStore) Register(_ context.Context, task string, entry *store.ServiceEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registerCalls = append(f.registerCalls, fakeRegisterCall{task: task, entry: *entry})
	if err := f.registerErrByTask[task]; err != nil {
		return err
	}
	return nil
}

func (f *fakeStore) IncrementQueryCount(_ context.Context, task, address string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.incrementCalls = append(f.incrementCalls, fakeIncrementCall{task: task, address: address})
	if err := f.incrementErrByKey[task+":"+address]; err != nil {
		return err
	}
	return nil
}

func (f *fakeStore) GetService(_ context.Context, task string) (*store.ServiceEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.getErrByTask[task]; err != nil {
		return nil, err
	}
	entry, ok := f.getEntries[task]
	if !ok {
		return nil, ErrNotFound
	}
	copyEntry := *entry
	return &copyEntry, nil
}

func (f *fakeStore) ListServices(_ context.Context) (map[string][]store.ServiceEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	copyMap := make(map[string][]store.ServiceEntry, len(f.listServices))
	for task, entries := range f.listServices {
		sliceCopy := make([]store.ServiceEntry, len(entries))
		copy(sliceCopy, entries)
		copyMap[task] = sliceCopy
	}
	return copyMap, nil
}

func (f *fakeStore) ListTaskServices(_ context.Context, task string) ([]store.ServiceEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listTaskCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	entries, ok := f.listServices[task]
	if !ok {
		return []store.ServiceEntry{}, nil
	}
	sliceCopy := make([]store.ServiceEntry, len(entries))
	copy(sliceCopy, entries)
	return sliceCopy, nil
}

func (f *fakeStore) ListTopTasksByQueryCount(_ context.Context, limit int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.topTasksCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}

	type taskCount struct {
		task  string
		count int64
	}
	counts := make([]taskCount, 0, len(f.listServices))
	for task, entries := range f.listServices {
		total := int64(0)
		for _, entry := range entries {
			total += entry.QueryCount
		}
		counts = append(counts, taskCount{task: task, count: total})
	}

	for i := 0; i < len(counts); i++ {
		for j := i + 1; j < len(counts); j++ {
			if counts[j].count > counts[i].count || (counts[j].count == counts[i].count && counts[j].task < counts[i].task) {
				counts[i], counts[j] = counts[j], counts[i]
			}
		}
	}

	if limit > len(counts) {
		limit = len(counts)
	}
	tasks := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		tasks = append(tasks, counts[i].task)
	}
	return tasks, nil
}

func (f *fakeStore) ListServicesForTasks(_ context.Context, tasks []string) (map[string][]store.ServiceEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listSubsetCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}

	result := make(map[string][]store.ServiceEntry, len(tasks))
	for _, task := range tasks {
		entries, ok := f.listServices[task]
		if !ok {
			continue
		}
		sliceCopy := make([]store.ServiceEntry, len(entries))
		copy(sliceCopy, entries)
		result[task] = sliceCopy
	}
	return result, nil
}

func (f *fakeStore) Cleanup(_ context.Context, _ time.Duration) (int64, error) {
	return f.cleanupRemoved, f.cleanupErr
}

func (f *fakeStore) Migrate(context.Context) error {
	return nil
}

func (f *fakeStore) Close() error {
	return nil
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		registerErrByTask: make(map[string]error),
		incrementErrByKey: make(map[string]error),
		getEntries:        make(map[string]*store.ServiceEntry),
		getErrByTask:      make(map[string]error),
		listServices:      make(map[string][]store.ServiceEntry),
	}
}

func testFirewall(t *testing.T, rules string) *firewall.Firewall {
	t.Helper()
	rulesPath := filepath.Join(t.TempDir(), "rules.txt")
	if err := os.WriteFile(rulesPath, []byte(rules), 0o600); err != nil {
		t.Fatalf("failed to write firewall rules: %v", err)
	}
	fw, err := firewall.LoadFromFile(rulesPath)
	if err != nil {
		t.Fatalf("failed to load firewall rules: %v", err)
	}
	return fw
}

func TestStoreBackedSyncQueryCountsToDBOnlyWritesQueriedEntries(t *testing.T) {
	storeStub := newFakeStore()
	sr := NewStoreBackedRegistry(storeStub, 0)

	sr.currentMemCache().RegisterWithCapacity("queried", "10.0.0.1:8080", 4)
	sr.currentMemCache().RegisterWithCapacity("idle", "10.0.0.2:8080", 2)
	if _, err := sr.currentMemCache().GetService("queried"); err != nil {
		t.Fatalf("unexpected GetService error: %v", err)
	}

	if err := sr.syncQueryCountsToDB(context.Background()); err != nil {
		t.Fatalf("unexpected syncQueryCountsToDB error: %v", err)
	}

	if len(storeStub.registerCalls) != 1 {
		t.Fatalf("expected one register call for queried entry, got %d", len(storeStub.registerCalls))
	}
	call := storeStub.registerCalls[0]
	if call.task != "queried" {
		t.Fatalf("expected queried task to be synced, got %q", call.task)
	}
	if call.entry.QueryCount != 1 || call.entry.Capacity != 4 {
		t.Fatalf("expected synced entry to preserve query count and capacity, got %+v", call.entry)
	}
}

func TestWarmCacheFromDBRespectsLFULimitAndPropagatesFirewall(t *testing.T) {
	storeStub := newFakeStore()
	storeStub.listServices = map[string][]store.ServiceEntry{
		"hot":  {{Address: "10.0.0.10:8080", QueryCount: 9, Capacity: 3, LastHeartbeat: time.Now()}},
		"warm": {{Address: "10.0.0.11:8080", QueryCount: 4, Capacity: 2, LastHeartbeat: time.Now()}},
		"cold": {{Address: "10.0.0.12:8080", QueryCount: 1, Capacity: 1, LastHeartbeat: time.Now()}},
	}

	sr := NewStoreBackedRegistry(storeStub, 2)
	fw := testFirewall(t, "192.168.1.10 10.0.0.10\n192.168.1.10 10.0.0.11\n")
	sr.SetFirewall(fw)

	if err := sr.WarmCacheFromDB(context.Background()); err != nil {
		t.Fatalf("unexpected WarmCacheFromDB error: %v", err)
	}

	services := sr.ListServices()
	if len(services) != 2 {
		t.Fatalf("expected top two tasks in cache, got %d", len(services))
	}
	if _, ok := services["hot"]; !ok {
		t.Fatalf("expected hot task to be cached")
	}
	if _, ok := services["warm"]; !ok {
		t.Fatalf("expected warm task to be cached")
	}
	if _, ok := services["cold"]; ok {
		t.Fatalf("did not expect cold task to be cached")
	}
	if storeStub.topTasksCalls != 1 {
		t.Fatalf("expected one top-task query during bounded warm-up, got %d", storeStub.topTasksCalls)
	}
	if storeStub.listSubsetCalls != 1 {
		t.Fatalf("expected one subset task fetch during bounded warm-up, got %d", storeStub.listSubsetCalls)
	}
	if storeStub.listCalls != 0 {
		t.Fatalf("did not expect full ListServices scan during bounded warm-up, got %d", storeStub.listCalls)
	}

	addr, err := sr.GetServiceForRequestor("hot", net.ParseIP("192.168.1.10"))
	if err != nil {
		t.Fatalf("unexpected firewall-allowed lookup error: %v", err)
	}
	if addr != "10.0.0.10:8080" {
		t.Fatalf("expected hot address from cache, got %q", addr)
	}
}

func TestPopulateCacheEntryUpdatesExistingEntry(t *testing.T) {
	sr := NewStoreBackedRegistry(newFakeStore(), 0)
	cache := NewMemoryRegistry()
	firstHeartbeat := time.Now().Add(-time.Minute)
	secondHeartbeat := time.Now()

	sr.populateCacheEntry(cache, "task", "10.0.0.20:8080", 2, firstHeartbeat, 2)
	sr.populateCacheEntry(cache, "task", "10.0.0.20:8080", 7, secondHeartbeat, 5)

	services := cache.ListServices()
	entries := services["task"]
	if len(entries) != 1 {
		t.Fatalf("expected one cache entry, got %d", len(entries))
	}
	if entries[0].Capacity != 5 {
		t.Fatalf("expected capacity update to 5, got %d", entries[0].Capacity)
	}
	if !entries[0].LastHeartbeat.Equal(secondHeartbeat) {
		t.Fatalf("expected latest heartbeat to be preserved")
	}
	if entries[0].QueryCount != 7 {
		t.Fatalf("expected query count 7, got %d", entries[0].QueryCount)
	}
}

func TestStoreBackedRegisterGetServiceAndCleanup(t *testing.T) {
	storeStub := newFakeStore()
	storeStub.listServices = map[string][]store.ServiceEntry{
		"dbtask": {{Address: "10.0.0.30:8080", Capacity: 6, LastHeartbeat: time.Now()}},
	}
	storeStub.cleanupRemoved = 2

	sr := NewStoreBackedRegistry(storeStub, 0)
	sr.RegisterWithCapacity(" regtask ", " 10.0.0.31:8080 ", 0)

	if len(storeStub.registerCalls) != 1 {
		t.Fatalf("expected one store register call, got %d", len(storeStub.registerCalls))
	}
	regCall := storeStub.registerCalls[0]
	if regCall.task != "regtask" || regCall.entry.Address != "10.0.0.31:8080" || regCall.entry.Capacity != 1 {
		t.Fatalf("unexpected register call: %+v", regCall)
	}

	addr, err := sr.GetService("regtask")
	if err != nil {
		t.Fatalf("unexpected cache-hit GetService error: %v", err)
	}
	if addr != "10.0.0.31:8080" {
		t.Fatalf("expected cached address, got %q", addr)
	}

	addr, err = sr.GetService("dbtask")
	if err != nil {
		t.Fatalf("unexpected cache-miss GetService error: %v", err)
	}
	if addr != "10.0.0.30:8080" {
		t.Fatalf("expected DB-hydrated address, got %q", addr)
	}

	if _, err := sr.GetService("   "); err != ErrInvalidTaskName {
		t.Fatalf("expected ErrInvalidTaskName, got %v", err)
	}

	sr.currentMemCache().mutex.Lock()
	sr.currentMemCache().services["regtask"][0].LastHeartbeat = time.Now().Add(-time.Hour)
	sr.currentMemCache().mutex.Unlock()

	removed := sr.Cleanup(30 * time.Second)
	if removed != 3 {
		t.Fatalf("expected cleanup total 3, got %d", removed)
	}
}

func TestStoreBackedGetServiceForRequestorUsesDBFallbackAndFirewall(t *testing.T) {
	storeStub := newFakeStore()
	storeStub.listServices = map[string][]store.ServiceEntry{
		"allowed": {{Address: "10.0.0.40:8080", Capacity: 2, LastHeartbeat: time.Now()}},
		"denied":  {{Address: "10.0.0.41:8080", Capacity: 2, LastHeartbeat: time.Now()}},
	}

	sr := NewStoreBackedRegistry(storeStub, 0)
	sr.SetFirewall(testFirewall(t, "192.168.1.10 10.0.0.40\n"))

	addr, err := sr.GetServiceForRequestor("allowed", net.ParseIP("192.168.1.10"))
	if err != nil {
		t.Fatalf("unexpected allowed requestor error: %v", err)
	}
	if addr != "10.0.0.40:8080" {
		t.Fatalf("expected allowed DB-hydrated address, got %q", addr)
	}

	_, err = sr.GetServiceForRequestor("denied", net.ParseIP("192.168.1.11"))
	if err != ErrNoAllowedService {
		t.Fatalf("expected ErrNoAllowedService after DB fallback, got %v", err)
	}
}

func TestStoreBackedListServicesTriggersWarmCacheAndSurvivesWarmErrors(t *testing.T) {
	storeStub := newFakeStore()
	storeStub.listServices = map[string][]store.ServiceEntry{
		"dbtask": {{Address: "10.0.0.50:8080", QueryCount: 5, Capacity: 2, LastHeartbeat: time.Now()}},
	}

	sr := NewStoreBackedRegistry(storeStub, 0)
	sr.currentMemCache().RegisterWithCapacity("cached", "10.0.0.51:8080", 4)
	if _, err := sr.currentMemCache().GetService("cached"); err != nil {
		t.Fatalf("unexpected cache query error: %v", err)
	}
	sr.cacheMutex.Lock()
	sr.lastCacheSync = time.Now().Add(-sr.cacheSyncPeriod - time.Second)
	sr.cacheMutex.Unlock()

	services := sr.ListServices()
	if storeStub.listCalls != 1 {
		t.Fatalf("expected one DB list call during warm-up, got %d", storeStub.listCalls)
	}
	if len(storeStub.registerCalls) != 1 || storeStub.registerCalls[0].task != "cached" {
		t.Fatalf("expected queried cache entry to sync before warm-up, got %+v", storeStub.registerCalls)
	}
	if _, ok := services["dbtask"]; !ok {
		t.Fatalf("expected warmed DB task in cache, got %+v", services)
	}

	storeStub.listErr = errors.New("db unavailable")
	sr.cacheMutex.Lock()
	sr.lastCacheSync = time.Now().Add(-sr.cacheSyncPeriod - time.Second)
	sr.cacheMutex.Unlock()

	services = sr.ListServices()
	if _, ok := services["dbtask"]; !ok {
		t.Fatalf("expected previous cache contents to survive warm-up failure")
	}
}

func TestStoreBackedGetServicePropagatesStoreErrors(t *testing.T) {
	storeStub := newFakeStore()
	storeStub.listErr = errors.New("lookup failed")
	sr := NewStoreBackedRegistry(storeStub, 0)

	_, err := sr.GetService("missing")
	if err == nil || err.Error() != "lookup failed" {
		t.Fatalf("expected store error to propagate, got %v", err)
	}
}

func TestStoreBackedCacheMissHydratesTaskAndUsesUnifiedWeightedCursor(t *testing.T) {
	storeStub := newFakeStore()
	storeStub.listServices = map[string][]store.ServiceEntry{
		"weighted": {
			{Address: "10.1.0.1:8080", Capacity: 3, LastHeartbeat: time.Now()},
			{Address: "10.1.0.2:8080", Capacity: 1, LastHeartbeat: time.Now()},
		},
	}

	sr := NewStoreBackedRegistry(storeStub, 0)

	got := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		addr, err := sr.GetService("weighted")
		if err != nil {
			t.Fatalf("unexpected GetService error at %d: %v", i, err)
		}
		got = append(got, addr)
	}

	want := []string{"10.1.0.1:8080", "10.1.0.1:8080", "10.1.0.1:8080", "10.1.0.2:8080", "10.1.0.1:8080"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected weighted sequence at %d: got %q want %q (full=%v)", i, got[i], want[i], got)
		}
	}

	if storeStub.listTaskCalls != 1 {
		t.Fatalf("expected one task-scoped store list call for first cache miss only, got %d", storeStub.listTaskCalls)
	}
}

func TestStoreBackedRegisterAliasInvalidInputAndStats(t *testing.T) {
	storeStub := newFakeStore()
	sr := NewStoreBackedRegistry(storeStub, 0)

	sr.Register(" alias-task ", " 10.0.0.60:8080 ")
	sr.Register("", "10.0.0.61:8080")
	sr.Register("alias-task", "")

	if len(storeStub.registerCalls) != 1 {
		t.Fatalf("expected exactly one valid store register call, got %d", len(storeStub.registerCalls))
	}
	if storeStub.registerCalls[0].entry.Capacity != 1 {
		t.Fatalf("expected Register alias to use capacity 1, got %d", storeStub.registerCalls[0].entry.Capacity)
	}

	if _, err := sr.GetService("alias-task"); err != nil {
		t.Fatalf("unexpected cache-hit GetService error: %v", err)
	}

	stats := sr.GetStats()
	if stats.TotalTasks != 1 || stats.TotalQueries != 1 {
		t.Fatalf("unexpected stats after alias Register/GetService: %+v", stats)
	}
}

func TestStoreBackedRegisterFailureUsesLogger(t *testing.T) {
	storeStub := newFakeStore()
	storeStub.registerErrByTask["task-a"] = errors.New("db write failed")
	sr := NewStoreBackedRegistry(storeStub, 0)

	var messages []string
	var messagesMu sync.Mutex
	sr.SetLogger(func(message string) {
		messagesMu.Lock()
		defer messagesMu.Unlock()
		messages = append(messages, message)
	})

	sr.RegisterWithCapacity("task-a", "10.0.0.99:8080", 1)

	messagesMu.Lock()
	defer messagesMu.Unlock()
	if len(messages) != 1 {
		t.Fatalf("expected one logged message, got %d", len(messages))
	}
	if !strings.Contains(messages[0], "[STORE] Register failed: db write failed") {
		t.Fatalf("unexpected log message: %q", messages[0])
	}
}

func TestStoreBackedWarmCacheWithoutLimitAndSyncErrorsIgnored(t *testing.T) {
	storeStub := newFakeStore()
	storeStub.registerErrByTask["queried"] = errors.New("sync failed")
	storeStub.listServices = map[string][]store.ServiceEntry{
		"one": {{Address: "10.0.0.70:8080", QueryCount: 2, Capacity: 1, LastHeartbeat: time.Now()}},
		"two": {{Address: "10.0.0.71:8080", QueryCount: 1, Capacity: 3, LastHeartbeat: time.Now()}},
	}

	sr := NewStoreBackedRegistry(storeStub, 0)
	sr.currentMemCache().RegisterWithCapacity("queried", "10.0.0.72:8080", 2)
	if _, err := sr.currentMemCache().GetService("queried"); err != nil {
		t.Fatalf("unexpected cache query error: %v", err)
	}

	if err := sr.WarmCacheFromDB(context.Background()); err != nil {
		t.Fatalf("expected WarmCacheFromDB to ignore sync write failures, got %v", err)
	}

	services := sr.ListServices()
	if len(services) != 2 {
		t.Fatalf("expected all DB tasks to be cached with unlimited size, got %d", len(services))
	}
	if _, ok := services["one"]; !ok {
		t.Fatalf("expected task one to be cached")
	}
	if _, ok := services["two"]; !ok {
		t.Fatalf("expected task two to be cached")
	}
}

func TestStoreBackedCleanupReturnsCacheRemovalsWhenStoreCleanupFails(t *testing.T) {
	storeStub := newFakeStore()
	storeStub.cleanupErr = errors.New("cleanup failed")
	sr := NewStoreBackedRegistry(storeStub, 0)
	sr.RegisterWithCapacity("stale", "10.0.0.90:8080", 1)

	sr.currentMemCache().mutex.Lock()
	sr.currentMemCache().services["stale"][0].LastHeartbeat = time.Now().Add(-time.Hour)
	sr.currentMemCache().mutex.Unlock()

	removed := sr.Cleanup(30 * time.Second)
	if removed != 1 {
		t.Fatalf("expected cache removal count when store cleanup fails, got %d", removed)
	}
}

func TestStoreBackedWarmCachePreservesRoundRobinCursor(t *testing.T) {
	storeStub := newFakeStore()
	storeStub.listServices = map[string][]store.ServiceEntry{
		"task_web": {
			{Address: "10.0.0.1:8080", Capacity: 1, LastHeartbeat: time.Now()},
			{Address: "10.0.0.2:8080", Capacity: 1, LastHeartbeat: time.Now()},
			{Address: "10.0.0.3:8080", Capacity: 1, LastHeartbeat: time.Now()},
		},
	}

	sr := NewStoreBackedRegistry(storeStub, 0)
	if err := sr.WarmCacheFromDB(context.Background()); err != nil {
		t.Fatalf("unexpected initial warm error: %v", err)
	}

	gotBeforeWarm := make([]string, 0, 4)
	for i := 0; i < 4; i++ {
		addr, err := sr.GetService("task_web")
		if err != nil {
			t.Fatalf("unexpected GetService error before warm: %v", err)
		}
		gotBeforeWarm = append(gotBeforeWarm, addr)
	}

	wantBeforeWarm := []string{"10.0.0.1:8080", "10.0.0.2:8080", "10.0.0.3:8080", "10.0.0.1:8080"}
	for i := range wantBeforeWarm {
		if gotBeforeWarm[i] != wantBeforeWarm[i] {
			t.Fatalf("unexpected pre-warm sequence at %d: got %q want %q (full=%v)", i, gotBeforeWarm[i], wantBeforeWarm[i], gotBeforeWarm)
		}
	}

	if err := sr.WarmCacheFromDB(context.Background()); err != nil {
		t.Fatalf("unexpected second warm error: %v", err)
	}

	addrAfterWarm, err := sr.GetService("task_web")
	if err != nil {
		t.Fatalf("unexpected GetService error after warm: %v", err)
	}
	if addrAfterWarm != "10.0.0.2:8080" {
		t.Fatalf("expected sequence to continue with 10.0.0.2:8080 after warm, got %q", addrAfterWarm)
	}
}

func TestStoreBackedWarmCachePreservesWeightedCursorState(t *testing.T) {
	storeStub := newFakeStore()
	storeStub.listServices = map[string][]store.ServiceEntry{
		"weighted": {
			{Address: "10.0.1.1:8080", Capacity: 3, LastHeartbeat: time.Now()},
			{Address: "10.0.1.2:8080", Capacity: 1, LastHeartbeat: time.Now()},
		},
	}

	sr := NewStoreBackedRegistry(storeStub, 0)
	if err := sr.WarmCacheFromDB(context.Background()); err != nil {
		t.Fatalf("unexpected initial warm error: %v", err)
	}

	gotBeforeWarm := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		addr, err := sr.GetService("weighted")
		if err != nil {
			t.Fatalf("unexpected GetService error before warm: %v", err)
		}
		gotBeforeWarm = append(gotBeforeWarm, addr)
	}

	wantBeforeWarm := []string{"10.0.1.1:8080", "10.0.1.1:8080", "10.0.1.1:8080"}
	for i := range wantBeforeWarm {
		if gotBeforeWarm[i] != wantBeforeWarm[i] {
			t.Fatalf("unexpected weighted pre-warm sequence at %d: got %q want %q (full=%v)", i, gotBeforeWarm[i], wantBeforeWarm[i], gotBeforeWarm)
		}
	}

	if err := sr.WarmCacheFromDB(context.Background()); err != nil {
		t.Fatalf("unexpected second warm error: %v", err)
	}

	addrAfterWarm, err := sr.GetService("weighted")
	if err != nil {
		t.Fatalf("unexpected GetService error after warm: %v", err)
	}
	if addrAfterWarm != "10.0.1.2:8080" {
		t.Fatalf("expected weighted sequence to continue with 10.0.1.2:8080 after warm, got %q", addrAfterWarm)
	}
}

func TestStoreBackedAdmissionThresholdDefersCacheHydration(t *testing.T) {
	storeStub := newFakeStore()
	storeStub.listServices = map[string][]store.ServiceEntry{
		"deferred": {{Address: "10.0.2.1:8080", Capacity: 1, LastHeartbeat: time.Now()}},
	}

	sr := NewStoreBackedRegistry(storeStub, 0)
	sr.SetCacheAdmissionMissThreshold(3)

	for i := 0; i < 2; i++ {
		addr, err := sr.GetService("deferred")
		if err != nil {
			t.Fatalf("unexpected GetService error on deferred miss %d: %v", i, err)
		}
		if addr != "10.0.2.1:8080" {
			t.Fatalf("unexpected address on deferred miss %d: %q", i, addr)
		}
	}

	if len(storeStub.incrementCalls) != 2 {
		t.Fatalf("expected DB increment for each non-admitted miss, got %d", len(storeStub.incrementCalls))
	}
	if _, ok := sr.currentMemCache().ListServices()["deferred"]; ok {
		t.Fatalf("task should not be admitted before threshold is reached")
	}

	addr, err := sr.GetService("deferred")
	if err != nil {
		t.Fatalf("unexpected GetService error on admission miss: %v", err)
	}
	if addr != "10.0.2.1:8080" {
		t.Fatalf("unexpected address on admission miss: %q", addr)
	}

	if len(storeStub.incrementCalls) != 2 {
		t.Fatalf("expected no direct DB increment once admitted to cache, got %d", len(storeStub.incrementCalls))
	}
	if _, ok := sr.currentMemCache().ListServices()["deferred"]; !ok {
		t.Fatalf("task should be admitted once threshold is reached")
	}
}

func TestStoreBackedNonAdmittedMissIncrementErrorPropagates(t *testing.T) {
	storeStub := newFakeStore()
	storeStub.listServices = map[string][]store.ServiceEntry{
		"deferred": {{Address: "10.0.2.9:8080", Capacity: 1, LastHeartbeat: time.Now()}},
	}
	storeStub.incrementErrByKey["deferred:10.0.2.9:8080"] = errors.New("increment failed")

	sr := NewStoreBackedRegistry(storeStub, 0)
	sr.SetCacheAdmissionMissThreshold(5)

	_, err := sr.GetService("deferred")
	if err == nil || err.Error() != "increment failed" {
		t.Fatalf("expected increment failure to propagate, got %v", err)
	}
	if len(storeStub.incrementCalls) != 1 {
		t.Fatalf("expected one increment attempt, got %d", len(storeStub.incrementCalls))
	}
	if _, ok := sr.currentMemCache().ListServices()["deferred"]; ok {
		t.Fatalf("task should remain out of cache when admission threshold is not reached")
	}
}
