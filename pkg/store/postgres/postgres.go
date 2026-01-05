package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "github.com/lib/pq"

	"tds/pkg/store"
)

// PostgresStore implements store.Store using PostgreSQL.
type PostgresStore struct {
	db                *sql.DB
	roundRobinIndex   map[string]int
	roundRobinIndexMu sync.RWMutex
}

// NewPostgresStore creates a new PostgreSQL-backed store.
// dsn should be a valid PostgreSQL connection string (e.g., "postgres://user:password@localhost:5432/dbname").
func NewPostgresStore(dsn string) (*PostgresStore, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open postgres connection: %w", err)
	}

	// Test the connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping postgres: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	return &PostgresStore{
		db:              db,
		roundRobinIndex: make(map[string]int),
	}, nil
}

// Register adds or updates a service entry.
func (ps *PostgresStore) Register(ctx context.Context, task string, entry *store.ServiceEntry) error {
	query := `
		INSERT INTO services (task, address, last_heartbeat, query_count, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		ON CONFLICT (task, address) DO UPDATE
		SET last_heartbeat = EXCLUDED.last_heartbeat, updated_at = NOW()
	`
	_, err := ps.db.ExecContext(ctx, query, task, entry.Address, entry.LastHeartbeat, entry.QueryCount)
	return err
}

// GetService retrieves a single service by task using round-robin selection and increments query count.
func (ps *PostgresStore) GetService(ctx context.Context, task string) (*store.ServiceEntry, error) {
	// Retrieve all entries for the task
	query := `
		SELECT address, last_heartbeat, query_count
		FROM services
		WHERE task = $1
		ORDER BY address ASC
	`
	rows, err := ps.db.QueryContext(ctx, query, task)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []store.ServiceEntry
	for rows.Next() {
		var e store.ServiceEntry
		if err := rows.Scan(&e.Address, &e.LastHeartbeat, &e.QueryCount); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(entries) == 0 {
		return nil, ErrNotFound
	}

	// Round-robin selection
	ps.roundRobinIndexMu.Lock()
	idx := ps.roundRobinIndex[task] % len(entries)
	ps.roundRobinIndex[task] = (idx + 1) % len(entries)
	ps.roundRobinIndexMu.Unlock()

	selected := &entries[idx]

	// Increment query count in database
	updateQuery := `
		UPDATE services
		SET query_count = query_count + 1, updated_at = NOW()
		WHERE task = $1 AND address = $2
	`
	_, err = ps.db.ExecContext(ctx, updateQuery, task, selected.Address)
	if err != nil {
		return nil, err
	}

	// Fetch updated entry to return correct query count
	selected.QueryCount++
	return selected, nil
}

// ListServices returns all services grouped by task.
func (ps *PostgresStore) ListServices(ctx context.Context) (map[string][]store.ServiceEntry, error) {
	query := `
		SELECT task, address, last_heartbeat, query_count
		FROM services
		ORDER BY task, address ASC
	`
	rows, err := ps.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string][]store.ServiceEntry)
	for rows.Next() {
		var task string
		var e store.ServiceEntry
		if err := rows.Scan(&task, &e.Address, &e.LastHeartbeat, &e.QueryCount); err != nil {
			return nil, err
		}
		result[task] = append(result[task], e)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

// Cleanup removes entries whose LastHeartbeat is older than the timeout.
func (ps *PostgresStore) Cleanup(ctx context.Context, timeout time.Duration) (int64, error) {
	cutoff := time.Now().Add(-timeout)
	result, err := ps.db.ExecContext(ctx, "DELETE FROM services WHERE last_heartbeat < $1", cutoff)
	if err != nil {
		return 0, err
	}

	count, err := result.RowsAffected()
	return count, err
}

// Migrate runs database migrations (reads and executes 0001_init.sql).
func (ps *PostgresStore) Migrate(ctx context.Context) error {
	// For now, we rely on the schema in 0001_init.sql being idempotent (IF NOT EXISTS).
	// In a production system, use a migration library like migrate or flyway.
	query := `
		CREATE TABLE IF NOT EXISTS services (
			id SERIAL PRIMARY KEY,
			task TEXT NOT NULL,
			address TEXT NOT NULL,
			last_heartbeat TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			query_count BIGINT NOT NULL DEFAULT 0,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			UNIQUE(task, address)
		);

		CREATE INDEX IF NOT EXISTS idx_services_task ON services(task);
		CREATE INDEX IF NOT EXISTS idx_services_last_heartbeat ON services(last_heartbeat);
	`
	_, err := ps.db.ExecContext(ctx, query)
	return err
}

// Close closes the database connection.
func (ps *PostgresStore) Close() error {
	return ps.db.Close()
}

// Ensure PostgresStore implements store.Store.
var _ store.Store = (*PostgresStore)(nil)
