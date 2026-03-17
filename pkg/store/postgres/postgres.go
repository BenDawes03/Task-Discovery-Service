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
// If entry.QueryCount > 0, it will update the query count as well (for syncing from cache).
func (ps *PostgresStore) Register(ctx context.Context, task string, entry *store.ServiceEntry) error {
	var query string
	var args []any

	if entry.QueryCount > 0 {
		// Sync query count as well (used when syncing from cache).
		query = `
			INSERT INTO services (task, address, last_heartbeat, query_count, capacity, is_active, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, TRUE, NOW(), NOW())
			ON CONFLICT (task, address) DO UPDATE
			SET last_heartbeat = EXCLUDED.last_heartbeat,
			    query_count = EXCLUDED.query_count,
			    capacity = EXCLUDED.capacity,
			    is_active = TRUE,
			    updated_at = NOW()
		`
		args = []any{task, entry.Address, entry.LastHeartbeat, entry.QueryCount, normalizedCapacity(entry.Capacity)}
	} else {
		// Normal registration: preserve existing query count if record already exists.
		query = `
			INSERT INTO services (task, address, last_heartbeat, query_count, capacity, is_active, created_at, updated_at)
			VALUES ($1, $2, $3, 0, $4, TRUE, NOW(), NOW())
			ON CONFLICT (task, address) DO UPDATE
			SET last_heartbeat = EXCLUDED.last_heartbeat,
			    capacity = EXCLUDED.capacity,
			    is_active = TRUE,
			    updated_at = NOW()
		`
		args = []any{task, entry.Address, entry.LastHeartbeat, normalizedCapacity(entry.Capacity)}
	}

	_, err := ps.db.ExecContext(ctx, query, args...)
	return err
}

// GetService retrieves a single service by task using capacity-weighted round-robin
// selection and increments query count.
func (ps *PostgresStore) GetService(ctx context.Context, task string) (*store.ServiceEntry, error) {
	// Retrieve all active entries for the task
	query := `
		SELECT address, last_heartbeat, query_count, capacity
		FROM services
		WHERE task = $1 AND is_active = TRUE
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
		if err := rows.Scan(&e.Address, &e.LastHeartbeat, &e.QueryCount, &e.Capacity); err != nil {
			return nil, err
		}
		e.Capacity = normalizedCapacity(e.Capacity)
		entries = append(entries, e)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(entries) == 0 {
		return nil, ErrNotFound
	}

	totalWeight := totalStoreWeight(entries)
	if totalWeight <= 0 {
		return nil, ErrNotFound
	}

	// Capacity-weighted round-robin selection via cumulative weights.
	ps.roundRobinIndexMu.Lock()
	slot := ps.roundRobinIndex[task] % totalWeight
	ps.roundRobinIndex[task] = (slot + 1) % totalWeight
	ps.roundRobinIndexMu.Unlock()

	selectedIndex, ok := selectWeightedEntryIndex(entries, slot)
	if !ok {
		return nil, ErrNotFound
	}
	selected := &entries[selectedIndex]

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

// ListServices returns all active services grouped by task.
func (ps *PostgresStore) ListServices(ctx context.Context) (map[string][]store.ServiceEntry, error) {
	query := `
		SELECT task, address, last_heartbeat, query_count, capacity
		FROM services
		WHERE is_active = TRUE
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
		if err := rows.Scan(&task, &e.Address, &e.LastHeartbeat, &e.QueryCount, &e.Capacity); err != nil {
			return nil, err
		}
		e.Capacity = normalizedCapacity(e.Capacity)
		result[task] = append(result[task], e)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

// Cleanup marks entries as inactive whose LastHeartbeat is older than the timeout.
// Records are not deleted from the database to preserve them for logging/audit purposes.
func (ps *PostgresStore) Cleanup(ctx context.Context, timeout time.Duration) (int64, error) {
	cutoff := time.Now().Add(-timeout)
	query := `
		UPDATE services 
		SET is_active = FALSE, updated_at = NOW()
		WHERE last_heartbeat < $1 AND is_active = TRUE
	`
	result, err := ps.db.ExecContext(ctx, query, cutoff)
	if err != nil {
		return 0, err
	}

	count, err := result.RowsAffected()
	return count, err
}

// Migrate runs database migrations.
//
// It is designed to be idempotent, including when the `services` table already exists
// with an older schema missing newer columns such as `is_active`.
func (ps *PostgresStore) Migrate(ctx context.Context) error {
	// In a production system, use a migration library like migrate or flyway.
	// Here we keep it simple and explicitly make schema changes safe to re-run.
	tx, err := ps.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	statements := []string{
		// Base table (new installs)
		`
		CREATE TABLE IF NOT EXISTS services (
			id SERIAL PRIMARY KEY,
			task TEXT NOT NULL,
			address TEXT NOT NULL,
			last_heartbeat TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			query_count BIGINT NOT NULL DEFAULT 0,
			capacity INTEGER NOT NULL DEFAULT 1,
			is_active BOOLEAN NOT NULL DEFAULT TRUE,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		);
		`,

		// Backfill/upgrade path (existing installs)
		`ALTER TABLE services ADD COLUMN IF NOT EXISTS query_count BIGINT NOT NULL DEFAULT 0;`,
		`ALTER TABLE services ADD COLUMN IF NOT EXISTS capacity INTEGER NOT NULL DEFAULT 1;`,
		`ALTER TABLE services ADD COLUMN IF NOT EXISTS is_active BOOLEAN NOT NULL DEFAULT TRUE;`,
		`ALTER TABLE services ADD COLUMN IF NOT EXISTS created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW();`,
		`ALTER TABLE services ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW();`,

		// Required for ON CONFLICT (task, address)
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_services_task_address_unique ON services(task, address);`,

		// Indexes
		`CREATE INDEX IF NOT EXISTS idx_services_task ON services(task);`,
		`CREATE INDEX IF NOT EXISTS idx_services_last_heartbeat ON services(last_heartbeat);`,
		`CREATE INDEX IF NOT EXISTS idx_services_is_active ON services(is_active);`,
	}

	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// Close closes the database connection.
func (ps *PostgresStore) Close() error {
	return ps.db.Close()
}

// ListInactiveServices returns all inactive services for logging/audit purposes.
// These are services that have been marked inactive by cleanup but preserved in the database.
func (ps *PostgresStore) ListInactiveServices(ctx context.Context) (map[string][]store.ServiceEntry, error) {
	query := `
		SELECT task, address, last_heartbeat, query_count, capacity
		FROM services
		WHERE is_active = FALSE
		ORDER BY updated_at DESC, task, address ASC
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
		if err := rows.Scan(&task, &e.Address, &e.LastHeartbeat, &e.QueryCount, &e.Capacity); err != nil {
			return nil, err
		}
		e.Capacity = normalizedCapacity(e.Capacity)
		result[task] = append(result[task], e)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

// GetServiceHistory returns all services (active and inactive) for a specific task.
// Useful for debugging and historical analysis.
func (ps *PostgresStore) GetServiceHistory(ctx context.Context, task string) ([]store.ServiceEntry, error) {
	query := `
		SELECT address, last_heartbeat, query_count, capacity, is_active
		FROM services
		WHERE task = $1
		ORDER BY is_active DESC, last_heartbeat DESC
	`
	rows, err := ps.db.QueryContext(ctx, query, task)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []store.ServiceEntry
	for rows.Next() {
		var e store.ServiceEntry
		var isActive bool
		if err := rows.Scan(&e.Address, &e.LastHeartbeat, &e.QueryCount, &e.Capacity, &isActive); err != nil {
			return nil, err
		}
		e.Capacity = normalizedCapacity(e.Capacity)
		// You could add a field to ServiceEntry to track active status if needed for display
		entries = append(entries, e)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return entries, nil
}

// Ensure PostgresStore implements store.Store.
var _ store.Store = (*PostgresStore)(nil)

func normalizedCapacity(capacity int) int {
	if capacity <= 0 {
		return 1
	}
	return capacity
}

func totalStoreWeight(entries []store.ServiceEntry) int {
	total := 0
	for _, entry := range entries {
		total += storeServiceWeight(entry)
	}
	return total
}

func selectWeightedEntryIndex(entries []store.ServiceEntry, slot int) (int, bool) {
	running := 0
	for i, entry := range entries {
		running += storeServiceWeight(entry)
		if slot < running {
			return i, true
		}
	}
	return 0, false
}

func storeServiceWeight(entry store.ServiceEntry) int {
	return normalizedCapacity(entry.Capacity)
}
