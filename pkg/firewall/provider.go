package firewall

// RulesProvider is an abstraction for where firewall rules come from.
// Implementations can load from files, databases, or other sources.
type RulesProvider interface {
	// GetRules returns the current set of firewall rules.
	GetRules() ([]FirewallRule, error)

	// Close releases any resources held by the provider (e.g., DB connections).
	// It's safe to call Close multiple times.
	Close() error
}
