package firewall

import (
	"fmt"
)

// FileRulesProvider implements RulesProvider for a static rules file (for test compatibility only).
type FileRulesProvider struct {
	path  string
	rules []FirewallRule
}

// NewFileRulesProvider loads rules from a file and returns a FileRulesProvider.
func NewFileRulesProvider(path string) (*FileRulesProvider, error) {
	fw, err := LoadFromFile(path)
	if err != nil {
		return nil, err
	}
	return &FileRulesProvider{path: path, rules: fw.rules}, nil
}

// GetRules returns a copy of the loaded rules.
func (f *FileRulesProvider) GetRules() ([]FirewallRule, error) {
	copyRules := make([]FirewallRule, len(f.rules))
	copy(copyRules, f.rules)
	return copyRules, nil
}

// reload reloads the rules from the file (for tests that mutate the file).
func (f *FileRulesProvider) reload() error {
	fw, err := LoadFromFile(f.path)
	if err != nil {
		return fmt.Errorf("reload failed: %w", err)
	}
	f.rules = fw.rules
	return nil
}

// Close is a no-op for FileRulesProvider.
func (f *FileRulesProvider) Close() error {
	return nil
}

