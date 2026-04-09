package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"tds/pkg/store"

	"github.com/go-chi/chi/v5"
)

type Service struct {
	Store   store.Store
	metrics metricsHolder
}

type createRequest struct {
	Task     string `json:"task"`
	Address  string `json:"address"`
	Capacity int    `json:"capacity"`
}

func (s *Service) Create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.Task == "" || req.Address == "" {
		http.Error(w, "task and address required", http.StatusBadRequest)
		return
	}
	entry := &store.ServiceEntry{
		Address:       req.Address,
		LastHeartbeat: time.Now().UTC(),
		Capacity:      req.Capacity,
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.Store.Register(ctx, req.Task, entry); err != nil {
		http.Error(w, "failed to register service", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Service) List(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	services, err := s.Store.ListServices(ctx)
	if err != nil {
		http.Error(w, "failed to list services", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(services)
}

func (s *Service) GetById(w http.ResponseWriter, r *http.Request) {
	task := chi.URLParam(r, "task")
	if task == "" {
		http.Error(w, "task required", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	list, err := s.Store.ListTaskServices(ctx, task)
	if err != nil {
		http.Error(w, "failed to get services", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

func (s *Service) Query(w http.ResponseWriter, r *http.Request) {
	task := chi.URLParam(r, "task")
	if task == "" {
		http.Error(w, "task required", http.StatusBadRequest)
		return
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	svc, err := s.Store.GetService(ctx, task)
	elapsed := time.Since(start)

	// record metrics if available
	if ms, ok := s.getTaskMetrics(task); ok {
		ms.recordQuery(elapsed, err == nil)
	}

	if err != nil {
		http.Error(w, "no available service", http.StatusNotFound)
		return
	}
	resp := map[string]interface{}{"address": svc.Address}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// ---- metrics ---------------------------------

type TaskMetrics struct {
	mu                sync.Mutex
	Queries           int64 `json:"queries"`
	Successes         int64 `json:"successes"`
	TotalResponseMs   int64 `json:"total_response_ms"`
	CacheHits         int64 `json:"cache_hits"`
	CacheMisses       int64 `json:"cache_misses"`
}

func (m *TaskMetrics) recordQuery(d time.Duration, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Queries++
	if ok {
		m.Successes++
		m.CacheHits++
	} else {
		m.CacheMisses++
	}
	m.TotalResponseMs += d.Milliseconds()
}

func (m *TaskMetrics) AvgResponseMs() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Queries == 0 {
		return 0
	}
	return float64(m.TotalResponseMs) / float64(m.Queries)
}

// metrics map on Service
type metricsHolder struct {
	mu      sync.RWMutex
	metrics map[string]*TaskMetrics
}

func (h *metricsHolder) get(task string) (*TaskMetrics, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	m, ok := h.metrics[task]
	return m, ok
}

func (h *metricsHolder) ensure(task string) *TaskMetrics {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.metrics == nil {
		h.metrics = make(map[string]*TaskMetrics)
	}
	m, ok := h.metrics[task]
	if !ok {
		m = &TaskMetrics{}
		h.metrics[task] = m
	}
	return m
}

// provide accessor methods on Service via embedding a holder
func (s *Service) getTaskMetrics(task string) (*TaskMetrics, bool) {
	m := s.metrics.ensure(task)
	return m, true
}

func (s *Service) ensureTaskMetrics(task string) *TaskMetrics {
	return s.metrics.ensure(task)
}

// Metrics handler: return collected metrics snapshot
func (s *Service) Metrics(w http.ResponseWriter, r *http.Request) {
	// build snapshot
	s.metrics.mu.RLock()
	snapshot := make(map[string]map[string]interface{})
	for t, m := range s.metrics.metrics {
		snapshot[t] = map[string]interface{}{
			"queries":        m.Queries,
			"successes":      m.Successes,
			"avg_response_ms": m.AvgResponseMs(),
			"cache_hits":     m.CacheHits,
			"cache_misses":   m.CacheMisses,
		}
	}
	s.metrics.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snapshot)
}


