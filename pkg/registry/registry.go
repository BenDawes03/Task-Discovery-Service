package registry

import (
	"errors"
	"net"
	"time"
)

var ErrNotFound = errors.New("service not found")
var ErrNoAllowedService = errors.New("no service allowed by firewall")

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
	// GetService returns a service address for the given task.
	// If requestorIP is provided, the result is filtered based on firewall rules.
	GetService(taskName string) (string, error)
	// GetServiceForRequestor returns a service address that the requestor is allowed to reach.
	GetServiceForRequestor(taskName string, requestorIP net.IP) (string, error)
	Cleanup(timeout time.Duration) int
	ListServices() map[string][]ServiceEntry
	GetStats() Stats
}
