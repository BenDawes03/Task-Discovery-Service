package main

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rivo/tview"

	"tds/pkg/registry"
)

func TestAskTerminalOptionsNoUIAndTLS(t *testing.T) {
	oldForceUI := forceUI
	oldNoUI := noUI
	oldUseTLS := useTLS
	oldArgs := os.Args
	oldIsTerminalFn := isTerminalFn
	defer func() {
		forceUI = oldForceUI
		noUI = oldNoUI
		useTLS = oldUseTLS
		os.Args = oldArgs
		isTerminalFn = oldIsTerminalFn
	}()

	forceUI = false
	noUI = true
	useTLS = true
	os.Args = []string{"server.test"}
	isTerminalFn = func(fd int) bool { return false }

	transportMode, runTUI, forced := askTerminalOptions()
	if transportMode != "tls" {
		t.Fatalf("expected transport tls, got %q", transportMode)
	}
	if runTUI {
		t.Fatalf("expected runTUI=false when noUI=true")
	}
	if forced {
		t.Fatalf("expected forced=false when forceUI=false")
	}
}

func TestAskTerminalOptionsInteractivePromptsApplyAndTLSNested(t *testing.T) {
	oldForceUI := forceUI
	oldNoUI := noUI
	oldUseTLS := useTLS
	oldArgs := os.Args
	oldIsTerminalFn := isTerminalFn
	oldPromptReaderFn := promptReaderFn
	oldListenPort := listenPort
	oldHeartbeatTimeout := heartbeatTimeout
	oldCleanupInterval := cleanupInterval
	oldLogDir := logDir
	oldTLSCertFile := tlsCertFile
	oldTLSKeyFile := tlsKeyFile
	oldTLSClientCAFile := tlsClientCAFile
	oldStoreURL := storeURL
	oldCacheMaxSize := cacheMaxSize
	oldFirewallEnabledFlag := firewallEnabledFlag
	oldFirewallDisabledFlag := firewallDisabledFlag
	oldFirewallRulesPath := firewallRulesPath
	defer func() {
		forceUI = oldForceUI
		noUI = oldNoUI
		useTLS = oldUseTLS
		os.Args = oldArgs
		isTerminalFn = oldIsTerminalFn
		promptReaderFn = oldPromptReaderFn
		listenPort = oldListenPort
		heartbeatTimeout = oldHeartbeatTimeout
		cleanupInterval = oldCleanupInterval
		logDir = oldLogDir
		tlsCertFile = oldTLSCertFile
		tlsKeyFile = oldTLSKeyFile
		tlsClientCAFile = oldTLSClientCAFile
		storeURL = oldStoreURL
		cacheMaxSize = oldCacheMaxSize
		firewallEnabledFlag = oldFirewallEnabledFlag
		firewallDisabledFlag = oldFirewallDisabledFlag
		firewallRulesPath = oldFirewallRulesPath
	}()

	forceUI = false
	noUI = false
	useTLS = false
	os.Args = []string{"server.test"}
	listenPort = 5000
	heartbeatTimeout = 60 * time.Second
	cleanupInterval = 10 * time.Second
	logDir = "logs"
	tlsCertFile = "certs/server.crt"
	tlsKeyFile = "certs/server.key"
	tlsClientCAFile = "certs/ca.crt"
	storeURL = ""
	cacheMaxSize = 100
	firewallEnabledFlag = false
	firewallDisabledFlag = false
	firewallRulesPath = ""

	isTerminalFn = func(fd int) bool { return true }
	promptReaderFn = func() *bufio.Reader {
		input := strings.Join([]string{
			"3",                   // transport -> tls
			"5500",                // port
			"75s",                 // heartbeat timeout
			"15s",                 // cleanup interval
			"5000",                // max concurrent tls connections
			"custom-logs",         // log dir
			"certs/custom.crt",    // tls cert
			"certs/custom.key",    // tls key
			"certs/custom-ca.crt", // tls client ca
			"n",                   // db disabled
			"n",                   // firewall disabled
			"n",                   // no tui
		}, "\n") + "\n"
		return bufio.NewReader(strings.NewReader(input))
	}

	transportMode, runTUI, _ := askTerminalOptions()

	if transportMode != "tls" {
		t.Fatalf("expected tls transport mode, got %q", transportMode)
	}
	if runTUI {
		t.Fatalf("expected runTUI=false from interactive choice")
	}
	if listenPort != 5500 {
		t.Fatalf("expected prompted port 5500, got %d", listenPort)
	}
	if heartbeatTimeout != 75*time.Second {
		t.Fatalf("expected prompted heartbeat timeout 75s, got %s", heartbeatTimeout)
	}
	if cleanupInterval != 15*time.Second {
		t.Fatalf("expected prompted cleanup interval 15s, got %s", cleanupInterval)
	}
	if logDir != "custom-logs" {
		t.Fatalf("expected prompted log dir custom-logs, got %q", logDir)
	}
	if tlsCertFile != "certs/custom.crt" || tlsKeyFile != "certs/custom.key" || tlsClientCAFile != "certs/custom-ca.crt" {
		t.Fatalf("expected prompted TLS files to be applied")
	}
}

func TestAskTerminalOptionsSkipsPromptedFieldsWhenFlagsProvided(t *testing.T) {
	oldForceUI := forceUI
	oldNoUI := noUI
	oldUseTLS := useTLS
	oldArgs := os.Args
	oldIsTerminalFn := isTerminalFn
	oldPromptReaderFn := promptReaderFn
	oldListenPort := listenPort
	oldHeartbeatTimeout := heartbeatTimeout
	oldCleanupInterval := cleanupInterval
	oldLogDir := logDir
	oldTLSCertFile := tlsCertFile
	oldTLSKeyFile := tlsKeyFile
	oldTLSClientCAFile := tlsClientCAFile
	oldStoreURL := storeURL
	oldCacheMaxSize := cacheMaxSize
	defer func() {
		forceUI = oldForceUI
		noUI = oldNoUI
		useTLS = oldUseTLS
		os.Args = oldArgs
		isTerminalFn = oldIsTerminalFn
		promptReaderFn = oldPromptReaderFn
		listenPort = oldListenPort
		heartbeatTimeout = oldHeartbeatTimeout
		cleanupInterval = oldCleanupInterval
		logDir = oldLogDir
		tlsCertFile = oldTLSCertFile
		tlsKeyFile = oldTLSKeyFile
		tlsClientCAFile = oldTLSClientCAFile
		storeURL = oldStoreURL
		cacheMaxSize = oldCacheMaxSize
	}()

	forceUI = false
	noUI = false
	useTLS = true
	os.Args = []string{
		"server.test",
		"--tls",
		"--port=6001",
		"--heartbeat-timeout=80s",
		"--cleanup-interval=20s",
		"--log-dir=my-logs",
		"--tls-cert=certs/flag.crt",
		"--tls-key=certs/flag.key",
		"--tls-client-ca=certs/flag-ca.crt",
		"--cache-max-size=42",
	}
	listenPort = 6001
	heartbeatTimeout = 80 * time.Second
	cleanupInterval = 20 * time.Second
	logDir = "my-logs"
	tlsCertFile = "certs/flag.crt"
	tlsKeyFile = "certs/flag.key"
	tlsClientCAFile = "certs/flag-ca.crt"
	storeURL = ""
	cacheMaxSize = 42

	isTerminalFn = func(fd int) bool { return true }
	promptReaderFn = func() *bufio.Reader {
		input := strings.Join([]string{
			"y",                    // db enabled
			"postgresql://example", // db url
			"n",                    // firewall disabled
			"n",                    // no tui
		}, "\n") + "\n"
		return bufio.NewReader(strings.NewReader(input))
	}

	transportMode, runTUI, _ := askTerminalOptions()
	if transportMode != "tls" {
		t.Fatalf("expected tls transport mode with --tls, got %q", transportMode)
	}
	if runTUI {
		t.Fatalf("expected runTUI=false from interactive choice")
	}
	if listenPort != 6001 || heartbeatTimeout != 80*time.Second || cleanupInterval != 20*time.Second || logDir != "my-logs" {
		t.Fatalf("expected flagged core settings to remain unchanged")
	}
	if tlsCertFile != "certs/flag.crt" || tlsKeyFile != "certs/flag.key" || tlsClientCAFile != "certs/flag-ca.crt" {
		t.Fatalf("expected flagged TLS settings to remain unchanged")
	}
	if cacheMaxSize != 42 {
		t.Fatalf("expected cache-max-size from flag to remain unchanged, got %d", cacheMaxSize)
	}
}

func TestAskTerminalOptionsMalformedPromptInputKeepsDefaults(t *testing.T) {
	oldForceUI := forceUI
	oldNoUI := noUI
	oldUseTLS := useTLS
	oldArgs := os.Args
	oldIsTerminalFn := isTerminalFn
	oldPromptReaderFn := promptReaderFn
	oldListenPort := listenPort
	oldHeartbeatTimeout := heartbeatTimeout
	oldCleanupInterval := cleanupInterval
	oldLogDir := logDir
	oldStoreURL := storeURL
	oldCacheMaxSize := cacheMaxSize
	defer func() {
		forceUI = oldForceUI
		noUI = oldNoUI
		useTLS = oldUseTLS
		os.Args = oldArgs
		isTerminalFn = oldIsTerminalFn
		promptReaderFn = oldPromptReaderFn
		listenPort = oldListenPort
		heartbeatTimeout = oldHeartbeatTimeout
		cleanupInterval = oldCleanupInterval
		logDir = oldLogDir
		storeURL = oldStoreURL
		cacheMaxSize = oldCacheMaxSize
	}()

	forceUI = false
	noUI = false
	useTLS = false
	os.Args = []string{"server.test"}
	listenPort = 5000
	heartbeatTimeout = 60 * time.Second
	cleanupInterval = 10 * time.Second
	logDir = "logs"
	storeURL = ""
	cacheMaxSize = 100

	isTerminalFn = func(fd int) bool { return true }
	promptReaderFn = func() *bufio.Reader {
		input := strings.Join([]string{
			"bad-choice",         // invalid transport -> should default UDP
			"not-a-port",         // invalid port -> keep default
			"not-a-duration",     // invalid heartbeat -> keep default
			"still-not-duration", // invalid cleanup -> keep default
			"not-a-number",       // invalid max udp handlers -> keep default
			"",                   // empty log dir -> keep default
			"n",                  // db disabled
			"n",                  // firewall disabled
			"n",                  // no tui
		}, "\n") + "\n"
		return bufio.NewReader(strings.NewReader(input))
	}

	transportMode, runTUI, _ := askTerminalOptions()
	if transportMode != "udp" {
		t.Fatalf("expected fallback transport udp, got %q", transportMode)
	}
	if runTUI {
		t.Fatalf("expected runTUI=false from interactive choice")
	}
	if listenPort != 5000 {
		t.Fatalf("expected default port retained, got %d", listenPort)
	}
	if heartbeatTimeout != 60*time.Second {
		t.Fatalf("expected default heartbeat timeout retained, got %s", heartbeatTimeout)
	}
	if cleanupInterval != 10*time.Second {
		t.Fatalf("expected default cleanup interval retained, got %s", cleanupInterval)
	}
	if logDir != "logs" {
		t.Fatalf("expected default log dir retained, got %q", logDir)
	}
}

func TestRequestDashboardUpdateQueuesAtMostOneSignal(t *testing.T) {
	oldRunningTUI := runningTUI
	oldUpdateDashboard := updateDashboard
	defer func() {
		runningTUI = oldRunningTUI
		updateDashboard = oldUpdateDashboard
	}()

	runningTUI = true
	updateDashboard = make(chan struct{}, 1)

	requestDashboardUpdate()
	requestDashboardUpdate()

	if got := len(updateDashboard); got != 1 {
		t.Fatalf("expected exactly one queued update signal, got %d", got)
	}
}

func TestRequestDashboardUpdateNoopWhenTUIDisabled(t *testing.T) {
	oldRunningTUI := runningTUI
	oldUpdateDashboard := updateDashboard
	defer func() {
		runningTUI = oldRunningTUI
		updateDashboard = oldUpdateDashboard
	}()

	runningTUI = false
	updateDashboard = make(chan struct{}, 1)

	requestDashboardUpdate()

	if got := len(updateDashboard); got != 0 {
		t.Fatalf("expected no queued update signal, got %d", got)
	}
}

func TestInitLogFileCreatesServerLog(t *testing.T) {
	oldLogDir := logDir
	oldLogFile := logFile
	oldLogWriter := logWriter
	defer func() {
		if logFile != nil {
			_ = logFile.Close()
		}
		logDir = oldLogDir
		logFile = oldLogFile
		logWriter = oldLogWriter
	}()

	logDir = t.TempDir()
	logFile = nil
	logWriter = nil

	if err := initLogFile(); err != nil {
		t.Fatalf("initLogFile failed: %v", err)
	}
	if logFile == nil {
		t.Fatalf("expected logFile to be initialized")
	}
	if logWriter == nil {
		t.Fatalf("expected logWriter to be initialized")
	}

	files, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected exactly one log file in log dir, got %d", len(files))
	}

	if !strings.HasPrefix(files[0].Name(), "server_") {
		t.Fatalf("expected log file name to start with server_, got %q", files[0].Name())
	}

	logPath := filepath.Join(logDir, files[0].Name())
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !strings.Contains(string(contents), "Server Started") {
		t.Fatalf("expected startup marker in log file")
	}
}

func TestLogEventQueuesNonQueryWhenTUIRunning(t *testing.T) {
	oldRunningTUI := runningTUI
	oldLogWriter := logWriter
	oldLogMessageChan := logMessageChan
	defer func() {
		runningTUI = oldRunningTUI
		logWriter = oldLogWriter
		logMessageChan = oldLogMessageChan
	}()

	runningTUI = true
	logWriter = io.Discard
	logMessageChan = make(chan string, 4)

	logEvent("QUERY task-a")
	logEvent("REGISTER task-a -> 10.0.0.1:7000")

	if got := len(logMessageChan); got != 1 {
		t.Fatalf("expected only non-QUERY message to be queued, got %d", got)
	}

	msg := <-logMessageChan
	if !strings.HasPrefix(msg, "REGISTER") {
		t.Fatalf("expected REGISTER message in queue, got %q", msg)
	}
}

func TestUpdateDetailsTableRendersHeadersAndRows(t *testing.T) {
	oldDetailTable := detailTable
	defer func() {
		detailTable = oldDetailTable
	}()

	detailTable = tview.NewTable()
	services := []registry.ServiceEntry{
		{Address: "10.0.0.1:7000", QueryCount: 2},
		{Address: "10.0.0.2:7000", QueryCount: 5},
	}

	updateDetailsTable(services)

	if got := detailTable.GetCell(0, 0).Text; got != " Address " {
		t.Fatalf("unexpected header cell text: %q", got)
	}
	if got := detailTable.GetCell(0, 2).Text; got != " Queries " {
		t.Fatalf("unexpected queries header text: %q", got)
	}
	if got := detailTable.GetCell(1, 0).Text; got != " 10.0.0.1:7000 " {
		t.Fatalf("unexpected first address cell: %q", got)
	}
	if got := detailTable.GetCell(2, 2).Text; got != " 5 " {
		t.Fatalf("unexpected second query cell: %q", got)
	}
}
