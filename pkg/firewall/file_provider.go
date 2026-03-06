package firewall

import (
	"sync"
)

// FileRulesProvider loads firewall rules from a file.
// Rules are loaded once at initialization and remain static.
type FileRulesProvider struct {
	filePath string
	mu       sync.RWMutex
	rules    []FirewallRule
}

// NewFileRulesProvider creates a new FileRulesProvider and loads rules from the file.
func NewFileRulesProvider(filePath string) (*FileRulesProvider, error) {
	frp := &FileRulesProvider{
		filePath: filePath,
	}

	// Load rules immediately from file
	if err := frp.reload(); err != nil {
		return nil, err
	}

	return frp, nil
}

// GetRules returns a copy of the currently loaded rules.
func (frp *FileRulesProvider) GetRules() ([]FirewallRule, error) {
	frp.mu.RLock()
	defer frp.mu.RUnlock()

	// Return a copy to prevent external modification
	rules := make([]FirewallRule, len(frp.rules))
	copy(rules, frp.rules)
	return rules, nil
}

// reload loads rules from disk (internal, not thread-safe on its own).
func (frp *FileRulesProvider) reload() error {
	fw, err := LoadFromFile(frp.filePath)
	if err != nil {
		return err
	}

	frp.mu.Lock()
	defer frp.mu.Unlock()

	frp.rules = fw.rules
	return nil
}

// Close releases any resources held by the provider.
func (frp *FileRulesProvider) Close() error {
	return nil
}
