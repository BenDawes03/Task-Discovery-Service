package firewall

import (
	"database/sql"
	"fmt"
	"net"
)

// DatabaseRulesProvider loads firewall rules from a SQL database.
type DatabaseRulesProvider struct {
	db *sql.DB
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
		SELECT
			COALESCE(source_net::text, source_network, source_ip) AS source_rule,
			COALESCE(dest_net::text, dest_network, dest_ip) AS dest_rule
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
		var sourceRuleStr, destRuleStr sql.NullString

		if err := rows.Scan(&sourceRuleStr, &destRuleStr); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		if !sourceRuleStr.Valid || !destRuleStr.Valid {
			return nil, fmt.Errorf("invalid firewall rule row: source_rule valid=%t dest_rule valid=%t", sourceRuleStr.Valid, destRuleStr.Valid)
		}

		rule, err := parseRule(sourceRuleStr.String, destRuleStr.String)
		if err != nil {
			return nil, fmt.Errorf("invalid firewall rule in database (%q -> %q): %w", sourceRuleStr.String, destRuleStr.String, err)
		}

		rules = append(rules, rule)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	// No caching: always return fresh rules from DB
	return cloneRules(rules), nil
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
			source_net CIDR,
			dest_net CIDR,
			created_at TIMESTAMP DEFAULT NOW()
		);
		ALTER TABLE firewall_rules ADD COLUMN IF NOT EXISTS source_net CIDR;
		ALTER TABLE firewall_rules ADD COLUMN IF NOT EXISTS dest_net CIDR;
		UPDATE firewall_rules
		SET source_net = COALESCE(source_net, source_network::cidr, (source_ip || CASE WHEN source_ip LIKE '%:%' THEN '/128' ELSE '/32' END)::cidr)
		WHERE source_net IS NULL;
		UPDATE firewall_rules
		SET dest_net = COALESCE(dest_net, dest_network::cidr, (dest_ip || CASE WHEN dest_ip LIKE '%:%' THEN '/128' ELSE '/32' END)::cidr)
		WHERE dest_net IS NULL;
		CREATE INDEX IF NOT EXISTS idx_firewall_rules_source ON firewall_rules(source_ip, source_network);
		CREATE INDEX IF NOT EXISTS idx_firewall_rules_destination ON firewall_rules(dest_ip, dest_network);
		CREATE INDEX IF NOT EXISTS idx_firewall_rules_source_net ON firewall_rules(source_net);
		CREATE INDEX IF NOT EXISTS idx_firewall_rules_dest_net ON firewall_rules(dest_net);
	`

	_, err := drp.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("failed to create tables: %w", err)
	}

	return nil
}

// ReplaceRulesFromFile loads firewall rules from a file and replaces all rules in the database.
// This is intended for startup seeding when a rules file is provided.
func (drp *DatabaseRulesProvider) ReplaceRulesFromFile(filePath string) (int, error) {
	fw, err := LoadFromFile(filePath)
	if err != nil {
		return 0, fmt.Errorf("failed to load firewall rules file: %w", err)
	}

	tx, err := drp.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to start firewall import transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.Exec(`DELETE FROM firewall_rules`); err != nil {
		return 0, fmt.Errorf("failed to clear existing firewall rules: %w", err)
	}

	insertQuery := `
		INSERT INTO firewall_rules (source_ip, source_network, dest_ip, dest_network, source_net, dest_net)
		VALUES ($1, $2, $3, $4, $5::cidr, $6::cidr)
	`

	for _, rule := range fw.rules {
		sourceIP, sourceNetwork, sourceNet, err := ruleSourceValues(rule)
		if err != nil {
			return 0, err
		}
		destIP, destNetwork, destNet, err := ruleDestValues(rule)
		if err != nil {
			return 0, err
		}

		if _, err := tx.Exec(insertQuery, sourceIP, sourceNetwork, destIP, destNetwork, sourceNet, destNet); err != nil {
			return 0, fmt.Errorf("failed to insert firewall rule (%q -> %q): %w", sourceNet, destNet, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit firewall rule import: %w", err)
	}

	// No caching: rules are always loaded fresh from DB

	return len(fw.rules), nil
}

func ruleSourceValues(rule FirewallRule) (sourceIP any, sourceNetwork any, sourceNet string, err error) {
	if rule.SourceNetwork != nil {
		sourceNetwork = rule.SourceNetwork.String()
		sourceNet = sourceNetwork.(string)
		return sourceIP, sourceNetwork, sourceNet, nil
	}
	if rule.SourceIP == nil {
		return nil, nil, "", fmt.Errorf("source rule is empty")
	}
	sourceIPStr := canonicalIPString(rule.SourceIP)
	if sourceIPStr == "" {
		return nil, nil, "", fmt.Errorf("invalid source IP")
	}
	if rule.SourceIP.To4() != nil {
		sourceNet = sourceIPStr + "/32"
	} else {
		sourceNet = sourceIPStr + "/128"
	}
	return sourceIPStr, sourceNetwork, sourceNet, nil
}

func ruleDestValues(rule FirewallRule) (destIP any, destNetwork any, destNet string, err error) {
	if rule.DestNetwork != nil {
		destNetwork = rule.DestNetwork.String()
		destNet = destNetwork.(string)
		return destIP, destNetwork, destNet, nil
	}
	if rule.DestIP == nil {
		return nil, nil, "", fmt.Errorf("destination rule is empty")
	}
	destIPStr := canonicalIPString(rule.DestIP)
	if destIPStr == "" {
		return nil, nil, "", fmt.Errorf("invalid destination IP")
	}
	if rule.DestIP.To4() != nil {
		destNet = destIPStr + "/32"
	} else {
		destNet = destIPStr + "/128"
	}
	return destIPStr, destNetwork, destNet, nil
}

func cloneRules(rules []FirewallRule) []FirewallRule {
	out := make([]FirewallRule, len(rules))
	for i, rule := range rules {
		out[i] = FirewallRule{
			SourceIP:      cloneIP(rule.SourceIP),
			SourceNetwork: cloneIPNet(rule.SourceNetwork),
			DestIP:        cloneIP(rule.DestIP),
			DestNetwork:   cloneIPNet(rule.DestNetwork),
		}
	}
	return out
}

func cloneIP(ip net.IP) net.IP {
	if ip == nil {
		return nil
	}
	dup := make(net.IP, len(ip))
	copy(dup, ip)
	return dup
}

func cloneIPNet(ipNet *net.IPNet) *net.IPNet {
	if ipNet == nil {
		return nil
	}
	ipCopy := cloneIP(ipNet.IP)
	maskCopy := make(net.IPMask, len(ipNet.Mask))
	copy(maskCopy, ipNet.Mask)
	return &net.IPNet{IP: ipCopy, Mask: maskCopy}
}
