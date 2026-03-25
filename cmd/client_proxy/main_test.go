package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rivo/tview"

	"tds/pkg/client"
	"tds/pkg/dht"
)

func withFreshFlags(t *testing.T, args []string) {
	t.Helper()
	oldArgs := os.Args
	oldCommandLine := flag.CommandLine
	os.Args = args
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flag.CommandLine = fs
	t.Cleanup(func() {
		os.Args = oldArgs
		flag.CommandLine = oldCommandLine
	})
}

func TestAskModeOptionsP2PFlags(t *testing.T) {
	oldReplication := dht.ReplicationFactor
	defer func() { dht.ReplicationFactor = oldReplication }()

	withFreshFlags(t, []string{
		"client_proxy.test",
		"-p2p",
		"-p2p-port", "6005",
		"-bootstrap", "127.0.0.1:6000, 127.0.0.1:6002",
		"-k-closest", "7",
		"-p2p-heartbeat-timeout", "90s",
		"-simple-ui",
		"-background",
	})

	mode, transport, p2pPort, bootstrapNodes, background, kClosest, simplifiedUI, p2pHeartbeatTimeout := askModeOptions()
	if mode != "p2p" {
		t.Fatalf("expected p2p mode, got %q", mode)
	}
	if transport != "udp" {
		t.Fatalf("expected default udp transport in p2p mode, got %q", transport)
	}
	if p2pPort != ":6005" {
		t.Fatalf("expected normalized p2p port :6005, got %q", p2pPort)
	}
	if len(bootstrapNodes) != 2 || bootstrapNodes[0] != "127.0.0.1:6000" || bootstrapNodes[1] != "127.0.0.1:6002" {
		t.Fatalf("unexpected bootstrap nodes: %#v", bootstrapNodes)
	}
	if !background {
		t.Fatalf("expected background=true")
	}
	if kClosest != 7 {
		t.Fatalf("expected kClosest=7, got %d", kClosest)
	}
	if !simplifiedUI {
		t.Fatalf("expected simplifiedUI=true")
	}
	if p2pHeartbeatTimeout != 90*time.Second {
		t.Fatalf("expected p2pHeartbeatTimeout=90s, got %s", p2pHeartbeatTimeout)
	}
}

func TestAskModeOptionsCentralizedTCPFlagNonInteractive(t *testing.T) {
	withFreshFlags(t, []string{"client_proxy.test", "-tcp"})

	mode, transport, p2pPort, bootstrapNodes, background, kClosest, simplifiedUI, p2pHeartbeatTimeout := askModeOptions()
	if mode != "centralized" {
		t.Fatalf("expected centralized mode, got %q", mode)
	}
	if transport != "tcp" {
		t.Fatalf("expected tcp transport from flag, got %q", transport)
	}
	if p2pPort != ":6000" {
		t.Fatalf("unexpected default p2p port: %q", p2pPort)
	}
	if len(bootstrapNodes) != 0 {
		t.Fatalf("expected no bootstrap nodes, got %#v", bootstrapNodes)
	}
	if background {
		t.Fatalf("expected background=false")
	}
	if kClosest != dht.ReplicationFactor {
		t.Fatalf("expected default kClosest=%d, got %d", dht.ReplicationFactor, kClosest)
	}
	if simplifiedUI {
		t.Fatalf("expected simplifiedUI=false")
	}
	if p2pHeartbeatTimeout != dht.ServiceHeartbeatTimeout {
		t.Fatalf("expected default p2p heartbeat timeout %s, got %s", dht.ServiceHeartbeatTimeout, p2pHeartbeatTimeout)
	}
}

func TestAskModeOptionsInvalidKClosestFallsBack(t *testing.T) {
	oldReplication := dht.ReplicationFactor
	defer func() { dht.ReplicationFactor = oldReplication }()

	withFreshFlags(t, []string{"client_proxy.test", "-k-closest", "0"})

	_, _, _, _, _, kClosest, _, _ := askModeOptions()
	if kClosest != dht.ReplicationFactor {
		t.Fatalf("expected fallback to default replication factor %d, got %d", dht.ReplicationFactor, kClosest)
	}
}

func TestAskBootstrapNodesRetriesAfterInvalidInput(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("bad-node\n127.0.0.1:7000\n"))
	nodes := askBootstrapNodes(reader)
	if len(nodes) != 1 || nodes[0] != "127.0.0.1:7000" {
		t.Fatalf("unexpected nodes: %#v", nodes)
	}
}

func TestAskBootstrapNodesEmptyReturnsNil(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("\n"))
	nodes := askBootstrapNodes(reader)
	if nodes != nil {
		t.Fatalf("expected nil nodes on empty input, got %#v", nodes)
	}
}

func TestNormalizePortInput(t *testing.T) {
	if got := normalizePortInput("6000"); got != ":6000" {
		t.Fatalf("expected :6000, got %q", got)
	}
	if got := normalizePortInput("127.0.0.1:6000"); got != "127.0.0.1:6000" {
		t.Fatalf("expected unchanged host:port, got %q", got)
	}
	if got := normalizePortInput("   "); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestSimplifyLogLineMappings(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"2026/03/15 10:00:00 stored locally: task-a -> 10.0.0.1", "Stored locally: task-a -> 10.0.0.1"},
		{"[dht] P2P REGISTER task-a -> 10.0.0.1 (from 1.2.3.4)", "REGISTER request: task-a -> 10.0.0.1"},
		{"proxy listening on :5100", "Proxy online: :5100"},
		{"proxy stopped", "Proxy stopped"},
		{"totally unclassified message", ""},
	}
	for _, tc := range cases {
		if got := simplifyLogLine(tc.in); got != tc.want {
			t.Fatalf("simplifyLogLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTrimHelpers(t *testing.T) {
	if got := trimLogEnvelope("[src] 2026/03/15 10:00:00 hello"); got != "hello" {
		t.Fatalf("unexpected trimLogEnvelope result: %q", got)
	}
	if got := trimSourceSuffix("task-a -> 10.0.0.1 (from 1.2.3.4)"); got != "task-a -> 10.0.0.1" {
		t.Fatalf("unexpected trimSourceSuffix result: %q", got)
	}
	if got := shortenStoreMessage("stored on peer: task-a -> 10.0.0.1"); got != "task-a -> 10.0.0.1" {
		t.Fatalf("unexpected shortenStoreMessage result: %q", got)
	}
	if got := trimSourceSuffix("task-a -> 10.0.0.1"); got != "task-a -> 10.0.0.1" {
		t.Fatalf("expected unchanged source suffix string, got %q", got)
	}
	if got := shortenStoreMessage("stored locally task-a"); got != "stored locally task-a" {
		t.Fatalf("expected unchanged store message without colon separator, got %q", got)
	}
}

func TestSimplifyLogLineAdditionalBranches(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"P2P QUERY task-a (from 1.1.1.1)", "QUERY request: task-a"},
		{"registering task-a -> 10.0.0.1", "DHT store started: task-a -> 10.0.0.1"},
		{"querying all for task-a", "DHT query(all): task-a"},
		{"querying task-a", "DHT query: task-a"},
		{"DHT listening on :6000", "DHT node online"},
		{"joining via bootstrap node 127.0.0.1:6000", "Bootstrapping via 127.0.0.1:6000"},
		{"join error with 127.0.0.1:6000: timeout", "Bootstrap failed: 127.0.0.1:6000: timeout"},
		{"received 3 peers from 127.0.0.1:6000", "Peer list updated: received 3 peers from 127.0.0.1:6000"},
		{"joined network, 4 peers known", "Network join complete: joined network, 4 peers known"},
		{"stored on 127.0.0.1: task-a -> 10.0.0.1", "Stored on peer: task-a -> 10.0.0.1"},
		{"store complete: task-a on 3 nodes", "Pool update done: task-a on 3 nodes"},
		{"found locally: task-a -> 10.0.0.1", "Query result(local): task-a -> 10.0.0.1"},
		{"found on 127.0.0.1:6000: task-a -> 10.0.0.1", "Query result(peer): 127.0.0.1:6000: task-a -> 10.0.0.1"},
		{"not found on any k-closest node: task-a", "Query miss: task-a"},
		{"queuing closer node 127.0.0.1:6001", "Retrying with closer peer"},
		{"removed unreachable peer 127.0.0.1:6002", "Peer dropped: 127.0.0.1:6002"},
		{"cleanup removed 3 entries (expired=2, stale-task=1, heartbeat-timeout=1m0s)", "Cleanup: 3 entries (expired=2, stale-task=1, heartbeat-timeout=1m0s)"},
	}

	for _, tc := range cases {
		if got := simplifyLogLine(tc.in); got != tc.want {
			t.Fatalf("simplifyLogLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDashboardWriteSimplifiedAndTrimmed(t *testing.T) {
	d := &p2pDashboard{simplified: true, maxLogLines: 2}

	n, err := d.Write([]byte("stored locally: task-a -> 10.0.0.1\nunknown\n"))
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if n == 0 {
		t.Fatalf("expected bytes written count")
	}

	_, _ = d.Write([]byte("proxy stopped"))
	if d.partialLog != "proxy stopped" {
		t.Fatalf("expected partial log to be buffered, got %q", d.partialLog)
	}
	_, _ = d.Write([]byte("\n"))

	if len(d.logLines) != 2 {
		t.Fatalf("expected 2 simplified log lines, got %d (%#v)", len(d.logLines), d.logLines)
	}
	if d.logLines[0] != "Stored locally: task-a -> 10.0.0.1" {
		t.Fatalf("unexpected first line: %q", d.logLines[0])
	}
	if d.logLines[1] != "Proxy stopped" {
		t.Fatalf("unexpected second line: %q", d.logLines[1])
	}

	d.clearLogs()
	if len(d.logLines) != 0 || d.partialLog != "" {
		t.Fatalf("expected logs to be cleared")
	}
}

func TestDashboardRenderSections(t *testing.T) {
	registry, err := dht.NewDHTRegistry("6009", nil)
	if err != nil {
		t.Fatalf("failed to create DHT registry: %v", err)
	}

	d := newP2PDashboard(registry, ":5100", ":6009", []string{"127.0.0.1:6001"}, 3, false)

	d.renderPeers(nil)
	if got := d.peersTable.GetCell(1, 1).Text; !strings.Contains(got, "No peers") {
		t.Fatalf("expected empty peer message, got %q", got)
	}

	d.renderTasks(nil, map[string][]string{})
	if got := d.tasksTable.GetCell(1, 0).Text; !strings.Contains(got, "No tasks") {
		t.Fatalf("expected empty task message, got %q", got)
	}

	d.logLines = []string{"event-a", "event-b"}
	d.renderLogs()
	if txt := d.logView.GetText(false); !strings.Contains(txt, "event-a") || !strings.Contains(txt, "event-b") {
		t.Fatalf("expected rendered logs in text view, got %q", txt)
	}

	d.clearLogs()
	d.renderLogs()
	if txt := d.logView.GetText(false); !strings.Contains(txt, "Waiting for log messages") {
		t.Fatalf("expected empty log placeholder, got %q", txt)
	}
}

func TestDashboardRenderSimplifiedSections(t *testing.T) {
	registry, err := dht.NewDHTRegistry("6011", nil)
	if err != nil {
		t.Fatalf("failed to create DHT registry: %v", err)
	}

	d := newP2PDashboard(registry, ":5100", ":6011", nil, 4, true)
	d.renderTasks([]string{"task-b", "task-a"}, map[string][]string{
		"task-a": []string{"10.0.0.1", "10.0.0.2"},
		"task-b": []string{"10.0.0.3"},
	})
	if got := d.tasksTable.GetCell(1, 1).Text; !strings.Contains(got, "1") && !strings.Contains(got, "2") {
		t.Fatalf("expected simplified pool-size column, got %q", got)
	}

	d.renderLogs()
	if txt := d.logView.GetText(false); !strings.Contains(txt, "Waiting for DHT events") {
		t.Fatalf("expected simplified empty log placeholder, got %q", txt)
	}
}

func TestDashboardStateAndStopHelpers(t *testing.T) {
	errSentinel := errors.New("proxy failed")
	stopped := false

	d := &p2pDashboard{
		app:    tview.NewApplication(),
		stopCh: make(chan struct{}),
	}

	d.setProxyErr(errSentinel)
	if got := d.getProxyErr(); got == nil || got.Error() != errSentinel.Error() {
		t.Fatalf("expected stored proxy error, got %v", got)
	}

	d.shutdown = func() { stopped = true }
	d.shutdownAndStop(nil)
	if !stopped {
		t.Fatalf("expected shutdown callback to execute")
	}

	select {
	case <-d.stopCh:
		// expected
	default:
		t.Fatalf("expected stop channel to be closed")
	}
}

func TestHeaderAndPlainCell(t *testing.T) {
	if got := headerCell("Address").Text; got != " Address " {
		t.Fatalf("unexpected header cell text: %q", got)
	}
	if got := plainCell("Value").Text; got != " Value " {
		t.Fatalf("unexpected plain cell text: %q", got)
	}
}

func TestQueueRefreshReturnsWhenStopped(t *testing.T) {
	d := &p2pDashboard{app: tview.NewApplication(), stopCh: make(chan struct{})}
	close(d.stopCh)

	d.queueRefresh()
}

func TestRefreshLoopExitsWhenStopped(t *testing.T) {
	d := &p2pDashboard{stopCh: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		d.refreshLoop()
		close(done)
	}()

	close(d.stopCh)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("refreshLoop did not stop after stopCh closed")
	}
}

func TestAskModeOptionsInteractiveP2PPrompts(t *testing.T) {
	oldStdinTerminal := stdinIsTerminalFn
	oldPromptReader := modePromptReaderFn
	defer func() {
		stdinIsTerminalFn = oldStdinTerminal
		modePromptReaderFn = oldPromptReader
	}()

	stdinIsTerminalFn = func() bool { return true }
	modePromptReaderFn = func() *bufio.Reader {
		input := strings.Join([]string{
			"2",                               // p2p mode
			"6007",                            // p2p port
			"bad",                             // invalid bootstrap list
			"127.0.0.1:6000,127.0.0.1:6001",   // valid bootstrap retry
			"n",                               // simplified dashboard disabled
		}, "\n") + "\n"
		return bufio.NewReader(strings.NewReader(input))
	}

	withFreshFlags(t, []string{"client_proxy.test"})
	mode, transport, p2pPort, bootstrapNodes, background, kClosest, simplifiedUI, p2pHeartbeatTimeout := askModeOptions()

	if mode != "p2p" {
		t.Fatalf("expected p2p mode, got %q", mode)
	}
	if transport != "udp" {
		t.Fatalf("expected udp transport in p2p mode, got %q", transport)
	}
	if p2pPort != ":6007" {
		t.Fatalf("expected normalized p2p port :6007, got %q", p2pPort)
	}
	if len(bootstrapNodes) != 2 {
		t.Fatalf("expected bootstrap retry to produce 2 nodes, got %#v", bootstrapNodes)
	}
	if background {
		t.Fatalf("expected background=false")
	}
	if kClosest != dht.ReplicationFactor {
		t.Fatalf("expected default replication factor, got %d", kClosest)
	}
	if simplifiedUI {
		t.Fatalf("expected simplifiedUI=false from explicit n choice")
	}
	if p2pHeartbeatTimeout != dht.ServiceHeartbeatTimeout {
		t.Fatalf("expected default p2p heartbeat timeout %s, got %s", dht.ServiceHeartbeatTimeout, p2pHeartbeatTimeout)
	}
}

func TestAskModeOptionsInteractiveCentralizedTransportPrompt(t *testing.T) {
	oldStdinTerminal := stdinIsTerminalFn
	oldPromptReader := modePromptReaderFn
	defer func() {
		stdinIsTerminalFn = oldStdinTerminal
		modePromptReaderFn = oldPromptReader
	}()

	stdinIsTerminalFn = func() bool { return true }
	modePromptReaderFn = func() *bufio.Reader {
		return bufio.NewReader(strings.NewReader("1\n2\n"))
	}

	withFreshFlags(t, []string{"client_proxy.test"})
	mode, transport, _, _, _, _, _, _ := askModeOptions()
	if mode != "centralized" {
		t.Fatalf("expected centralized mode, got %q", mode)
	}
	if transport != "tcp" {
		t.Fatalf("expected tcp transport from interactive prompt, got %q", transport)
	}
}

func TestMainCentralizedBackgroundUsesTCPRunner(t *testing.T) {
	oldRunProxy := runProxyFn
	oldRunProxyTCP := runProxyTCPFn
	oldRunProxyP2P := runProxyP2PFn
	oldStdinTerminal := stdinIsTerminalFn
	oldStdoutTerminal := stdoutIsTerminalFn
	oldListenEnv, hadListenEnv := os.LookupEnv("TDS_PROXY_LISTEN")
	defer func() {
		runProxyFn = oldRunProxy
		runProxyTCPFn = oldRunProxyTCP
		runProxyP2PFn = oldRunProxyP2P
		stdinIsTerminalFn = oldStdinTerminal
		stdoutIsTerminalFn = oldStdoutTerminal
		if hadListenEnv {
			_ = os.Setenv("TDS_PROXY_LISTEN", oldListenEnv)
		} else {
			_ = os.Unsetenv("TDS_PROXY_LISTEN")
		}
	}()

	withFreshFlags(t, []string{"client_proxy.test", "-background", "-tcp"})
	stdinIsTerminalFn = func() bool { return false }
	stdoutIsTerminalFn = func() bool { return false }
	_ = os.Setenv("TDS_PROXY_LISTEN", ":5200")

	var tcpCalls int32
	runProxyFn = func(ctx context.Context, listen string) error {
		t.Fatalf("did not expect UDP proxy runner in tcp mode")
		return nil
	}
	runProxyP2PFn = func(ctx context.Context, listen string, reg client.DHTRegistry) error {
		t.Fatalf("did not expect p2p proxy runner in centralized mode")
		return nil
	}
	runProxyTCPFn = func(ctx context.Context, listen string) error {
		if listen != ":5200" {
			t.Fatalf("unexpected listen address: %q", listen)
		}
		atomic.AddInt32(&tcpCalls, 1)
		return nil
	}

	main()

	if atomic.LoadInt32(&tcpCalls) != 1 {
		t.Fatalf("expected tcp proxy runner to be called once")
	}
}
