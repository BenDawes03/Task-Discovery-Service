package store

import (
	"context"
	"time"
)

// ServiceEntry represents a service instance in the store.
type ServiceEntry struct {
	Address       string
	LastHeartbeat time.Time
	QueryCount    int64
	Capacity      int
}

// Store defines the persistence interface for the registry.
type Store interface {
	// Register adds or updates a service entry in the store.
	Register(ctx context.Context, task string, entry *ServiceEntry) error

	// IncrementQueryCount increments query_count for the given active service entry.
	IncrementQueryCount(ctx context.Context, task, address string) error

	// GetService retrieves a single service entry by task, applying round-robin selection
	// and incrementing the query count.
	GetService(ctx context.Context, task string) (*ServiceEntry, error)

	// ListServices returns a deep copy of all services grouped by task.
	ListServices(ctx context.Context) (map[string][]ServiceEntry, error)

	// ListTaskServices returns all active services for a single task.
	ListTaskServices(ctx context.Context, task string) ([]ServiceEntry, error)

	// ListTopTasksByQueryCount returns up to limit task names ordered by descending
	// combined query_count across active services.
	ListTopTasksByQueryCount(ctx context.Context, limit int) ([]string, error)

	// ListServicesForTasks returns active services for the provided task names.
	ListServicesForTasks(ctx context.Context, tasks []string) (map[string][]ServiceEntry, error)

	// Cleanup removes entries whose LastHeartbeat is older than the given timeout.
	// Returns the number of entries removed.
	Cleanup(ctx context.Context, timeout time.Duration) (int64, error)

	// Migrate runs database migrations (idempotent).
	Migrate(ctx context.Context) error

	// Close closes the store connection.
	Close() error
}
