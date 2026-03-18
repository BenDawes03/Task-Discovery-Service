package registry

import (
	"errors"
	"net"
	"time"
)

var ErrNotFound = errors.New("service not found")
var ErrNoAllowedService = errors.New("no service allowed by firewall")
var ErrInvalidTaskName = errors.New("invalid task name")
var ErrInvalidAddress = errors.New("invalid service address")

type ServiceEntry struct {
	Address       string
	ParsedIP      net.IP
	LastHeartbeat time.Time
	QueryCount    int64
	Capacity      int
}

type Stats struct {
	TotalQueries int64
	TotalTasks   int
}

type Registry interface {
	Register(taskName, address string)
	// GetService returns a service address for the given task.
	GetService(taskName string) (string, error)
	// GetServiceForRequestor returns a service address that the requestor is allowed to reach,
	// filtered by firewall rules when requestorIP is non-nil.
	GetServiceForRequestor(taskName string, requestorIP net.IP) (string, error)
	Cleanup(timeout time.Duration) int
	ListServices() map[string][]ServiceEntry
	GetStats() Stats
}
