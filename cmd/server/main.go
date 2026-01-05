package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rivo/tview"
	"golang.org/x/term"

	"tds/pkg/registry"
	"tds/pkg/store/postgres"
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
	if reg == nil {
		return // Registry not yet initialized
	}
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
	runTUI := true // Default to TUI if interactive
	forceUI := false
	skipPrompts := false

	// Parse command-line flags
	for i, a := range os.Args[1:] {
		switch a {
		case "--force-ui", "-ui":
			forceUI = true
			runTUI = true
			skipPrompts = true
		case "--no-tui":
			runTUI = false
			skipPrompts = true
		case "--tcp":
			transportMode = "tcp"
		case "--udp":
			transportMode = "udp"
		case "--store-url":
			// Skip next arg (it's the URL value)
			i++
		}
		_ = i
	}

	// If not in interactive terminal or prompts skipped, return early
	if skipPrompts || !term.IsTerminal(int(os.Stdin.Fd())) {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			fmt.Fprintln(os.Stderr, "No interactive terminal detected; defaulting to UDP transport and no TUI")
			runTUI = false
		}
		return transportMode, runTUI, forceUI
	}

	// Interactive prompts
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

	// Prompt whether to start the TUI
	fmt.Fprint(os.Stderr, "Run interactive TUI? [Y/n]: ")
	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(strings.ToLower(choice))
	if choice == "n" || choice == "no" {
		runTUI = false
		fmt.Fprintln(os.Stderr, "User declined TUI. Server will continue running without the UI.")
	} else {
		runTUI = true
	}

	return transportMode, runTUI, forceUI
}

func main() {

	// Gather terminal options before starting any server output.
	transportMode, runTUI, _ := askTerminalOptions()

	// Check for --store-url or DATABASE_URL for persistence.
	storeURL := os.Getenv("DATABASE_URL")

	// Check for --store-url flag
	for i, a := range os.Args[1:] {
		if a == "--store-url" && i+1 < len(os.Args)-1 {
			storeURL = os.Args[i+2]
			break
		}
	}

	if storeURL != "" {
		// Initialize store-backed registry
		s, err := postgres.NewPostgresStore(storeURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to initialize postgres store: %v\n", err)
			os.Exit(1)
		}

		// Run migrations
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := s.Migrate(ctx); err != nil {
			cancel()
			fmt.Fprintf(os.Stderr, "failed to run migrations: %v\n", err)
			s.Close()
			os.Exit(1)
		}
		cancel()

		storeReg := registry.NewStoreBackedRegistry(s)

		// Warm the in-memory cache from the database on startup
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		if err := storeReg.WarmCacheFromDB(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to warm cache from db: %v\n", err)
		} else {
			fmt.Fprintln(os.Stderr, "cache warmed from database")
		}
		cancel()

		reg = storeReg
		fmt.Fprintln(os.Stderr, "using postgres persistent store with in-memory cache")
	} else {
		// Fall back to in-memory registry
		reg = registry.NewMemoryRegistry()
		fmt.Fprintln(os.Stderr, "using in-memory registry (no persistence)")
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

	// Decide whether to run the TUI based on earlier prompts.
	if !runTUI {
		fmt.Fprintln(os.Stderr, "Server will continue running without the TUI.")
		// For headless mode, just run cleanup, no UI refresh needed
		ticker := time.NewTicker(CleanupInterval)
		go func() {
			for range ticker.C {
				removed := reg.Cleanup(HeartbeatTimeout)
				if removed > 0 {
					logEvent(fmt.Sprintf("Cleanup removed %d entries", removed))
				}
			}
		}()
		select {}
	}

	// TUI mode: start tickers and UI refresh
	// Periodic cleanup
	ticker := time.NewTicker(CleanupInterval)
	go func() {
		for range ticker.C {
			removed := reg.Cleanup(HeartbeatTimeout)
			logEvent(fmt.Sprintf("Cleanup ran: removed %d stale entries", removed))
			
			// Warm cache from DB to ensure UI reflects deleted entries
			if storeReg, ok := reg.(*registry.StoreBackedRegistry); ok {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = storeReg.WarmCacheFromDB(ctx)
				cancel()
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

	// Start initial data refresh AFTER UI is ready
	go func() {
		time.Sleep(100 * time.Millisecond)
		updateDashboardData()
	}()

	// Start TUI
	if err := app.SetRoot(flex, true).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "tview run error:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "TUI exited")
}
