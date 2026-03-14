package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"tds/pkg/client"
	"tds/pkg/dht"
)

type p2pDashboard struct {
	app            *tview.Application
	registry       *dht.DHTRegistry
	proxyListen    string
	dhtListen      string
	bootstrapNodes []string
	kClosest       int
	simplified     bool
	startedAt      time.Time

	summaryView *tview.TextView
	peersTable  *tview.Table
	tasksTable  *tview.Table
	logView     *tview.TextView
	footerView  *tview.TextView

	mu          sync.Mutex
	logLines    []string
	partialLog  string
	maxLogLines int

	stopOnce sync.Once
	stopCh   chan struct{}
	shutdown func()

	proxyErrMu sync.Mutex
	proxyErr   error
}

func newP2PDashboard(registry *dht.DHTRegistry, proxyListen, dhtListen string, bootstrapNodes []string, kClosest int, simplified bool) *p2pDashboard {
	summaryView := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(true)
	summaryTitle := " Node summary "
	if simplified {
		summaryTitle = " DHT quick view "
	}
	summaryView.SetBorder(true).SetTitle(summaryTitle)

	peersTable := tview.NewTable().SetBorders(false).SetFixed(1, 0)
	peersTitle := " Known peers "
	if simplified {
		peersTitle = " Peers "
	}
	peersTable.SetBorder(true).SetTitle(peersTitle)

	tasksTable := tview.NewTable().SetBorders(false).SetFixed(1, 0)
	tasksTitle := " Stored tasks "
	if simplified {
		tasksTitle = " Task replicas "
	}
	tasksTable.SetBorder(true).SetTitle(tasksTitle)

	logView := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(simplified)
	logTitle := " Recent log messages "
	if simplified {
		logTitle = " DHT event feed "
	}
	logView.SetBorder(true).SetTitle(logTitle)

	footerView := tview.NewTextView().SetDynamicColors(true)
	footerView.SetBorder(true).SetTitle(" Controls ")

	var body *tview.Flex
	if simplified {
		body = tview.NewFlex().
			AddItem(tasksTable, 0, 1, false)
	} else {
		body = tview.NewFlex().
			AddItem(peersTable, 0, 2, false).
			AddItem(tasksTable, 0, 3, false)
	}

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(summaryView, 7, 0, false).
		AddItem(body, 0, 2, false).
		AddItem(logView, 0, 3, false).
		AddItem(footerView, 3, 0, false)

	dashboard := &p2pDashboard{
		app:            tview.NewApplication(),
		registry:       registry,
		proxyListen:    proxyListen,
		dhtListen:      dhtListen,
		bootstrapNodes: append([]string(nil), bootstrapNodes...),
		kClosest:       kClosest,
		simplified:     simplified,
		startedAt:      time.Now(),
		summaryView:    summaryView,
		peersTable:     peersTable,
		tasksTable:     tasksTable,
		logView:        logView,
		footerView:     footerView,
		maxLogLines:    250,
		stopCh:         make(chan struct{}),
	}

	dashboard.app.SetRoot(layout, true)
	dashboard.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyCtrlC:
			go dashboard.shutdownAndStop(nil)
			return nil
		}

		switch strings.ToLower(string(event.Rune())) {
		case "q":
			go dashboard.shutdownAndStop(nil)
			return nil
		case "c":
			dashboard.clearLogs()
			dashboard.render()
			return nil
		case "r":
			dashboard.render()
			return nil
		}
		return event
	})

	dashboard.render()
	return dashboard
}

func (d *p2pDashboard) Write(p []byte) (int, error) {
	d.mu.Lock()
	text := d.partialLog + strings.ReplaceAll(string(p), "\r\n", "\n")
	parts := strings.Split(text, "\n")
	d.partialLog = parts[len(parts)-1]
	for _, line := range parts[:len(parts)-1] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if d.simplified {
			line = simplifyLogLine(line)
			if line == "" {
				continue
			}
		}
		d.logLines = append(d.logLines, line)
	}
	if len(d.logLines) > d.maxLogLines {
		d.logLines = d.logLines[len(d.logLines)-d.maxLogLines:]
	}
	d.mu.Unlock()
	return len(p), nil
}

func (d *p2pDashboard) Run(proxyExited <-chan struct{}, getProxyErr func() error, shutdown func()) error {
	d.shutdown = shutdown

	go d.refreshLoop()
	go func() {
		select {
		case <-proxyExited:
			err := getProxyErr()
			if err != nil {
				d.setProxyErr(err)
				fmt.Fprintf(d, "proxy exited: %v\n", err)
			} else {
				fmt.Fprintln(d, "proxy stopped")
			}
			shutdown()
			d.stop(err)
		case <-d.stopCh:
		}
	}()

	err := d.app.Run()
	d.stop(nil)
	if err != nil {
		return err
	}
	return d.getProxyErr()
}

func (d *p2pDashboard) shutdownAndStop(err error) {
	if d.shutdown != nil {
		d.shutdown()
	}
	d.stop(err)
}

func (d *p2pDashboard) refreshLoop() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			d.queueRefresh()
		case <-d.stopCh:
			return
		}
	}
}

func (d *p2pDashboard) queueRefresh() {
	select {
	case <-d.stopCh:
		return
	default:
	}

	d.app.QueueUpdateDraw(func() {
		d.render()
	})
}

func (d *p2pDashboard) render() {
	snapshot := d.registry.Snapshot()
	clientRegs, clientQueries, clientErrs := client.Stats()
	dhtRegs, dhtQueries, dhtErrs := d.registry.Stats()

	peers := append([]string(nil), snapshot.Peers...)
	sort.Strings(peers)

	tasks := make([]string, 0, len(snapshot.StoredTasks))
	for task := range snapshot.StoredTasks {
		tasks = append(tasks, task)
	}
	sort.Strings(tasks)

	bootstraps := "none"
	if len(d.bootstrapNodes) > 0 {
		sortedBootstraps := append([]string(nil), d.bootstrapNodes...)
		sort.Strings(sortedBootstraps)
		bootstraps = strings.Join(sortedBootstraps, ", ")
	}

	var summary string
	if d.simplified {
		summary = fmt.Sprintf(
			"[green::b]DHT VIEW[white:-:-]    [yellow]Uptime:[white] %s    [yellow]Replication (k):[white] %d\n"+
				"[yellow]Proxy:[white] %s    [yellow]DHT node:[white] %s\n"+
				"[yellow]Peers:[white] %d    [yellow]Tasks on this node:[white] %d    [yellow]Bootstraps:[white] %s\n"+
				"[yellow]Operations:[white] reg=%d query=%d err=%d    [yellow]DHT ops:[white] reg=%d query=%d err=%d",
			time.Since(d.startedAt).Round(time.Second),
			d.kClosest,
			d.proxyListen,
			snapshot.Address,
			len(peers),
			snapshot.StorageSize,
			bootstraps,
			clientRegs,
			clientQueries,
			clientErrs,
			dhtRegs,
			dhtQueries,
			dhtErrs,
		)
	} else {
		summary = fmt.Sprintf(
			"[red::b]DHT VIEW[white:-:-]    [yellow]Uptime:[white] %s    [yellow]Replication (k):[white] %d\n"+
				"[yellow]Proxy listen:[white] %s    [yellow]DHT listen:[white] %s\n"+
				"[yellow]Node ID:[white] %s    [yellow]Ring size:[white] %d    [yellow]Peers:[white] %d\n"+
				"[yellow]Local storage:[white] %d task(s)    [yellow]Bootstraps:[white] %s\n"+
				"[yellow]Proxy stats:[white] registers=%d queries=%d errors=%d    [yellow]DHT stats:[white] registers=%d queries=%d errors=%d",
			time.Since(d.startedAt).Round(time.Second),
			d.kClosest,
			d.proxyListen,
			snapshot.Address,
			snapshot.NodeID,
			snapshot.RingSize,
			len(peers),
			snapshot.StorageSize,
			bootstraps,
			clientRegs,
			clientQueries,
			clientErrs,
			dhtRegs,
			dhtQueries,
			dhtErrs,
		)
	}
	d.summaryView.SetText(summary)

	if !d.simplified {
		d.renderPeers(peers)
	}
	d.renderTasks(tasks, snapshot.StoredTasks)
	d.renderLogs()
	if d.simplified {
		d.footerView.SetText("[green::b]UI MODE: SIMPLIFIED[white:-:-]    [yellow]q[white] quit    [yellow]c[white] clear events    [yellow]r[white] refresh")
	} else {
		d.footerView.SetText("[red::b]UI MODE: STANDARD[white:-:-]    [yellow]q[white] quit    [yellow]c[white] clear logs    [yellow]r[white] refresh")
	}
}

func (d *p2pDashboard) renderPeers(peers []string) {
	d.peersTable.Clear()
	d.peersTable.SetCell(0, 0, headerCell("#"))
	d.peersTable.SetCell(0, 1, headerCell("Address"))

	if len(peers) == 0 {
		d.peersTable.SetCell(1, 0, plainCell("-"))
		d.peersTable.SetCell(1, 1, plainCell("No peers discovered yet"))
		return
	}

	for i, peer := range peers {
		d.peersTable.SetCell(i+1, 0, plainCell(fmt.Sprintf("%d", i+1)))
		d.peersTable.SetCell(i+1, 1, plainCell(peer))
	}
}

func (d *p2pDashboard) renderTasks(taskNames []string, stored map[string][]string) {
	d.tasksTable.Clear()
	d.tasksTable.SetCell(0, 0, headerCell("Task"))
	if d.simplified {
		d.tasksTable.SetCell(0, 1, headerCell("Replicas"))
	} else {
		d.tasksTable.SetCell(0, 1, headerCell("Addresses"))
		d.tasksTable.SetCell(0, 2, headerCell("Replicas"))
	}

	if len(taskNames) == 0 {
		d.tasksTable.SetCell(1, 0, plainCell("No tasks stored on this node"))
		d.tasksTable.SetCell(1, 1, plainCell("0"))
		if !d.simplified {
			d.tasksTable.SetCell(1, 2, plainCell("0"))
		}
		return
	}

	for i, task := range taskNames {
		addrs := append([]string(nil), stored[task]...)
		sort.Strings(addrs)
		d.tasksTable.SetCell(i+1, 0, plainCell(task))
		if d.simplified {
			d.tasksTable.SetCell(i+1, 1, plainCell(fmt.Sprintf("%d", len(addrs))))
		} else {
			d.tasksTable.SetCell(i+1, 1, plainCell(strings.Join(addrs, ", ")))
			d.tasksTable.SetCell(i+1, 2, plainCell(fmt.Sprintf("%d", len(addrs))))
		}
	}
}

func (d *p2pDashboard) renderLogs() {
	d.mu.Lock()
	lines := append([]string(nil), d.logLines...)
	d.mu.Unlock()

	d.logView.Clear()
	if len(lines) == 0 {
		if d.simplified {
			fmt.Fprintln(d.logView, "Waiting for DHT events...")
		} else {
			fmt.Fprintln(d.logView, "Waiting for log messages...")
		}
		return
	}
	for _, line := range lines {
		fmt.Fprintln(d.logView, line)
	}
	d.logView.ScrollToEnd()
}

func (d *p2pDashboard) clearLogs() {
	d.mu.Lock()
	d.logLines = nil
	d.partialLog = ""
	d.mu.Unlock()
}

func (d *p2pDashboard) stop(err error) {
	d.stopOnce.Do(func() {
		if err != nil {
			d.setProxyErr(err)
		}
		close(d.stopCh)
		d.app.Stop()
	})
}

func (d *p2pDashboard) setProxyErr(err error) {
	d.proxyErrMu.Lock()
	defer d.proxyErrMu.Unlock()
	d.proxyErr = err
}

func (d *p2pDashboard) getProxyErr() error {
	d.proxyErrMu.Lock()
	defer d.proxyErrMu.Unlock()
	return d.proxyErr
}

func headerCell(text string) *tview.TableCell {
	return tview.NewTableCell(" " + text + " ").
		SetSelectable(false).
		SetTextColor(tcell.ColorYellow).
		SetAttributes(tcell.AttrBold)
}

func plainCell(text string) *tview.TableCell {
	return tview.NewTableCell(" " + text + " ").SetSelectable(false)
}

func simplifyLogLine(line string) string {
	msg := trimLogEnvelope(line)
	if msg == "" {
		return ""
	}

	switch {
	case strings.HasPrefix(msg, "P2P REGISTER "):
		rest := strings.TrimPrefix(msg, "P2P REGISTER ")
		rest = trimSourceSuffix(rest)
		return "REGISTER request: " + rest
	case strings.HasPrefix(msg, "P2P QUERY "):
		rest := strings.TrimPrefix(msg, "P2P QUERY ")
		rest = trimSourceSuffix(rest)
		return "QUERY request: " + rest
	case strings.HasPrefix(msg, "registering "):
		return "DHT store started: " + strings.TrimPrefix(msg, "registering ")
	case strings.HasPrefix(msg, "querying all for "):
		return "DHT query(all): " + strings.TrimPrefix(msg, "querying all for ")
	case strings.HasPrefix(msg, "querying "):
		return "DHT query: " + strings.TrimPrefix(msg, "querying ")
	case strings.HasPrefix(msg, "DHT listening on "):
		return "DHT node online"
	case strings.HasPrefix(msg, "joining via bootstrap node "):
		return "Bootstrapping via " + strings.TrimPrefix(msg, "joining via bootstrap node ")
	case strings.HasPrefix(msg, "join error with "):
		return "Bootstrap failed: " + strings.TrimPrefix(msg, "join error with ")
	case strings.HasPrefix(msg, "received ") && strings.Contains(msg, " peers from "):
		return "Peer list updated: " + msg
	case strings.HasPrefix(msg, "joined network, "):
		return "Network join complete: " + msg
	case strings.HasPrefix(msg, "stored locally"):
		return "Stored locally: " + shortenStoreMessage(msg)
	case strings.HasPrefix(msg, "stored on "):
		return "Stored on peer: " + shortenStoreMessage(msg)
	case strings.HasPrefix(msg, "store complete: "):
		return "Replication done: " + strings.TrimPrefix(msg, "store complete: ")
	case strings.HasPrefix(msg, "found locally: "):
		return "Query result(local): " + strings.TrimPrefix(msg, "found locally: ")
	case strings.HasPrefix(msg, "found on "):
		return "Query result(peer): " + strings.TrimPrefix(msg, "found on ")
	case strings.HasPrefix(msg, "not found on any k-closest node: "):
		return "Query miss: " + strings.TrimPrefix(msg, "not found on any k-closest node: ")
	case strings.HasPrefix(msg, "queuing closer node "):
		return "Retrying with closer peer"
	case strings.HasPrefix(msg, "removed unreachable peer "):
		return "Peer dropped: " + strings.TrimPrefix(msg, "removed unreachable peer ")
	case strings.HasPrefix(msg, "proxy listening on "):
		return "Proxy online: " + strings.TrimPrefix(msg, "proxy listening on ")
	case strings.HasPrefix(msg, "proxy stopped"):
		return "Proxy stopped"
	}

	// In simplified mode, drop noisy unclassified logs.
	return ""
}

func trimLogEnvelope(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}

	if strings.HasPrefix(line, "[") {
		if idx := strings.Index(line, "] "); idx != -1 {
			line = strings.TrimSpace(line[idx+2:])
		}
	}

	if len(line) >= 20 {
		candidate := line[:19]
		if _, err := time.Parse("2006/01/02 15:04:05", candidate); err == nil {
			line = strings.TrimSpace(line[20:])
		}
	}

	return line
}

func trimSourceSuffix(s string) string {
	if idx := strings.Index(s, " (from "); idx != -1 {
		return strings.TrimSpace(s[:idx])
	}
	return strings.TrimSpace(s)
}

func shortenStoreMessage(msg string) string {
	if idx := strings.Index(msg, ": "); idx != -1 {
		return strings.TrimSpace(msg[idx+2:])
	}
	return msg
}
