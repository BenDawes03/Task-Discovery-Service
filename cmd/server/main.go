package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rivo/tview"
	"golang.org/x/term"

	"tds/pkg/registry"
	"tds/pkg/transport"
)

// Config
const (
	ListenPort       = 5000
	HeartbeatTimeout = 60 * time.Second
	CleanupInterval  = 10 * time.Second
)

var (
	serverStartTime = time.Now()
	reg             registry.Registry

	app         = tview.NewApplication()
	taskList    = tview.NewList()
	detailTable = tview.NewTable()
	logView     = tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
	searchField = tview.NewInputField().SetLabel(" Filter: ")
)

func writeToLogView(message string) {
	fmt.Fprintf(logView, "%s %s\n", time.Now().Format("[15:04:05]"), message)
	logView.ScrollToEnd()
}

func logEvent(message string) {
	go app.QueueUpdateDraw(func() {
		writeToLogView(message)
	})
}

func updateDashboardData() {
	servicesMap := reg.ListServices()
	go app.QueueUpdateDraw(func() {
		taskList.Clear()
		for task, entries := range servicesMap {
			label := fmt.Sprintf("%s (%d)", task, len(entries))
			t := task
			taskList.AddItem(label, "", 0, func() {
				showTaskDetails(t)
			})
		}
		// Auto-show details for the first task so entries are visible immediately
		for task := range servicesMap {
			showTaskDetails(task)
			break
		}
	})
}

func showTaskDetails(task string) {
	services := reg.ListServices()[task]
	go app.QueueUpdateDraw(func() {
		detailTable.Clear()
		detailTable.SetCell(0, 0, tview.NewTableCell("Address").SetSelectable(false))
		detailTable.SetCell(0, 1, tview.NewTableCell("LastHeartbeat").SetSelectable(false))
		detailTable.SetCell(0, 2, tview.NewTableCell("Queries").SetSelectable(false))
		for i, e := range services {
			detailTable.SetCell(i+1, 0, tview.NewTableCell(e.Address))
			detailTable.SetCell(i+1, 1, tview.NewTableCell(e.LastHeartbeat.Format(time.RFC3339)))
			detailTable.SetCell(i+1, 2, tview.NewTableCell(fmt.Sprintf("%d", e.QueryCount)))
		}
	})
}

func main() {

	// initialize registry
	reg = registry.NewMemoryRegistry()

	// Ask which transport to run (interactive). Default is UDP.
	transportMode := "udp"
	if term.IsTerminal(int(os.Stdin.Fd())) {
		reader := bufio.NewReader(os.Stdin)
		fmt.Fprint(os.Stderr, "Select transport mode (udp/tcp) [udp]: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input != "" {
			transportMode = strings.ToLower(input)
		}
	} else {
		fmt.Fprintln(os.Stderr, "No interactive terminal detected; defaulting to UDP transport")
	}

	fmt.Fprintln(os.Stderr, "starting server (mode=", transportMode, ")")
	switch transportMode {
	case "tcp":
		go func() {
			if err := transport.StartTCPServer(reg, ListenPort, logEvent); err != nil {
				fmt.Fprintln(os.Stderr, "tcp server error:", err)
				os.Exit(1)
			}
		}()
	default:
		go func() {
			if err := transport.StartUDPServer(reg, ListenPort, logEvent); err != nil {
				fmt.Fprintln(os.Stderr, "udp server error:", err)
				os.Exit(1)
			}
		}()
	}
	fmt.Fprintln(os.Stderr, "Server correctly started")
	// Broadcast server info on boot (fire-and-forget)
	go transport.BroadcastServerInfo(ListenPort, HeartbeatTimeout, serverStartTime)
	// Periodic cleanup
	ticker := time.NewTicker(CleanupInterval)
	go func() {
		for range ticker.C {
			removed := reg.Cleanup(HeartbeatTimeout)
			if removed > 0 {
				logEvent(fmt.Sprintf("Cleanup removed %d entries", removed))
			}
			updateDashboardData()
		}
	}()
	fmt.Fprintln(os.Stderr, "Check 1")
	// Periodic UI refresh
	uiTicker := time.NewTicker(2 * time.Second)
	go func() {
		for range uiTicker.C {
			updateDashboardData()
		}
	}()
	fmt.Fprintln(os.Stderr, "Check 2")
	// Build layout
	flex := tview.NewFlex()
	left := tview.NewFlex().SetDirection(tview.FlexRow)
	left.AddItem(searchField, 1, 0, false)
	left.AddItem(taskList, 0, 1, true)
	right := tview.NewFlex().SetDirection(tview.FlexRow)
	right.AddItem(detailTable, 0, 3, false)
	right.AddItem(logView, 0, 1, false)
	flex.AddItem(left, 30, 0, true)
	flex.AddItem(right, 0, 1, false)

	taskList.SetBorder(true).SetTitle("Tasks")
	detailTable.SetBorder(true).SetTitle("Details")
	logView.SetBorder(true).SetTitle("Log")
	fmt.Fprintln(os.Stderr, "Check 3")
	// Start initial data refresh
	updateDashboardData()
	fmt.Fprintln(os.Stderr, "Check 4")
	// Allow forcing the UI even if TTY checks fail.
	forceUI := false
	for _, a := range os.Args[1:] {
		if a == "--force-ui" || a == "-ui" {
			forceUI = true
			break
		}
	}
	fmt.Fprintln(os.Stderr, "Check 5")
	// If none of stdin/stdout/stderr are interactive terminals and not forced,
	// inform the user and keep the background services running (so UDP transport works).
	if !forceUI && !(term.IsTerminal(int(os.Stdin.Fd())) || term.IsTerminal(int(os.Stdout.Fd())) || term.IsTerminal(int(os.Stderr.Fd()))) {
		fmt.Fprintln(os.Stderr, "No interactive terminal detected. Run this program in a real terminal to see the TUI (e.g. 'go run ./cmd/server'). Server will continue running without the UI.")
		select {}
	}
	fmt.Fprintln(os.Stderr, "Interactive terminal detected. Starting TUI...")

	// Start TUI
	fmt.Fprintln(os.Stderr, "Starting TUI (force-ui=", forceUI, ") ...")
	if err := app.SetRoot(flex, true).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "tview run error:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "TUI exited")
}
