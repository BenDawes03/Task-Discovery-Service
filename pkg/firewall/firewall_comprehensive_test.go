package firewall

import (
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

type stubRulesProvider struct {
	rules    []FirewallRule
	getErr   error
	closeErr error
}

func (sp *stubRulesProvider) GetRules() ([]FirewallRule, error) {
	if sp.getErr != nil {
		return nil, sp.getErr
	}
	rules := make([]FirewallRule, len(sp.rules))
	copy(rules, sp.rules)
	return rules, nil
}

func (sp *stubRulesProvider) Close() error {
	return sp.closeErr
}

func writeRulesFile(t *testing.T, content string) string {
	t.Helper()

	file, err := os.CreateTemp("", "firewall_rules_*.txt")
	if err != nil {
		t.Fatalf("CreateTemp failed: %v", err)
	}

	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		t.Fatalf("WriteString failed: %v", err)
	}

	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		t.Fatalf("Close failed: %v", err)
	}

	t.Cleanup(func() {
		_ = os.Remove(file.Name())
	})

	return file.Name()
}

func mustRule(t *testing.T, source, dest string) FirewallRule {
	t.Helper()

	rule, err := parseRule(source, dest)
	if err != nil {
		t.Fatalf("parseRule(%q, %q) failed: %v", source, dest, err)
	}

	return rule
}

func TestLoadFromFile(t *testing.T) {
	t.Run("success with comments and blank lines", func(t *testing.T) {
		path := writeRulesFile(t, "\n# comment\n192.168.1.10 10.0.0.5\n\n192.168.1.0/24 10.0.0.0/24\n")

		fw, err := LoadFromFile(path)
		if err != nil {
			t.Fatalf("LoadFromFile failed: %v", err)
		}

		if fw.RuleCount() != 2 {
			t.Fatalf("expected 2 rules, got %d", fw.RuleCount())
		}
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := LoadFromFile("does-not-exist.rules")
		if err == nil || !strings.Contains(err.Error(), "failed to open firewall rules file") {
			t.Fatalf("expected open error, got %v", err)
		}
	})

	t.Run("invalid field count", func(t *testing.T) {
		path := writeRulesFile(t, "192.168.1.10 10.0.0.5 extra\n")

		_, err := LoadFromFile(path)
		if err == nil || !strings.Contains(err.Error(), "expected 2 fields") {
			t.Fatalf("expected field count error, got %v", err)
		}
	})

	t.Run("invalid source rule", func(t *testing.T) {
		path := writeRulesFile(t, "bad-ip 10.0.0.5\n")

		_, err := LoadFromFile(path)
		if err == nil || !strings.Contains(err.Error(), "invalid source IP") {
			t.Fatalf("expected invalid source error, got %v", err)
		}
	})

	t.Run("invalid destination rule", func(t *testing.T) {
		path := writeRulesFile(t, "192.168.1.10 bad-ip\n")

		_, err := LoadFromFile(path)
		if err == nil || !strings.Contains(err.Error(), "invalid destination IP") {
			t.Fatalf("expected invalid destination error, got %v", err)
		}
	})
	}

func TestParseRule(t *testing.T) {
	t.Run("parses exact IP rule", func(t *testing.T) {
		rule, err := parseRule("192.168.1.10", "10.0.0.5")
		if err != nil {
			t.Fatalf("parseRule failed: %v", err)
		}

		if !rule.SourceIP.Equal(net.ParseIP("192.168.1.10")) {
			t.Fatalf("unexpected source IP: %v", rule.SourceIP)
		}
		if !rule.DestIP.Equal(net.ParseIP("10.0.0.5")) {
			t.Fatalf("unexpected dest IP: %v", rule.DestIP)
		}
	})

	t.Run("parses CIDR rule", func(t *testing.T) {
		rule, err := parseRule("192.168.1.0/24", "2001:db8::/64")
		if err != nil {
			t.Fatalf("parseRule failed: %v", err)
		}

		if rule.SourceNetwork == nil || rule.SourceNetwork.String() != "192.168.1.0/24" {
			t.Fatalf("unexpected source network: %v", rule.SourceNetwork)
		}
		if rule.DestNetwork == nil || rule.DestNetwork.String() != "2001:db8::/64" {
			t.Fatalf("unexpected dest network: %v", rule.DestNetwork)
		}
	})

	t.Run("rejects invalid source CIDR", func(t *testing.T) {
		_, err := parseRule("192.168.1.0/99", "10.0.0.5")
		if err == nil || !strings.Contains(err.Error(), "invalid source CIDR") {
			t.Fatalf("expected invalid source CIDR error, got %v", err)
		}
	})

	t.Run("rejects invalid destination CIDR", func(t *testing.T) {
		_, err := parseRule("192.168.1.10", "10.0.0.0/99")
		if err == nil || !strings.Contains(err.Error(), "invalid destination CIDR") {
			t.Fatalf("expected invalid destination CIDR error, got %v", err)
		}
	})
	}

func TestFirewallIsAllowedAndHelpers(t *testing.T) {
	fw := &Firewall{rules: []FirewallRule{
		mustRule(t, "192.168.1.10", "10.0.0.5"),
		mustRule(t, "192.168.1.0/24", "10.0.0.0/24"),
		mustRule(t, "2001:db8::1", "2001:db8:1::/64"),
	}}

	if !fw.IsAllowed(net.ParseIP("192.168.1.10"), net.ParseIP("10.0.0.5")) {
		t.Fatal("expected exact IPv4 match to be allowed")
	}

	if !fw.IsAllowed(net.ParseIP("192.168.1.55"), net.ParseIP("10.0.0.50")) {
		t.Fatal("expected CIDR IPv4 match to be allowed")
	}

	if !fw.IsAllowed(net.ParseIP("2001:db8::1"), net.ParseIP("2001:db8:1::99")) {
		t.Fatal("expected IPv6 match to be allowed")
	}

	if fw.IsAllowed(net.ParseIP("172.16.0.1"), net.ParseIP("10.0.0.5")) {
		t.Fatal("unexpected allow for unmatched source")
	}

	if fw.IsAllowed(net.ParseIP("192.168.1.10"), net.ParseIP("172.16.0.1")) {
		t.Fatal("unexpected allow for unmatched destination")
	}

	if !(&Firewall{}).IsAllowed(net.ParseIP("1.1.1.1"), net.ParseIP("2.2.2.2")) {
		t.Fatal("empty firewall should be permissive")
	}

	rule := mustRule(t, "192.168.1.0/24", "10.0.0.5")
	if !fw.matchesSource(rule, net.ParseIP("192.168.1.9")) {
		t.Fatal("matchesSource should accept address inside CIDR")
	}
	if fw.matchesSource(rule, net.ParseIP("10.1.1.1")) {
		t.Fatal("matchesSource should reject address outside CIDR")
	}
	if !fw.matchesDest(rule, net.ParseIP("10.0.0.5")) {
		t.Fatal("matchesDest should accept exact destination")
	}
	if fw.matchesDest(rule, net.ParseIP("10.0.0.6")) {
		t.Fatal("matchesDest should reject non-matching destination")
	}
	if fw.RuleCount() != 3 {
		t.Fatalf("expected rule count 3, got %d", fw.RuleCount())
	}
}

func TestFirewallFilterAddresses(t *testing.T) {
	fw := &Firewall{rules: []FirewallRule{
		mustRule(t, "192.168.1.10", "10.0.0.5"),
		mustRule(t, "192.168.1.10", "2001:db8::5"),
	}}

	sourceIP := net.ParseIP("192.168.1.10")
	addresses := []string{
		"10.0.0.5:8080",
		"10.0.0.5",
		"2001:db8::5",
		"172.16.0.1:8080",
		"not-an-ip",
	}

	filtered := fw.FilterAddresses(sourceIP, addresses)
	if len(filtered) != 3 {
		t.Fatalf("expected 3 allowed addresses, got %d (%v)", len(filtered), filtered)
	}

	if filtered[0] != "10.0.0.5:8080" || filtered[1] != "10.0.0.5" || filtered[2] != "2001:db8::5" {
		t.Fatalf("unexpected filtered results: %v", filtered)
	}

	input := []string{"bad", "still-bad"}
	if out := (&Firewall{}).FilterAddresses(sourceIP, input); len(out) != 2 || &out[0] != &input[0] {
		t.Fatal("permissive filter should return original addresses slice")
	}
}

func TestFileRulesProviderBehaviors(t *testing.T) {
	path := writeRulesFile(t, "192.168.1.10 10.0.0.5\n")

	provider, err := NewFileRulesProvider(path)
	if err != nil {
		t.Fatalf("NewFileRulesProvider failed: %v", err)
	}
	defer func() { _ = provider.Close() }()

	rules, err := provider.GetRules()
	if err != nil {
		t.Fatalf("GetRules failed: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}

	rules[0].SourceIP = net.ParseIP("10.10.10.10")
	reloadedRules, err := provider.GetRules()
	if err != nil {
		t.Fatalf("GetRules second call failed: %v", err)
	}
	if !reloadedRules[0].SourceIP.Equal(net.ParseIP("192.168.1.10")) {
		t.Fatal("GetRules should return a copy, not internal slice")
	}

	if err := os.WriteFile(path, []byte("192.168.1.20 10.0.0.6\n"), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := provider.reload(); err != nil {
		t.Fatalf("reload failed: %v", err)
	}

	rules, err = provider.GetRules()
	if err != nil {
		t.Fatalf("GetRules after reload failed: %v", err)
	}
	if !rules[0].SourceIP.Equal(net.ParseIP("192.168.1.20")) {
		t.Fatalf("expected reloaded source IP, got %v", rules[0].SourceIP)
	}

	if err := provider.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if _, err := NewFileRulesProvider("missing.rules"); err == nil {
		t.Fatal("expected NewFileRulesProvider to fail for missing file")
	}
}

func TestIPToNetworkAndSourceMatches(t *testing.T) {
	v4 := ipToNetwork(net.ParseIP("10.0.0.5"))
	if ones, bits := v4.Mask.Size(); ones != 32 || bits != 32 {
		t.Fatalf("expected /32 for IPv4, got /%d of %d", ones, bits)
	}

	v6 := ipToNetwork(net.ParseIP("2001:db8::5"))
	if ones, bits := v6.Mask.Size(); ones != 128 || bits != 128 {
		t.Fatalf("expected /128 for IPv6, got /%d of %d", ones, bits)
	}

	ifw := &IndexedFirewall{index: map[string][]*net.IPNet{}}
	if ifw.sourceMatches("not-a-cidr", net.ParseIP("10.0.0.1")) {
		t.Fatal("sourceMatches should reject invalid CIDR key")
	}
}

func TestIndexedFirewallBehaviors(t *testing.T) {
	provider := &stubRulesProvider{rules: []FirewallRule{
		mustRule(t, "192.168.1.10", "10.0.0.5"),
	}}

	ifw, err := NewIndexedFirewall(provider)
	if err != nil {
		t.Fatalf("NewIndexedFirewall failed: %v", err)
	}

	if !ifw.IsAllowed(net.ParseIP("192.168.1.10"), net.ParseIP("10.0.0.5")) {
		t.Fatal("expected indexed firewall allow")
	}
	if ifw.IsAllowed(net.ParseIP("192.168.1.10"), net.ParseIP("10.0.0.6")) {
		t.Fatal("unexpected indexed firewall allow")
	}

	stats := ifw.GetStats()
	if stats.TotalChecks != 2 || stats.TotalAllowed != 1 || stats.TotalBlocked != 1 {
		t.Fatalf("unexpected stats after checks: %+v", stats)
	}

	provider.rules = []FirewallRule{mustRule(t, "192.168.1.10", "10.0.0.6")}
	beforeReload := time.Now().Unix()
	if err := ifw.Reload(); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}
	if ifw.GetStats().LastReloadTime < beforeReload {
		t.Fatalf("expected reload time to be updated, got %d", ifw.GetStats().LastReloadTime)
	}
	if !ifw.IsAllowed(net.ParseIP("192.168.1.10"), net.ParseIP("10.0.0.6")) {
		t.Fatal("expected reloaded rule to allow new destination")
	}

	if provider.closeErr == nil {
		provider.closeErr = errors.New("close failure")
	}
	if err := ifw.Close(); err == nil || err.Error() != "close failure" {
		t.Fatalf("expected provider close error, got %v", err)
	}

	addresses := []string{"10.0.0.6:8080", "bad", "10.0.0.7:8080"}
	filtered := ifw.FilterAddresses(net.ParseIP("192.168.1.10"), addresses)
	if len(filtered) != 1 || filtered[0] != "10.0.0.6:8080" {
		t.Fatalf("unexpected filtered addresses: %v", filtered)
	}

	emptyFW, err := NewIndexedFirewall(&stubRulesProvider{})
	if err != nil {
		t.Fatalf("NewIndexedFirewall with empty provider failed: %v", err)
	}
	if !emptyFW.IsAllowed(net.ParseIP("1.1.1.1"), net.ParseIP("2.2.2.2")) {
		t.Fatal("empty indexed firewall should be permissive")
	}
	emptyStats := emptyFW.GetStats()
	if emptyStats.TotalChecks != 1 || emptyStats.TotalAllowed != 1 || emptyStats.TotalBlocked != 0 {
		t.Fatalf("unexpected empty firewall stats: %+v", emptyStats)
	}

	providerErr := errors.New("provider error")
	if _, err := NewIndexedFirewall(&stubRulesProvider{getErr: providerErr}); !errors.Is(err, providerErr) {
		t.Fatalf("expected provider error from NewIndexedFirewall, got %v", err)
	}

	broken := &IndexedFirewall{
		index: map[string][]*net.IPNet{
			"broken": {ipToNetwork(net.ParseIP("10.0.0.5"))},
		},
	}
	if broken.IsAllowed(net.ParseIP("192.168.1.10"), net.ParseIP("10.0.0.5")) {
		t.Fatal("invalid source key should not match")
	}
	if out := broken.FilterAddresses(net.ParseIP("192.168.1.10"), []string{"10.0.0.5:80"}); len(out) != 0 {
		t.Fatalf("expected broken index to filter all addresses, got %v", out)
	}

	provider.getErr = providerErr
	if err := ifw.Reload(); !errors.Is(err, providerErr) {
		t.Fatalf("expected provider error from Reload, got %v", err)
	}
	provider.getErr = nil
	}

func TestDatabaseRulesProviderBehaviors(t *testing.T) {
	t.Run("new provider validates ping", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
		if err != nil {
			t.Fatalf("sqlmock.New failed: %v", err)
		}
		defer db.Close()

		mock.ExpectPing().WillReturnError(errors.New("ping failure"))
		_, err = NewDatabaseRulesProvider(db)
		if err == nil || !strings.Contains(err.Error(), "failed to ping database") {
			t.Fatalf("expected ping error, got %v", err)
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet sqlmock expectations: %v", err)
		}
	})

	t.Run("get rules success and close", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
		if err != nil {
			t.Fatalf("sqlmock.New failed: %v", err)
		}

		mock.ExpectPing()
		provider, err := NewDatabaseRulesProvider(db)
		if err != nil {
			t.Fatalf("NewDatabaseRulesProvider failed: %v", err)
		}

		rows := sqlmock.NewRows([]string{"source_ip", "source_network", "dest_ip", "dest_network"}).
			AddRow("192.168.1.10", nil, "10.0.0.5", nil).
			AddRow(nil, "192.168.1.0/24", nil, "10.0.0.0/24")

		mock.ExpectQuery("SELECT source_ip, source_network, dest_ip, dest_network").WillReturnRows(rows)

		rules, err := provider.GetRules()
		if err != nil {
			t.Fatalf("GetRules failed: %v", err)
		}
		if len(rules) != 2 {
			t.Fatalf("expected 2 rules, got %d", len(rules))
		}
		if !rules[0].SourceIP.Equal(net.ParseIP("192.168.1.10")) || !rules[0].DestIP.Equal(net.ParseIP("10.0.0.5")) {
			t.Fatalf("unexpected first rule: %+v", rules[0])
		}
		if rules[1].SourceNetwork == nil || rules[1].DestNetwork == nil {
			t.Fatalf("expected CIDR rule, got %+v", rules[1])
		}

		mock.ExpectExec("CREATE TABLE IF NOT EXISTS firewall_rules").WillReturnResult(sqlmock.NewResult(0, 0))
		if err := provider.CreateTablesIfNotExist(); err != nil {
			t.Fatalf("CreateTablesIfNotExist failed: %v", err)
		}

		mock.ExpectClose()
		if err := provider.Close(); err != nil {
			t.Fatalf("Close failed: %v", err)
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet sqlmock expectations: %v", err)
		}
	})

	t.Run("query and parse failures", func(t *testing.T) {
		tests := []struct {
			name   string
			rows   *sqlmock.Rows
			query  error
			want   string
			rowErr func(*sqlmock.Rows) *sqlmock.Rows
		}{
			{
				name:  "query error",
				query: errors.New("query failure"),
				want:  "failed to query firewall rules",
			},
			{
				name: "invalid source cidr",
				rows: sqlmock.NewRows([]string{"source_ip", "source_network", "dest_ip", "dest_network"}).
					AddRow(nil, "192.168.1.0/99", "10.0.0.5", nil),
				want: "invalid source CIDR in database",
			},
			{
				name: "invalid source ip",
				rows: sqlmock.NewRows([]string{"source_ip", "source_network", "dest_ip", "dest_network"}).
					AddRow("bad-ip", nil, "10.0.0.5", nil),
				want: "invalid source IP in database",
			},
			{
				name: "invalid destination cidr",
				rows: sqlmock.NewRows([]string{"source_ip", "source_network", "dest_ip", "dest_network"}).
					AddRow("192.168.1.10", nil, nil, "10.0.0.0/99"),
				want: "invalid destination CIDR in database",
			},
			{
				name: "invalid destination ip",
				rows: sqlmock.NewRows([]string{"source_ip", "source_network", "dest_ip", "dest_network"}).
					AddRow("192.168.1.10", nil, "bad-ip", nil),
				want: "invalid destination IP in database",
			},
			{
				name: "row iteration error",
				rows: sqlmock.NewRows([]string{"source_ip", "source_network", "dest_ip", "dest_network"}).
					AddRow("192.168.1.10", nil, "10.0.0.5", nil).
					RowError(0, errors.New("row failure")),
				want: "error iterating rows",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
				if err != nil {
					t.Fatalf("sqlmock.New failed: %v", err)
				}
				defer db.Close()

				mock.ExpectPing()
				provider, err := NewDatabaseRulesProvider(db)
				if err != nil {
					t.Fatalf("NewDatabaseRulesProvider failed: %v", err)
				}

				query := mock.ExpectQuery("SELECT source_ip, source_network, dest_ip, dest_network")
				if tt.query != nil {
					query.WillReturnError(tt.query)
				} else {
					query.WillReturnRows(tt.rows)
				}

				_, err = provider.GetRules()
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("expected error containing %q, got %v", tt.want, err)
				}

				mock.ExpectClose()
				if err := provider.Close(); err != nil {
					t.Fatalf("Close failed: %v", err)
				}

				if err := mock.ExpectationsWereMet(); err != nil {
					t.Fatalf("unmet sqlmock expectations: %v", err)
				}
			})
		}
	})

	t.Run("create tables error", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
		if err != nil {
			t.Fatalf("sqlmock.New failed: %v", err)
		}
		defer db.Close()

		mock.ExpectPing()
		provider, err := NewDatabaseRulesProvider(db)
		if err != nil {
			t.Fatalf("NewDatabaseRulesProvider failed: %v", err)
		}

		mock.ExpectExec("CREATE TABLE IF NOT EXISTS firewall_rules").WillReturnError(errors.New("exec failure"))
		if err := provider.CreateTablesIfNotExist(); err == nil || !strings.Contains(err.Error(), "failed to create tables") {
			t.Fatalf("expected create tables error, got %v", err)
		}

		mock.ExpectClose()
		if err := provider.Close(); err != nil {
			t.Fatalf("Close failed: %v", err)
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet sqlmock expectations: %v", err)
		}
	})
}