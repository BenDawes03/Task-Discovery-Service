package firewall

import (
	"database/sql"
	"fmt"
	"net"
)

// DatabaseRulesProvider loads firewall rules from a SQL database.
// It supports querying and caching rules from a persistent store.
type DatabaseRulesProvider struct {
	db *sql.DB
	// Table schema assumed:
	//   CREATE TABLE firewall_rules (
	//     id SERIAL PRIMARY KEY,
	//     source_ip TEXT NOT NULL,
	//     source_network TEXT,
	//     dest_ip TEXT NOT NULL,
	//     dest_network TEXT,
	//     created_at TIMESTAMP DEFAULT NOW()
	//   );
}

// NewDatabaseRulesProvider creates a new DatabaseRulesProvider.
// The provider assumes the database has a firewall_rules table already set up.
func NewDatabaseRulesProvider(db *sql.DB) (*DatabaseRulesProvider, error) {
	drp := &DatabaseRulesProvider{
		db: db,
	}

	// Verify the database connection
	if err := drp.db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return drp, nil
}

// GetRules queries the database and returns all firewall rules.
func (drp *DatabaseRulesProvider) GetRules() ([]FirewallRule, error) {
	query := `
		SELECT source_ip, source_network, dest_ip, dest_network
		FROM firewall_rules
		ORDER BY id
	`

	rows, err := drp.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query firewall rules: %w", err)
	}
	defer rows.Close()

	var rules []FirewallRule
	for rows.Next() {
		var sourceIPStr, destIPStr, sourceNetStr, destNetStr sql.NullString

		if err := rows.Scan(&sourceIPStr, &sourceNetStr, &destIPStr, &destNetStr); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		var rule FirewallRule

		// Parse source
		if sourceNetStr.Valid {
			_, network, err := net.ParseCIDR(sourceNetStr.String)
			if err != nil {
				return nil, fmt.Errorf("invalid source CIDR in database: %w", err)
			}
			rule.SourceNetwork = network
		} else if sourceIPStr.Valid {
			ip := net.ParseIP(sourceIPStr.String)
			if ip == nil {
				return nil, fmt.Errorf("invalid source IP in database: %s", sourceIPStr.String)
			}
			rule.SourceIP = ip
		}

		// Parse destination
		if destNetStr.Valid {
			_, network, err := net.ParseCIDR(destNetStr.String)
			if err != nil {
				return nil, fmt.Errorf("invalid destination CIDR in database: %w", err)
			}
			rule.DestNetwork = network
		} else if destIPStr.Valid {
			ip := net.ParseIP(destIPStr.String)
			if ip == nil {
				return nil, fmt.Errorf("invalid destination IP in database: %s", destIPStr.String)
			}
			rule.DestIP = ip
		}

		rules = append(rules, rule)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return rules, nil
}

// Close closes the database connection.
func (drp *DatabaseRulesProvider) Close() error {
	return drp.db.Close()
}

// CreateTablesIfNotExist creates the firewall_rules table if it doesn't exist.
// Call this once during initialization.
func (drp *DatabaseRulesProvider) CreateTablesIfNotExist() error {
	schema := `
		CREATE TABLE IF NOT EXISTS firewall_rules (
			id SERIAL PRIMARY KEY,
			source_ip TEXT,
			source_network TEXT,
			dest_ip TEXT,
			dest_network TEXT,
			created_at TIMESTAMP DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_firewall_rules_source ON firewall_rules(source_ip, source_network);
	`

	_, err := drp.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("failed to create tables: %w", err)
	}

	return nil
}
