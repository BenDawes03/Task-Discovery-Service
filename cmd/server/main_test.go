package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rivo/tview"

	"tds/pkg/registry"
)

func TestAskTerminalOptionsNoUIAndTLS(t *testing.T) {
	oldForceUI := forceUI
	oldNoUI := noUI
	oldUseTLS := useTLS
	oldArgs := os.Args
	defer func() {
		forceUI = oldForceUI
		noUI = oldNoUI
		useTLS = oldUseTLS
		os.Args = oldArgs
	}()

	forceUI = false
	noUI = true
	useTLS = true
	os.Args = []string{"server.test"}

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
