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

// askTerminalOptions collects interactive options from the terminal before
// starting any server output. It returns the chosen transport mode ("udp"|"tcp"),
// whether to run the TUI, and whether the TUI was forced via args.
func askTerminalOptions() (string, bool, bool) {
	transportMode := "udp"
	runTUI := false
	forceUI := false

	// Check for --force-ui flag early so we can skip prompting.
	for _, a := range os.Args[1:] {
		if a == "--force-ui" || a == "-ui" {
			forceUI = true
			break
		}
	}

	if term.IsTerminal(int(os.Stdin.Fd())) {
		reader := bufio.NewReader(os.Stdin)
		fmt.Fprint(os.Stderr, "Select transport mode: 1) udp (default) 2) tcp. Enter 1 or 2 [1]: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		switch strings.ToLower(input) {
		case "", "1":
			transportMode = "udp"
		case "2":
			transportMode = "tcp"
		case "udp":
			transportMode = "udp"
		case "tcp":
			transportMode = "tcp"
		default:
			fmt.Fprintln(os.Stderr, "Unrecognized input; defaulting to UDP transport")
			transportMode = "udp"
		}

		// Prompt whether to start the TUI unless forced
		if forceUI {
			runTUI = true
			fmt.Fprintln(os.Stderr, "--force-ui detected; TUI will be started.")
		} else {
			fmt.Fprint(os.Stderr, "Run interactive TUI? [Y/n]: ")
			choice, _ := reader.ReadString('\n')
			choice = strings.TrimSpace(strings.ToLower(choice))
			if choice == "n" || choice == "no" {
				runTUI = false
				fmt.Fprintln(os.Stderr, "User declined TUI. Server will continue running without the UI.")
			} else {
				runTUI = true
			}
		}
	} else {
		fmt.Fprintln(os.Stderr, "No interactive terminal detected; defaulting to UDP transport and no TUI")
		transportMode = "udp"
		runTUI = false
	}

	return transportMode, runTUI, forceUI
}

func main() {

	// initialize registry
	reg = registry.NewMemoryRegistry()

	// Gather terminal options before starting any server output.
	transportMode, runTUI, forceUI := askTerminalOptions()

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
	// Periodic UI refresh
	uiTicker := time.NewTicker(2 * time.Second)
	go func() {
		for range uiTicker.C {
			updateDashboardData()
		}
	}()
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
	// Start initial data refresh
	updateDashboardData()
	// Decide whether to run the TUI based on earlier prompts.
	if !runTUI {
		fmt.Fprintln(os.Stderr, "Server will continue running without the TUI.")
		select {}
	}

	// Start TUI
	fmt.Fprintln(os.Stderr, "Starting TUI (force-ui=", forceUI, ") ...")
	if err := app.SetRoot(flex, true).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "tview run error:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "TUI exited")
}
