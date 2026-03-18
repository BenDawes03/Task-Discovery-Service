package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strconv"
	"strings"
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
	host, port, err := splitAddress(entry.Address)
	if err != nil {
		return fmt.Errorf("invalid service address %q: %w", entry.Address, err)
	}
	storageHost, err := resolveHostForInet(host)
	if err != nil {
		return fmt.Errorf("invalid service host %q: %w", host, err)
	}
	canonicalAddress := net.JoinHostPort(host, strconv.Itoa(port))

	var query string
	var args []any

	if entry.QueryCount > 0 {
		// Sync query count as well (used when syncing from cache).
		query = `
			INSERT INTO services (task, address, host, port, last_heartbeat, query_count, capacity, is_active, created_at, updated_at)
			VALUES ($1, $2, $3::inet, $4, $5, $6, $7, TRUE, NOW(), NOW())
			ON CONFLICT (task, host, port) DO UPDATE
			SET last_heartbeat = EXCLUDED.last_heartbeat,
			    address = EXCLUDED.address,
			    query_count = EXCLUDED.query_count,
			    capacity = EXCLUDED.capacity,
			    is_active = TRUE,
			    updated_at = NOW()
		`
		args = []any{task, canonicalAddress, storageHost, port, entry.LastHeartbeat, entry.QueryCount, normalizedCapacity(entry.Capacity)}
	} else {
		// Normal registration: reset query count when reactivating an inactive record.
		query = `
			INSERT INTO services (task, address, host, port, last_heartbeat, query_count, capacity, is_active, created_at, updated_at)
			VALUES ($1, $2, $3::inet, $4, $5, 0, $6, TRUE, NOW(), NOW())
			ON CONFLICT (task, host, port) DO UPDATE
			SET last_heartbeat = EXCLUDED.last_heartbeat,
			    address = EXCLUDED.address,
			    query_count = CASE WHEN services.is_active = FALSE THEN 0 ELSE services.query_count END,
			    capacity = EXCLUDED.capacity,
			    is_active = TRUE,
			    updated_at = NOW()
		`
		args = []any{task, canonicalAddress, storageHost, port, entry.LastHeartbeat, normalizedCapacity(entry.Capacity)}
	}

	_, err = ps.db.ExecContext(ctx, query, args...)
	return err
}

// IncrementQueryCount increments query_count for a specific active service entry.
func (ps *PostgresStore) IncrementQueryCount(ctx context.Context, task, address string) error {
	query := `
		UPDATE services
		SET query_count = query_count + 1, updated_at = NOW()
		WHERE task = $1 AND address = $2 AND is_active = TRUE
	`
	result, err := ps.db.ExecContext(ctx, query, task, address)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}

	return nil
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

	// Return the entry with updated query count.
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

// ListTaskServices returns active services for a specific task.
func (ps *PostgresStore) ListTaskServices(ctx context.Context, task string) ([]store.ServiceEntry, error) {
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

	entries := make([]store.ServiceEntry, 0)
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

	return entries, nil
}

// ListTopTasksByQueryCount returns up to limit tasks ordered by descending
// combined query_count across active services.
func (ps *PostgresStore) ListTopTasksByQueryCount(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		return []string{}, nil
	}

	query := `
		SELECT task
		FROM services
		WHERE is_active = TRUE
		GROUP BY task
		ORDER BY SUM(query_count) DESC, task ASC
		LIMIT $1
	`
	rows, err := ps.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tasks := make([]string, 0, limit)
	for rows.Next() {
		var task string
		if err := rows.Scan(&task); err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return tasks, nil
}

// ListServicesForTasks returns active services for the provided task names.
func (ps *PostgresStore) ListServicesForTasks(ctx context.Context, tasks []string) (map[string][]store.ServiceEntry, error) {
	result := make(map[string][]store.ServiceEntry)
	if len(tasks) == 0 {
		return result, nil
	}

	placeholders := make([]string, 0, len(tasks))
	args := make([]any, 0, len(tasks))
	for i, task := range tasks {
		placeholders = append(placeholders, fmt.Sprintf("$%d", i+1))
		args = append(args, task)
	}

	query := fmt.Sprintf(`
		SELECT task, address, last_heartbeat, query_count, capacity
		FROM services
		WHERE is_active = TRUE
		  AND task IN (%s)
		ORDER BY task, address ASC
	`, strings.Join(placeholders, ","))

	rows, err := ps.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

	if err := ps.pruneRoundRobinIndex(ctx); err != nil {
		return 0, err
	}

	count, err := result.RowsAffected()
	return count, err
}

func (ps *PostgresStore) pruneRoundRobinIndex(ctx context.Context) error {
	rows, err := ps.db.QueryContext(ctx, `SELECT DISTINCT task FROM services WHERE is_active = TRUE`)
	if err != nil {
		return err
	}
	defer rows.Close()

	activeTasks := make(map[string]struct{})
	for rows.Next() {
		var task string
		if err := rows.Scan(&task); err != nil {
			return err
		}
		activeTasks[task] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	ps.roundRobinIndexMu.Lock()
	for task := range ps.roundRobinIndex {
		if _, ok := activeTasks[task]; !ok {
			delete(ps.roundRobinIndex, task)
		}
	}
	ps.roundRobinIndexMu.Unlock()

	return nil
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
			host INET,
			port INTEGER,
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
		`ALTER TABLE services ADD COLUMN IF NOT EXISTS host INET;`,
		`ALTER TABLE services ADD COLUMN IF NOT EXISTS port INTEGER;`,

		// Backfill typed columns from legacy address text where possible.
		`UPDATE services SET host = split_part(address, ':', 1)::inet WHERE host IS NULL AND address LIKE '%:%' AND address NOT LIKE '[%';`,
		`UPDATE services SET port = split_part(address, ':', 2)::INTEGER WHERE port IS NULL AND address LIKE '%:%' AND address NOT LIKE '[%';`,
		`UPDATE services SET host = substring(address from '^\\[(.*)\\]:')::inet WHERE host IS NULL AND address LIKE '[%]:%';`,
		`UPDATE services SET port = substring(address from '\\]:(\\d+)$')::INTEGER WHERE port IS NULL AND address LIKE '[%]:%';`,
		`DELETE FROM services WHERE host IS NULL OR port IS NULL;`,
		`ALTER TABLE services ALTER COLUMN host SET NOT NULL;`,
		`ALTER TABLE services ALTER COLUMN port SET NOT NULL;`,
		`ALTER TABLE services DROP CONSTRAINT IF EXISTS chk_services_port_range;`,
		`ALTER TABLE services ADD CONSTRAINT chk_services_port_range CHECK (port BETWEEN 1 AND 65535);`,

		// Required for ON CONFLICT (task, address)
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_services_task_address_unique ON services(task, address);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_services_task_host_port_unique ON services(task, host, port);`,

		// Indexes
		`CREATE INDEX IF NOT EXISTS idx_services_task ON services(task);`,
		`CREATE INDEX IF NOT EXISTS idx_services_task_host_port ON services(task, host, port);`,
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

func splitAddress(address string) (string, int, error) {
	trimmed := strings.TrimSpace(address)
	host, portStr, err := net.SplitHostPort(trimmed)
	if err != nil {
		if strings.Count(trimmed, ":") == 1 {
			parts := strings.SplitN(trimmed, ":", 2)
			host = parts[0]
			portStr = parts[1]
		} else {
			return "", 0, err
		}
	}

	if host == "" {
		return "", 0, fmt.Errorf("missing host")
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port: %w", err)
	}
	if port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("port out of range: %d", port)
	}

	return host, port, nil
}

func resolveHostForInet(host string) (string, error) {
	trimmed := strings.TrimSpace(host)
	if idx := strings.Index(trimmed, "%"); idx != -1 {
		trimmed = trimmed[:idx]
	}

	if ip := net.ParseIP(trimmed); ip != nil {
		return ip.String(), nil
	}

	ips, err := net.LookupIP(trimmed)
	if err != nil {
		return "", err
	}
	if len(ips) == 0 {
		return "", fmt.Errorf("no IPs resolved")
	}

	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			return v4.String(), nil
		}
	}

	return ips[0].String(), nil
}
