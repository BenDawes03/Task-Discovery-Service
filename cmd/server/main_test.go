package main

import (
	"bufio"
	"context"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

func TestRenderDetailsTableNoServicesRendersHeadersOnly(t *testing.T) {
	oldDetailTable := detailTable
	defer func() {
		detailTable = oldDetailTable
	}()

	detailTable = tview.NewTable()
	renderDetailsTable(nil)

	if got := detailTable.GetCell(0, 0).Text; got != " Address " {
		t.Fatalf("unexpected header cell text: %q", got)
	}
	if cell := detailTable.GetCell(1, 0); cell != nil && cell.Text != "" {
		t.Fatalf("expected no data rows, got first row cell text %q", cell.Text)
	}
}

func TestWriteToLogViewBoundsLogLines(t *testing.T) {
	oldLogView := logView
	oldLogLines := logLines
	oldMaxLogLines := maxLogLines
	defer func() {
		logView = oldLogView
		logLines = oldLogLines
		maxLogLines = oldMaxLogLines
	}()

	logView = tview.NewTextView()
	logLines = nil
	maxLogLines = 2

	writeToLogView("first")
	writeToLogView("second")
	writeToLogView("third")

	if len(logLines) != 2 {
		t.Fatalf("expected bounded log lines length 2, got %d", len(logLines))
	}
	if !strings.Contains(logLines[0], "second") || !strings.Contains(logLines[1], "third") {
		t.Fatalf("expected oldest line to be dropped, got %#v", logLines)
	}
}

func TestProcessTransportModeFlags(t *testing.T) {
	oldUseTLS := useTLS
	defer func() { useTLS = oldUseTLS }()

	useTLS = false
	processTransportModeFlags(false, false, true)
	if !useTLS {
		t.Fatalf("expected TLS mode to enable useTLS")
	}

	useTLS = true
	processTransportModeFlags(true, false, false)
	if useTLS {
		t.Fatalf("expected explicit tcp mode to disable useTLS")
	}

	useTLS = true
	processTransportModeFlags(false, true, false)
	if useTLS {
		t.Fatalf("expected udp mode to disable useTLS")
	}
}

func TestDetermineEffectiveFirewallEnabled(t *testing.T) {
	oldDisabled := firewallDisabledFlag
	oldEnabled := firewallEnabledFlag
	oldRules := firewallRulesPath
	defer func() {
		firewallDisabledFlag = oldDisabled
		firewallEnabledFlag = oldEnabled
		firewallRulesPath = oldRules
	}()

	firewallDisabledFlag = true
	firewallEnabledFlag = true
	firewallRulesPath = "some.rules"
	if determineEffectiveFirewallEnabled() {
		t.Fatalf("expected no-firewall to take precedence")
	}

	firewallDisabledFlag = false
	firewallEnabledFlag = true
	firewallRulesPath = ""
	if !determineEffectiveFirewallEnabled() {
		t.Fatalf("expected firewall flag to enable firewall")
	}

	firewallDisabledFlag = false
	firewallEnabledFlag = false
	firewallRulesPath = "firewall_rules.txt"
	if !determineEffectiveFirewallEnabled() {
		t.Fatalf("expected firewall rules path to imply enabled firewall")
	}

	firewallDisabledFlag = false
	firewallEnabledFlag = false
	firewallRulesPath = ""
	if determineEffectiveFirewallEnabled() {
		t.Fatalf("expected firewall disabled by default")
	}
}

func TestConfigureFirewallDisabledModesAndPermissive(t *testing.T) {
	oldRunningTUI := runningTUI
	oldLogWriter := logWriter
	oldLogMessageChan := logMessageChan
	oldRulesPath := firewallRulesPath
	defer func() {
		runningTUI = oldRunningTUI
		logWriter = oldLogWriter
		logMessageChan = oldLogMessageChan
		firewallRulesPath = oldRulesPath
	}()

	runningTUI = true
	logWriter = io.Discard
	logMessageChan = make(chan string, 10)

	firewallRulesPath = "custom.rules"
	fw := configureFirewall(false, false)
	if fw != nil {
		t.Fatalf("expected nil firewall when disabled")
	}

	firewallRulesPath = ""
	fw = configureFirewall(false, false)
	if fw != nil {
		t.Fatalf("expected nil firewall when disabled without rules")
	}

	firewallRulesPath = ""
	fw = configureFirewall(false, true)
	if fw == nil {
		t.Fatalf("expected permissive firewall when enabled without rules")
	}
}

func TestConfigureTUIIOHeadlessNoop(t *testing.T) {
	oldRunningTUI := runningTUI
	oldStderr := os.Stderr
	defer func() {
		runningTUI = oldRunningTUI
		os.Stderr = oldStderr
	}()

	runningTUI = false
	cleanup := configureTUIIO(false)
	cleanup()

	if runningTUI {
		t.Fatalf("expected runningTUI to remain false in headless mode")
	}
}

func TestSetupLoggingFallbackAndCleanup(t *testing.T) {
	oldLogDir := logDir
	oldLogFile := logFile
	oldLogWriter := logWriter
	defer func() {
		logDir = oldLogDir
		logFile = oldLogFile
		logWriter = oldLogWriter
	}()

	tmpFile := filepath.Join(t.TempDir(), "not_a_dir")
	if err := os.WriteFile(tmpFile, []byte("x"), 0644); err != nil {
		t.Fatalf("failed to create file path for invalid log dir: %v", err)
	}

	logDir = tmpFile
	logFile = nil
	logWriter = nil

	cleanup := setupLogging()
	cleanup()

	if logWriter != os.Stderr {
		t.Fatalf("expected setupLogging fallback to stderr when initLogFile fails")
	}
}

func TestConfigureTUIIOInteractivePathRestoresStderr(t *testing.T) {
	oldRunningTUI := runningTUI
	oldOriginalStderr := originalStderr
	oldStderr := os.Stderr
	defer func() {
		runningTUI = oldRunningTUI
		originalStderr = oldOriginalStderr
		os.Stderr = oldStderr
	}()

	runningTUI = false
	originalStderr = nil

	cleanup := configureTUIIO(true)
	if !runningTUI {
		t.Fatalf("expected runningTUI=true after interactive setup")
	}
	if originalStderr == nil {
		t.Fatalf("expected originalStderr to be captured")
	}

	cleanup()
	if os.Stderr != oldStderr {
		t.Fatalf("expected cleanup to restore original stderr")
	}
}

func TestConfigureFirewallEnabledWithRulesFile(t *testing.T) {
	oldRunningTUI := runningTUI
	oldLogWriter := logWriter
	oldLogMessageChan := logMessageChan
	oldRulesPath := firewallRulesPath
	defer func() {
		runningTUI = oldRunningTUI
		logWriter = oldLogWriter
		logMessageChan = oldLogMessageChan
		firewallRulesPath = oldRulesPath
	}()

	runningTUI = true
	logWriter = io.Discard
	logMessageChan = make(chan string, 10)

	rulesFile := filepath.Join(t.TempDir(), "firewall_rules.txt")
	rules := "192.168.1.10 10.0.0.5\n"
	if err := os.WriteFile(rulesFile, []byte(rules), 0644); err != nil {
		t.Fatalf("failed to write rules file: %v", err)
	}

	firewallRulesPath = rulesFile
	fw := configureFirewall(false, true)
	if fw == nil {
		t.Fatalf("expected loaded firewall when rules file is provided")
	}
	if fw.RuleCount() != 1 {
		t.Fatalf("expected 1 firewall rule loaded, got %d", fw.RuleCount())
	}
}

func TestInitializeRegistryReturnsMemoryRegistry(t *testing.T) {
	oldStoreURL := storeURL
	oldRunningTUI := runningTUI
	oldLogWriter := logWriter
	oldLogMessageChan := logMessageChan
	oldDatabaseEnv, hadDatabaseEnv := os.LookupEnv("DATABASE_URL")
	defer func() {
		storeURL = oldStoreURL
		runningTUI = oldRunningTUI
		logWriter = oldLogWriter
		logMessageChan = oldLogMessageChan
		if hadDatabaseEnv {
			_ = os.Setenv("DATABASE_URL", oldDatabaseEnv)
		} else {
			_ = os.Unsetenv("DATABASE_URL")
		}
	}()

	storeURL = ""
	_ = os.Unsetenv("DATABASE_URL")
	runningTUI = true
	logWriter = io.Discard
	logMessageChan = make(chan string, 10)

	got := initializeRegistry(false, nil)
	if _, ok := got.(*registry.MemoryRegistry); !ok {
		t.Fatalf("expected in-memory registry, got %T", got)
	}
}

func TestStartTransportServerUsesExpectedBackendByMode(t *testing.T) {
	oldReg := reg
	oldListenPort := listenPort
	oldHeartbeatTimeout := heartbeatTimeout
	oldMaxUDP := maxConcurrentUDP
	oldMaxTCP := maxConcurrentTCP
	oldRunningTUI := runningTUI
	oldLogWriter := logWriter
	oldStartUDP := startUDPServerWithContextFn
	oldStartTCP := startTCPServerWithContextFn
	oldStartTLS := startTCPServerTLSWithContextFn
	oldBroadcast := broadcastServerInfoFn
	defer func() {
		reg = oldReg
		listenPort = oldListenPort
		heartbeatTimeout = oldHeartbeatTimeout
		maxConcurrentUDP = oldMaxUDP
		maxConcurrentTCP = oldMaxTCP
		runningTUI = oldRunningTUI
		logWriter = oldLogWriter
		startUDPServerWithContextFn = oldStartUDP
		startTCPServerWithContextFn = oldStartTCP
		startTCPServerTLSWithContextFn = oldStartTLS
		broadcastServerInfoFn = oldBroadcast
	}()

	reg = registry.NewMemoryRegistry()
	listenPort = 5500
	heartbeatTimeout = 30 * time.Second
	maxConcurrentUDP = 111
	maxConcurrentTCP = 222
	runningTUI = true
	logWriter = io.Discard

	var udpCalls, tcpCalls, tlsCalls, broadcastCalls int32
	startUDPServerWithContextFn = func(ctx context.Context, _ registry.Registry, _ int, _ int64, _ func(string)) error {
		atomic.AddInt32(&udpCalls, 1)
		return context.Canceled
	}
	startTCPServerWithContextFn = func(ctx context.Context, _ registry.Registry, _ int, _ int64, _ func(string)) error {
		atomic.AddInt32(&tcpCalls, 1)
		return context.Canceled
	}
	startTCPServerTLSWithContextFn = func(ctx context.Context, _ registry.Registry, _ int, _ int64, _, _, _ string, _ func(string)) error {
		atomic.AddInt32(&tlsCalls, 1)
		return context.Canceled
	}
	broadcastServerInfoFn = func(_ int, _ time.Duration, _ time.Time) {
		atomic.AddInt32(&broadcastCalls, 1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	startTransportServer(ctx, "udp", false)
	startTransportServer(ctx, "tcp", false)
	startTransportServer(ctx, "tls", false)

	time.Sleep(50 * time.Millisecond)

	if atomic.LoadInt32(&udpCalls) == 0 {
		t.Fatalf("expected UDP transport backend to be called")
	}
	if atomic.LoadInt32(&tcpCalls) == 0 {
		t.Fatalf("expected TCP transport backend to be called")
	}
	if atomic.LoadInt32(&tlsCalls) == 0 {
		t.Fatalf("expected TLS transport backend to be called")
	}
	if atomic.LoadInt32(&broadcastCalls) < 3 {
		t.Fatalf("expected broadcast to be called for each startup path")
	}
}

func TestSetupUILayoutWithStubbedRunner(t *testing.T) {
	oldApp := app
	oldTaskList := taskList
	oldDetailTable := detailTable
	oldLogView := logView
	oldSearchField := searchField
	oldRun := runTviewAppFn
	defer func() {
		app = oldApp
		taskList = oldTaskList
		detailTable = oldDetailTable
		logView = oldLogView
		searchField = oldSearchField
		runTviewAppFn = oldRun
	}()

	app = tview.NewApplication()
	taskList = tview.NewList()
	detailTable = tview.NewTable()
	logView = tview.NewTextView()
	searchField = tview.NewInputField().SetLabel(" Filter: ")

	ran := false
	runTviewAppFn = func(a *tview.Application, root tview.Primitive) error {
		ran = true
		if a == nil || root == nil {
			t.Fatalf("expected non-nil app and root")
		}
		return nil
	}

	setupUILayout()
	if !ran {
		t.Fatalf("expected stubbed UI runner to be called")
	}
}

func TestRunHeadlessLoopReturnsOnContextCancel(t *testing.T) {
	oldReg := reg
	oldCleanupInterval := cleanupInterval
	oldHeartbeat := heartbeatTimeout
	oldRunningTUI := runningTUI
	oldLogWriter := logWriter
	defer func() {
		reg = oldReg
		cleanupInterval = oldCleanupInterval
		heartbeatTimeout = oldHeartbeat
		runningTUI = oldRunningTUI
		logWriter = oldLogWriter
	}()

	reg = registry.NewMemoryRegistry()
	cleanupInterval = 5 * time.Millisecond
	heartbeatTimeout = 5 * time.Millisecond
	runningTUI = true
	logWriter = io.Discard

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	runHeadlessLoop(ctx)
}

func TestRunTUILoopReturnsWhenUILayoutStops(t *testing.T) {
	oldReg := reg
	oldCleanupInterval := cleanupInterval
	oldRunningTUI := runningTUI
	oldLogWriter := logWriter
	oldSetup := setupUILayoutFn
	defer func() {
		reg = oldReg
		cleanupInterval = oldCleanupInterval
		runningTUI = oldRunningTUI
		logWriter = oldLogWriter
		setupUILayoutFn = oldSetup
	}()

	reg = registry.NewMemoryRegistry()
	cleanupInterval = 5 * time.Millisecond
	runningTUI = true
	logWriter = io.Discard

	called := false
	setupUILayoutFn = func() { called = true }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runTUILoop(ctx)
	if !called {
		t.Fatalf("expected runTUILoop to invoke setupUILayoutFn")
	}
}

func TestMainDispatchesHeadlessPath(t *testing.T) {
	oldArgs := os.Args
	oldCommandLine := flag.CommandLine
	oldNoUI := noUI
	oldForceUI := forceUI
	oldIsTerminalFn := isTerminalFn
	oldRunHeadless := runHeadlessFn
	oldRunTUI := runTUIFn
	oldStartUDP := startUDPServerWithContextFn
	oldStartTCP := startTCPServerWithContextFn
	oldStartTLS := startTCPServerTLSWithContextFn
	oldBroadcast := broadcastServerInfoFn
	oldLogDir := logDir
	oldStoreURL := storeURL
	defer func() {
		os.Args = oldArgs
		flag.CommandLine = oldCommandLine
		noUI = oldNoUI
		forceUI = oldForceUI
		isTerminalFn = oldIsTerminalFn
		runHeadlessFn = oldRunHeadless
		runTUIFn = oldRunTUI
		startUDPServerWithContextFn = oldStartUDP
		startTCPServerWithContextFn = oldStartTCP
		startTCPServerTLSWithContextFn = oldStartTLS
		broadcastServerInfoFn = oldBroadcast
		logDir = oldLogDir
		storeURL = oldStoreURL
	}()

	flag.CommandLine = flag.NewFlagSet("server.test", flag.ContinueOnError)
	flag.CommandLine.SetOutput(io.Discard)
	os.Args = []string{"server.test", "--no-ui", "--port=5510", "--log-dir=" + t.TempDir()}
	isTerminalFn = func(fd int) bool { return false }

	var headlessCalled int32
	runHeadlessFn = func(ctx context.Context) { atomic.AddInt32(&headlessCalled, 1) }
	runTUIFn = func(ctx context.Context) { t.Fatalf("did not expect TUI path") }
	startUDPServerWithContextFn = func(ctx context.Context, _ registry.Registry, _ int, _ int64, _ func(string)) error { return context.Canceled }
	startTCPServerWithContextFn = func(ctx context.Context, _ registry.Registry, _ int, _ int64, _ func(string)) error { return context.Canceled }
	startTCPServerTLSWithContextFn = func(ctx context.Context, _ registry.Registry, _ int, _ int64, _, _, _ string, _ func(string)) error { return context.Canceled }
	broadcastServerInfoFn = func(_ int, _ time.Duration, _ time.Time) {}
	storeURL = ""

	main()

	if atomic.LoadInt32(&headlessCalled) != 1 {
		t.Fatalf("expected main to dispatch to headless loop once")
	}
}
