package registry

import (
	"errors"
	"time"
)

var ErrNotFound = errors.New("service not found")

type ServiceEntry struct {
	Address       string
	LastHeartbeat time.Time
	QueryCount    int64
}

type Stats struct {
	TotalQueries int64
	TotalTasks   int
}

type Registry interface {
	Register(taskName, address string)
	GetService(taskName string) (string, error)
	Cleanup(timeout time.Duration) int
	ListServices() map[string][]ServiceEntry
	GetStats() Stats
}
