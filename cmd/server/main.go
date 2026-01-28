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

	"github.com/rivo/tview"
	"golang.org/x/term"

	"tds/pkg/registry"
	"tds/pkg/store/postgres"
	"tds/pkg/transport"
)

// Config
const (
	ListenPort      = 5000
	CleanupInterval = 10 * time.Second
	CacheMaxSize    = 100 // Maximum number of tasks to keep in cache (0 = unlimited)
)

var (
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

	serverStartTime = time.Now()
	reg             registry.Registry

	app         = tview.NewApplication()
	taskList    = tview.NewList()
	detailTable = tview.NewTable()
	logView     = tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
	searchField = tview.NewInputField().SetLabel(" Filter: ")

	// File logging
	logFile   *os.File
	logWriter io.Writer

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

				// Update task list
				taskList.Clear()
				var firstTask string
				for task, entries := range servicesMapCopy {
					label := fmt.Sprintf("%s (%d)", task, len(entries))
					t := task
					if firstTask == "" {
						firstTask = task
					}
					taskList.AddItem(label, "", 0, func() {
						// Request update for selected task (avoid nested QueueUpdateDraw)
						selectedTask := t
						go func() {
							services := reg.ListServices()[selectedTask]
							app.QueueUpdateDraw(func() {
								updateDetailsTable(services)
							})
						}()
					})
				}
				// Auto-update details for first task
				if firstTask != "" {
					if services, ok := servicesMapCopy[firstTask]; ok {
						// Temporarily unlock for the nested call
						uiMutex.Unlock()
						updateDetailsTable(services)
						uiMutex.Lock()
					}
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
	detailTable.SetCell(0, 0, tview.NewTableCell("Address").SetSelectable(false))
	detailTable.SetCell(0, 1, tview.NewTableCell("LastHeartbeat").SetSelectable(false))
	detailTable.SetCell(0, 2, tview.NewTableCell("Queries").SetSelectable(false))
	for i, e := range services {
		detailTable.SetCell(i+1, 0, tview.NewTableCell(e.Address))
		detailTable.SetCell(i+1, 1, tview.NewTableCell(e.LastHeartbeat.Format(time.RFC3339)))
		detailTable.SetCell(i+1, 2, tview.NewTableCell(fmt.Sprintf("%d", e.QueryCount)))
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
	logsDir := "logs"
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return fmt.Errorf("failed to create logs directory: %w", err)
	}

	// Create log filename with timestamp
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	logPath := filepath.Join(logsDir, fmt.Sprintf("server_%s.log", timestamp))

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
	forceUI := false
	skipPrompts := false

	// Parse command-line flags
	for i, a := range os.Args[1:] {
		switch a {
		case "--force-ui", "--ui":
			forceUI = true
			runTUI = true
			skipPrompts = true
		case "--no-ui":
			runTUI = false
			skipPrompts = true
		case "--tcp":
			transportMode = "tcp"
		case "--udp":
			transportMode = "udp"
		case "--tls":
			transportMode = "tls"
			useTLS = true
		case "--store-url", "--tls-cert", "--tls-key", "--tls-client-ca":
			// Skip next arg (it's the value)
			i++
		}
		_ = i
	}

	// If not in interactive terminal or prompts skipped, return early
	if skipPrompts || !term.IsTerminal(int(os.Stdin.Fd())) {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			fmt.Fprintln(os.Stderr, "No interactive terminal detected; defaulting to no TUI")
			runTUI = false
		}
		return transportMode, runTUI, forceUI
	}

	// Interactive prompts
	reader := bufio.NewReader(os.Stdin)
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
	// Parse command-line flags
	flag.DurationVar(&heartbeatTimeout, "heartbeat-timeout", 60*time.Second, "Timeout for service heartbeats")
	flag.DurationVar(&cleanupInterval, "cleanup-interval", 10*time.Second, "Interval for cleanup of stale entries")
	flag.BoolVar(&useTLS, "tls", false, "Enable TLS with mutual authentication (requires --tcp or interactive selection)")
	flag.StringVar(&tlsCertFile, "tls-cert", "certs/server.crt", "Server TLS certificate file")
	flag.StringVar(&tlsKeyFile, "tls-key", "certs/server.key", "Server TLS private key file")
	flag.StringVar(&tlsClientCAFile, "tls-client-ca", "certs/ca.crt", "CA certificate to verify client certificates")
	flag.StringVar(&storeURL, "store-url", "", "Database URL for persistent storage (e.g., postgres://user:password@host:port/dbname)")
	flag.Parse()

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
	var originalStderr *os.File
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
			logEvent(fmt.Sprintf("ERROR: failed to initialize postgres store: %v", err))
			if !runTUI {
				fmt.Fprintf(os.Stderr, "failed to initialize postgres store: %v\n", err)
			}
			os.Exit(1)
		}

		// Run migrations
		logEvent("Running database migrations")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := s.Migrate(ctx); err != nil {
			cancel()
			logEvent(fmt.Sprintf("ERROR: failed to run migrations: %v", err))
			if !runTUI {
				fmt.Fprintf(os.Stderr, "failed to run migrations: %v\n", err)
			}
			s.Close()
			os.Exit(1)
		}
		cancel()

		storeReg := registry.NewStoreBackedRegistry(s, CacheMaxSize)

		// Warm the in-memory cache from the database on startup
		logEvent("Warming cache from database")
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		if err := storeReg.WarmCacheFromDB(ctx); err != nil {
			logEvent(fmt.Sprintf("WARNING: failed to warm cache from db: %v", err))
			if !runTUI {
				fmt.Fprintf(os.Stderr, "warning: failed to warm cache from db: %v\n", err)
			}
		} else {
			if CacheMaxSize > 0 {
				logEvent(fmt.Sprintf("Cache warmed with top %d most queried tasks from database", CacheMaxSize))
				if !runTUI {
					fmt.Fprintf(os.Stderr, "cache warmed with top %d most queried tasks from database\n", CacheMaxSize)
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
		logEvent(fmt.Sprintf("Using postgres persistent store with LFU cache (max=%d)", CacheMaxSize))
		if !runTUI {
			fmt.Fprintf(os.Stderr, "using postgres persistent store with LFU cache (max=%d)\n", CacheMaxSize)
		}
	} else {
		// Fall back to in-memory registry
		reg = registry.NewMemoryRegistry()
		logEvent("Using in-memory registry (no persistence)")
		if !runTUI {
			fmt.Fprintln(os.Stderr, "using in-memory registry (no persistence)")
		}
	}

	logEvent(fmt.Sprintf("Starting server on port %d (mode=%s)", ListenPort, transportMode))
	if !runTUI {
		fmt.Fprintln(os.Stderr, "starting server (mode=", transportMode, ")")
	}
	switch transportMode {
	case "tls":
		logEvent(fmt.Sprintf("TLS mode: cert=%s key=%s ca=%s", tlsCertFile, tlsKeyFile, tlsClientCAFile))
		go func() {
			if err := transport.StartTCPServerTLS(reg, ListenPort, tlsCertFile, tlsKeyFile, tlsClientCAFile, logEvent); err != nil {
				logEvent(fmt.Sprintf("ERROR: TLS server error: %v", err))
				if !runTUI {
					fmt.Fprintln(os.Stderr, "tls server error:", err)
				}
				os.Exit(1)
			}
		}()
	case "tcp":
		go func() {
			if err := transport.StartTCPServer(reg, ListenPort, logEvent); err != nil {
				logEvent(fmt.Sprintf("ERROR: TCP server error: %v", err))
				if !runTUI {
					fmt.Fprintln(os.Stderr, "tcp server error:", err)
				}
				os.Exit(1)
			}
		}()
	default:
		go func() {
			if err := transport.StartUDPServer(reg, ListenPort, logEvent); err != nil {
				logEvent(fmt.Sprintf("ERROR: UDP server error: %v", err))
				if !runTUI {
					fmt.Fprintln(os.Stderr, "udp server error:", err)
				}
				os.Exit(1)
			}
		}()
	}
	logEvent("Server started successfully")
	if !runTUI {
		fmt.Fprintln(os.Stderr, "Server correctly started")
	}
	logEvent(fmt.Sprintf("Broadcasting server info (heartbeat timeout: %v)", heartbeatTimeout))
	// Broadcast server info on boot (fire-and-forget)
	go transport.BroadcastServerInfo(ListenPort, heartbeatTimeout, serverStartTime)

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

	searchField.SetBorder(true).SetTitle("Filter")
	taskList.SetBorder(true).SetTitle("Tasks")
	detailTable.SetBorder(true).SetTitle("Details")
	logView.SetBorder(true).SetTitle("Log")

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

	// Start the UI update coordinator (single goroutine for all UI updates)
	go uiUpdateCoordinator()

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
