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

func newP2PDashboard(registry *dht.DHTRegistry, proxyListen, dhtListen string, bootstrapNodes []string, kClosest int) *p2pDashboard {
	summaryView := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(true)
	summaryView.SetBorder(true).SetTitle(" Node summary ")

	peersTable := tview.NewTable().SetBorders(false).SetFixed(1, 0)
	peersTable.SetBorder(true).SetTitle(" Known peers ")

	tasksTable := tview.NewTable().SetBorders(false).SetFixed(1, 0)
	tasksTable.SetBorder(true).SetTitle(" Stored tasks ")

	logView := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(false)
	logView.SetBorder(true).SetTitle(" Recent log messages ")

	footerView := tview.NewTextView().SetDynamicColors(true)
	footerView.SetBorder(true).SetTitle(" Controls ")

	body := tview.NewFlex().
		AddItem(peersTable, 0, 2, false).
		AddItem(tasksTable, 0, 3, false)

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(summaryView, 6, 0, false).
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

	summary := fmt.Sprintf(
		"[yellow]Mode:[white] P2P    [yellow]Uptime:[white] %s    [yellow]Replication (k):[white] %d\n"+
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
	d.summaryView.SetText(summary)

	d.renderPeers(peers)
	d.renderTasks(tasks, snapshot.StoredTasks)
	d.renderLogs()
	d.footerView.SetText("[yellow]q[white] quit    [yellow]c[white] clear logs    [yellow]r[white] refresh")
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
	d.tasksTable.SetCell(0, 1, headerCell("Addresses"))
	d.tasksTable.SetCell(0, 2, headerCell("Replicas"))

	if len(taskNames) == 0 {
		d.tasksTable.SetCell(1, 0, plainCell("No tasks stored on this node"))
		d.tasksTable.SetCell(1, 1, plainCell(""))
		d.tasksTable.SetCell(1, 2, plainCell("0"))
		return
	}

	for i, task := range taskNames {
		addrs := append([]string(nil), stored[task]...)
		sort.Strings(addrs)
		d.tasksTable.SetCell(i+1, 0, plainCell(task))
		d.tasksTable.SetCell(i+1, 1, plainCell(strings.Join(addrs, ", ")))
		d.tasksTable.SetCell(i+1, 2, plainCell(fmt.Sprintf("%d", len(addrs))))
	}
}

func (d *p2pDashboard) renderLogs() {
	d.mu.Lock()
	lines := append([]string(nil), d.logLines...)
	d.mu.Unlock()

	d.logView.Clear()
	if len(lines) == 0 {
		fmt.Fprintln(d.logView, "Waiting for log messages...")
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
