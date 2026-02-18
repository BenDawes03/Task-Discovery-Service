package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"golang.org/x/term"

	"tds/pkg/firewall"
	"tds/pkg/registry"
	"tds/pkg/store/postgres"
	"tds/pkg/transport"
)

// Config
var (
	// Server configuration
	listenPort   int
	cacheMaxSize int
	logDir       string

	// Configurable timeouts
	heartbeatTimeout time.Duration
	cleanupInterval  time.Duration

	// TLS configuration
	useTLS          bool
	tlsCertFile     string
	tlsKeyFile      string
	tlsClientCAFile string

	// Database configuration
	storeURL string

	// UI configuration
	forceUI bool
	noUI    bool

	serverStartTime = time.Now()
	reg             registry.Registry

	app         = tview.NewApplication()
	taskList    = tview.NewList()
	detailTable = tview.NewTable()
	logView     = tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
	searchField = tview.NewInputField().SetLabel(" Filter: ")

	// Filter state
	currentFilter = ""
	currentSelectedTask = "" // Track which task is currently selected

	// File logging
	logFile        *os.File
	logWriter      io.Writer
	originalStderr *os.File // Store original stderr for critical error display

	// UI update coordination
	logMessageChan   = make(chan string, 1000) // Buffered channel for log messages
	updateDashboard  = make(chan struct{}, 1)  // Signal to update dashboard
	uiUpdateInterval = 250 * time.Millisecond  // Batch UI updates (increased for stability)
	runningTUI       = false                   // Track if TUI is active
	logLines         []string                  // Track log lines for bounded display
	maxLogLines      = 100                     // Maximum log lines to keep
	uiMutex          sync.Mutex                // Serialize all UI operations
)

func writeToLogView(message string) {
	uiMutex.Lock()
	defer uiMutex.Unlock()

	// Add to bounded log buffer
	logLines = append(logLines, fmt.Sprintf("%s %s", time.Now().Format("[15:04:05]"), message))
	if len(logLines) > maxLogLines {
		logLines = logLines[len(logLines)-maxLogLines:]
	}

	// Redraw entire log view with bounded content
	logView.Clear()
	for _, line := range logLines {
		fmt.Fprintln(logView, line)
	}
	logView.ScrollToEnd()
}

func logEvent(message string) {
	// Always write to log file immediately
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	logLine := fmt.Sprintf("[%s] %s\n", timestamp, message)
	if logWriter != nil {
		logWriter.Write([]byte(logLine))
	}

	// Before TUI starts, also print to stderr for visibility
	if !runningTUI {
		fmt.Fprint(os.Stderr, logLine)
	}

	// Queue for TUI update (non-blocking, rate-limited by coordinator)
	// Filter out high-frequency QUERY operations to prevent TUI spam
	if runningTUI && !strings.HasPrefix(message, "QUERY ") {
		select {
		case logMessageChan <- message:
			// Message queued successfully
		default:
			// Channel full, drop message (it's already in log file)
		}
	}
}

// fatalError handles critical errors during startup
// It ensures the error is visible even when TUI mode redirects stderr
func fatalError(message string) {
	logEvent(fmt.Sprintf("FATAL: %s", message))
	
	// If TUI mode, write to original stderr so user can see the error
	if runningTUI && originalStderr != nil {
		fmt.Fprintf(originalStderr, "\nFATAL ERROR: %s\n", message)
		fmt.Fprintln(originalStderr, "Check the log file for more details.")
	} else if !runningTUI {
		fmt.Fprintf(os.Stderr, "FATAL: %s\n", message)
	}
	
	// Give time for log message to be written to file
	time.Sleep(100 * time.Millisecond)
	os.Exit(1)
}

// uiUpdateCoordinator is the single goroutine that handles all UI updates
// This prevents race conditions and rendering corruption
func uiUpdateCoordinator() {
	logBatchTicker := time.NewTicker(uiUpdateInterval)
	defer logBatchTicker.Stop()

	var pendingLogs []string
	const maxBatchSize = 50 // Max log lines to show per batch

	for {
		select {
		case msg := <-logMessageChan:
			// Collect log messages
			pendingLogs = append(pendingLogs, msg)
			// Keep only recent logs to avoid memory buildup
			if len(pendingLogs) > maxBatchSize*2 {
				pendingLogs = pendingLogs[len(pendingLogs)-maxBatchSize:]
			}

		case <-logBatchTicker.C:
			// Batch update logs to UI
			if len(pendingLogs) > 0 {
				logsToWrite := pendingLogs
				if len(logsToWrite) > maxBatchSize {
					logsToWrite = logsToWrite[len(logsToWrite)-maxBatchSize:]
				}
				app.QueueUpdateDraw(func() {
					for _, msg := range logsToWrite {
						writeToLogView(msg)
					}
				})
				pendingLogs = nil
			}

		case <-updateDashboard:
			// Update dashboard data
			if reg == nil {
				continue
			}
			servicesMap := reg.ListServices()
			// Make a copy for the closure to avoid race conditions
			servicesMapCopy := make(map[string][]registry.ServiceEntry)
			for k, v := range servicesMap {
				// Deep copy the slice
				servicesMapCopy[k] = make([]registry.ServiceEntry, len(v))
				copy(servicesMapCopy[k], v)
			}

			app.QueueUpdateDraw(func() {
				uiMutex.Lock()
				defer uiMutex.Unlock()

				// Update task list with filter applied
				taskList.Clear()
				var firstTask string
				var taskToShow string // Track which task to display details for
				filterLower := strings.ToLower(currentFilter)
				
				for task, entries := range servicesMapCopy {
					// Apply filter: show only tasks that contain the filter string (case-insensitive)
					if filterLower != "" && !strings.Contains(strings.ToLower(task), filterLower) {
						continue
					}
					
					label := fmt.Sprintf("%s (%d)", task, len(entries))
					t := task
					if firstTask == "" {
						firstTask = task
					}
					// Check if this is the currently selected task
					if task == currentSelectedTask {
						taskToShow = task
					}
					taskList.AddItem(label, "", 0, func() {
						// Update selected task and refresh details
						selectedTask := t
						currentSelectedTask = selectedTask
						go func() {
							services := reg.ListServices()[selectedTask]
							app.QueueUpdateDraw(func() {
								updateDetailsTable(services)
							})
						}()
					})
				}
				// If previously selected task still exists, show it; otherwise show first task
				if taskToShow == "" {
					taskToShow = firstTask
					currentSelectedTask = firstTask
				}
				
				// Always update the details table to reflect current state
				if taskToShow != "" {
					if services, ok := servicesMapCopy[taskToShow]; ok {
						// Temporarily unlock for the nested call
						uiMutex.Unlock()
						updateDetailsTable(services)
						uiMutex.Lock()
					} else {
						// Task exists but has no services - clear the table
						uiMutex.Unlock()
						updateDetailsTable(nil)
						uiMutex.Lock()
					}
				} else {
					// No tasks at all - clear the details table
					uiMutex.Unlock()
					updateDetailsTable(nil)
					uiMutex.Lock()
				}
			})
		}
	}
}

// updateDetailsTable updates the details table (must be called within QueueUpdateDraw)
func updateDetailsTable(services []registry.ServiceEntry) {
	uiMutex.Lock()
	defer uiMutex.Unlock()

	detailTable.Clear()
	detailTable.SetCell(0, 0, tview.NewTableCell(" Address ").
		SetSelectable(false).
		SetExpansion(1).
		SetTextColor(tcell.ColorYellow).
		SetAttributes(tcell.AttrBold))
	detailTable.SetCell(0, 1, tview.NewTableCell(" LastHeartbeat ").
		SetSelectable(false).
		SetExpansion(1).
		SetTextColor(tcell.ColorYellow).
		SetAttributes(tcell.AttrBold))
	detailTable.SetCell(0, 2, tview.NewTableCell(" Queries ").
		SetSelectable(false).
		SetExpansion(1).
		SetTextColor(tcell.ColorYellow).
		SetAttributes(tcell.AttrBold))
	for i, e := range services {
		detailTable.SetCell(i+1, 0, tview.NewTableCell(" "+e.Address+" ").SetExpansion(1))
		detailTable.SetCell(i+1, 1, tview.NewTableCell(" "+e.LastHeartbeat.Format("2006-01-02 15:04:05")+" ").SetExpansion(1))
		detailTable.SetCell(i+1, 2, tview.NewTableCell(" "+fmt.Sprintf("%d", e.QueryCount)+" ").SetExpansion(1))
	}
}

// requestDashboardUpdate signals the coordinator to update the dashboard
func requestDashboardUpdate() {
	if runningTUI {
		select {
		case updateDashboard <- struct{}{}:
			// Signal sent
		default:
			// Already pending, skip
		}
	}
}

// initLogFile creates or appends to a log file in the logs/ directory
func initLogFile() error {
	// Create logs directory if it doesn't exist
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create logs directory: %w", err)
	}

	// Create log filename with timestamp
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	logPath := filepath.Join(logDir, fmt.Sprintf("server_%s.log", timestamp))

	// Open log file
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}

	logFile = f
	logWriter = f // Write to file only (stderr would corrupt TUI)
	log.SetOutput(logWriter)

	fmt.Fprintf(logWriter, "[%s] ========== Server Started ==========\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(logWriter, "[%s] Log file: %s\n", time.Now().Format("2006-01-02 15:04:05"), logPath)

	return nil
}

// askTerminalOptions collects interactive options from the terminal before
// starting any server output. It returns the chosen transport mode ("udp"|"tcp"|"tls"),
// whether to run the TUI, and whether the TUI was forced via args.
func askTerminalOptions() (string, bool, bool) {
	transportMode := "udp"
	runTUI := true // Default to TUI if interactive
	skipPrompts := false

	// Check if UI mode was forced via flags
	if forceUI {
		runTUI = true
		skipPrompts = true
	}
	if noUI {
		runTUI = false
		skipPrompts = true
	}

	// Check if transport mode was set via flags
	transportFlagSet := false
	for _, a := range os.Args[1:] {
		if a == "--tcp" || a == "--udp" || a == "--tls" {
			transportFlagSet = true
			break
		}
	}

	// If not in interactive terminal or prompts skipped, return early
	if skipPrompts || !term.IsTerminal(int(os.Stdin.Fd())) {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			fmt.Fprintln(os.Stderr, "No interactive terminal detected; defaulting to no TUI")
			runTUI = false
		}
		// Use flag value or default
		if useTLS {
			transportMode = "tls"
		}
		return transportMode, runTUI, forceUI
	}

	// Interactive prompts
	reader := bufio.NewReader(os.Stdin)
	
	// Transport mode prompt (skip if flag was set)
	if !transportFlagSet {
		fmt.Fprint(os.Stderr, "Select transport mode: 1) udp (default) 2) tcp 3) tls. Enter 1, 2, or 3 [1]: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		switch strings.ToLower(input) {
		case "", "1":
			transportMode = "udp"
		case "2":
			transportMode = "tcp"
		case "3":
			transportMode = "tls"
			useTLS = true
		case "udp":
			transportMode = "udp"
		case "tcp":
			transportMode = "tcp"
		case "tls":
			transportMode = "tls"
			useTLS = true
		default:
			fmt.Fprintln(os.Stderr, "Unrecognized input; defaulting to UDP transport")
			transportMode = "udp"
		}
	} else {
		// Use flag value
		if useTLS {
			transportMode = "tls"
		}
	}

	// Database persistence prompt (skip if --store-url flag was set)
	if storeURL == "" {
		fmt.Fprint(os.Stderr, "Enable database persistence? [y/N]: ")
		dbChoice, _ := reader.ReadString('\n')
		dbChoice = strings.TrimSpace(strings.ToLower(dbChoice))
		if dbChoice == "y" || dbChoice == "yes" {
			fmt.Fprint(os.Stderr, "Enter database URL (e.g., postgresql://user:password@localhost:5432/trs?sslmode=disable): ")
			dbURL, _ := reader.ReadString('\n')
			storeURL = strings.TrimSpace(dbURL)
			if storeURL != "" {
				fmt.Fprintln(os.Stderr, "Database persistence enabled")
				
				// Cache size prompt (only if database is enabled and not set via flag)
				if cacheMaxSize == 100 { // Default value, not set via flag
					fmt.Fprint(os.Stderr, "Enter cache max size (0 for unlimited) [100]: ")
					cacheInput, _ := reader.ReadString('\n')
					cacheInput = strings.TrimSpace(cacheInput)
					if cacheInput != "" {
						fmt.Sscanf(cacheInput, "%d", &cacheMaxSize)
					}
				}
			}
		} else {
			fmt.Fprintln(os.Stderr, "Using in-memory registry (no persistence)")
		}
	}

	// Prompt whether to start the TUI (skip if flag was set)
	if !forceUI && !noUI {
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

	return transportMode, runTUI, forceUI
}

func main() {
	// Parse command-line flags
	var firewallRulesPath string

	flag.IntVar(&listenPort, "port", 5000, "Port to listen on")
	flag.DurationVar(&heartbeatTimeout, "heartbeat-timeout", 60*time.Second, "Timeout for service heartbeats")
	flag.DurationVar(&cleanupInterval, "cleanup-interval", 10*time.Second, "Interval for cleanup of stale entries")
	flag.StringVar(&firewallRulesPath, "firewall-rules", "", "Path to firewall rules file (optional)")

	// Transport mode flags
	tcpMode := flag.Bool("tcp", false, "Use TCP transport")
	udpMode := flag.Bool("udp", false, "Use UDP transport (default)")
	tlsMode := flag.Bool("tls", false, "Use TLS transport with mutual authentication")

	// TLS configuration flags
	flag.StringVar(&tlsCertFile, "tls-cert", "certs/server.crt", "Server TLS certificate file")
	flag.StringVar(&tlsKeyFile, "tls-key", "certs/server.key", "Server TLS private key file")
	flag.StringVar(&tlsClientCAFile, "tls-client-ca", "certs/ca.crt", "CA certificate to verify client certificates")

	// Database configuration flags
	flag.StringVar(&storeURL, "store-url", "", "Database URL for persistent storage (e.g., postgresql://user:password@localhost:5432/dbname)")
	flag.IntVar(&cacheMaxSize, "cache-max-size", 100, "Maximum number of tasks to keep in cache (0 = unlimited)")

	// Logging configuration flags
	flag.StringVar(&logDir, "log-dir", "logs", "Directory for log files")

	// UI configuration flags
	flag.BoolVar(&forceUI, "force-ui", false, "Force TUI mode without prompting")
	flag.BoolVar(&forceUI, "ui", false, "Alias for --force-ui")
	flag.BoolVar(&noUI, "no-ui", false, "Run in headless mode without TUI")
	flag.BoolVar(&noUI, "no-tui", false, "Alias for --no-ui")

	flag.Parse()

	// Process transport mode flags
	if *tlsMode {
		useTLS = true
	}
	if *tcpMode && !*tlsMode {
		// Explicitly TCP without TLS
		useTLS = false
	}
	if *udpMode {
		useTLS = false
	}

	// Initialize file logging
	if err := initLogFile(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to initialize log file: %v\n", err)
		logWriter = os.Stderr // Fallback to stderr only
	}
	defer func() {
		if logFile != nil {
			logFile.Close()
		}
	}()

	// Gather terminal options before starting any server output.
	transportMode, runTUI, _ := askTerminalOptions()

	// If running TUI, redirect stderr to discard to prevent corruption
	if runTUI {
		// Set runningTUI immediately so logEvent() queues messages for TUI
		runningTUI = true
		// Save original stderr for restoration on exit
		originalStderr = os.Stderr
		// Open actual /dev/null and redirect stderr to it
		devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err == nil {
			os.Stderr = devNull
			defer devNull.Close()
		}
		defer func() {
			// Restore stderr on exit
			os.Stderr = originalStderr
		}()

		// CRITICAL: Start the UI update coordinator NOW, before any database initialization
		// This ensures log messages from database setup are properly queued and processed
		go uiUpdateCoordinator()
	}

	// Load firewall rules if specified
	var fw *firewall.Firewall
	if firewallRulesPath != "" {
		var err error
		fw, err = firewall.LoadFromFile(firewallRulesPath)
		if err != nil {
			fatalError(fmt.Sprintf("failed to load firewall rules: %v", err))
		}
		logEvent(fmt.Sprintf("Loaded %d firewall rules from %s", fw.RuleCount(), firewallRulesPath))
		if !runTUI {
			fmt.Fprintf(os.Stderr, "loaded %d firewall rules from %s\n", fw.RuleCount(), firewallRulesPath)
		}
	} else {
		logEvent("No firewall rules specified; all requests will be allowed")
		if !runTUI {
			fmt.Fprintln(os.Stderr, "no firewall rules specified; all requests will be allowed")
		}
	}

	// Check for --store-url flag or DATABASE_URL environment variable for persistence.
	// Flag takes precedence over environment variable
	if storeURL == "" {
		storeURL = os.Getenv("DATABASE_URL")
	}

	if storeURL != "" {
		// Initialize store-backed registry
		logEvent("Initializing postgres persistent store")
		s, err := postgres.NewPostgresStore(storeURL)
		if err != nil {
			fatalError(fmt.Sprintf("failed to initialize postgres store: %v", err))
		}

		// Run migrations
		logEvent("Running database migrations")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := s.Migrate(ctx); err != nil {
			cancel()
			s.Close()
			fatalError(fmt.Sprintf("failed to run migrations: %v", err))
		}
		cancel()
		
		storeReg := registry.NewStoreBackedRegistry(s, cacheMaxSize)
		if fw != nil {
			storeReg.SetFirewall(fw)
		}

		// Warm the in-memory cache from the database on startup
		logEvent("Warming cache from database")
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		if err := storeReg.WarmCacheFromDB(ctx); err != nil {
			logEvent(fmt.Sprintf("WARNING: failed to warm cache from db: %v", err))
			if !runTUI {
				fmt.Fprintf(os.Stderr, "warning: failed to warm cache from db: %v\n", err)
			}
		} else {
			if cacheMaxSize > 0 {
				logEvent(fmt.Sprintf("Cache warmed with top %d most queried tasks from database", cacheMaxSize))
				if !runTUI {
					fmt.Fprintf(os.Stderr, "cache warmed with top %d most queried tasks from database\n", cacheMaxSize)
				}
			} else {
				logEvent("Cache warmed from database (unlimited)")
				if !runTUI {
					fmt.Fprintln(os.Stderr, "cache warmed from database (unlimited)")
				}
			}
		}
		cancel()

		reg = storeReg
		logEvent(fmt.Sprintf("Using postgres persistent store with LFU cache (max=%d)", cacheMaxSize))
		if !runTUI {
			fmt.Fprintf(os.Stderr, "using postgres persistent store with LFU cache (max=%d)\n", cacheMaxSize)
		}
	} else {
		// Fall back to in-memory registry
		memReg := registry.NewMemoryRegistry()
		if fw != nil {
			memReg.SetFirewall(fw)
		}
		reg = memReg
		logEvent("Using in-memory registry (no persistence)")
		if !runTUI {
			fmt.Fprintln(os.Stderr, "using in-memory registry (no persistence)")
		}
	}

	logEvent(fmt.Sprintf("Starting server on port %d (mode=%s)", listenPort, transportMode))
	if !runTUI {
		fmt.Fprintln(os.Stderr, "starting server (mode=", transportMode, ")")
	}
	switch transportMode {
	case "tls":
		logEvent(fmt.Sprintf("TLS mode: cert=%s key=%s ca=%s", tlsCertFile, tlsKeyFile, tlsClientCAFile))
		go func() {
			if err := transport.StartTCPServerTLS(reg, listenPort, tlsCertFile, tlsKeyFile, tlsClientCAFile, logEvent); err != nil {
				fatalError(fmt.Sprintf("TLS server error: %v", err))
			}
		}()
	case "tcp":
		go func() {
			if err := transport.StartTCPServer(reg, listenPort, logEvent); err != nil {
				fatalError(fmt.Sprintf("TCP server error: %v", err))
			}
		}()
	default:
		go func() {
			if err := transport.StartUDPServer(reg, listenPort, logEvent); err != nil {
				fatalError(fmt.Sprintf("UDP server error: %v", err))
			}
		}()
	}
	logEvent("Server started successfully")
	if !runTUI {
		fmt.Fprintln(os.Stderr, "Server correctly started")
	}
	logEvent(fmt.Sprintf("Broadcasting server info (heartbeat timeout: %v)", heartbeatTimeout))
	// Broadcast server info on boot (fire-and-forget)
	go transport.BroadcastServerInfo(listenPort, heartbeatTimeout, serverStartTime)

	// Build layout
	flex := tview.NewFlex()
	left := tview.NewFlex().SetDirection(tview.FlexRow)
	left.AddItem(searchField, 3, 0, false)
	left.AddItem(taskList, 0, 1, true)
	right := tview.NewFlex().SetDirection(tview.FlexRow)
	right.AddItem(detailTable, 0, 3, false)
	right.AddItem(logView, 0, 1, false)
	flex.AddItem(left, 30, 0, true)
	flex.AddItem(right, 0, 1, false)

	// Configure search field with live filtering
	searchField.SetBorder(true).SetTitle("Filter (Tab to focus, Esc to return)")
	searchField.SetChangedFunc(func(text string) {
		currentFilter = text
		requestDashboardUpdate() // Trigger refresh with filter
	})
	searchField.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEscape {
			app.SetFocus(taskList)
		}
	})
	
	taskList.SetBorder(true).SetTitle("Tasks (Tab to filter)")
	detailTable.SetBorder(true).SetTitle("Details")
	detailTable.SetSeparator('|') // Add column separators
	logView.SetBorder(true).SetTitle("Log")
	
	// Set up keyboard navigation
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyTab {
			// Toggle between filter and task list
			if app.GetFocus() == searchField {
				app.SetFocus(taskList)
			} else {
				app.SetFocus(searchField)
			}
			return nil
		}
		return event
	})

	// Decide whether to run the TUI based on earlier prompts.
	if !runTUI {
		logEvent("Running in headless mode (no TUI)")
		fmt.Fprintln(os.Stderr, "Server will continue running without the TUI.")
		// For headless mode, just run cleanup, no UI refresh needed
		ticker := time.NewTicker(cleanupInterval)
		go func() {
			for range ticker.C {
				removed := reg.Cleanup(heartbeatTimeout)
				if removed > 0 {
					logEvent(fmt.Sprintf("Cleanup removed %d entries", removed))
				}
			}
		}()
		select {}
	}

	logEvent("Starting TUI mode")
	// Note: runningTUI already set to true earlier
	// Note: uiUpdateCoordinator already started during initialization

	// TUI mode: start tickers and UI refresh
	// Periodic cleanup
	ticker := time.NewTicker(cleanupInterval)
	go func() {
		for range ticker.C {
			removed := reg.Cleanup(heartbeatTimeout)
			logEvent(fmt.Sprintf("Cleanup ran: removed %d stale entries", removed))

			// Warm cache from DB to ensure UI reflects deleted entries
			if storeReg, ok := reg.(*registry.StoreBackedRegistry); ok {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = storeReg.WarmCacheFromDB(ctx)
				cancel()
			}

			requestDashboardUpdate()
		}
	}()

	// Periodic UI refresh
	uiTicker := time.NewTicker(2 * time.Second)
	go func() {
		for range uiTicker.C {
			requestDashboardUpdate()
		}
	}()

	// Start initial data refresh AFTER UI is ready
	go func() {
		time.Sleep(100 * time.Millisecond)
		requestDashboardUpdate()
	}()

	// Start TUI
	if err := app.SetRoot(flex, true).Run(); err != nil {
		logEvent(fmt.Sprintf("ERROR: tview run error: %v", err))
		os.Exit(1)
	}
	logEvent("TUI exited")
	// Stderr is restored by defer, so this will be visible if we manually restore it
	if originalStderr != nil {
		fmt.Fprintln(originalStderr, "TUI exited")
	}
}
