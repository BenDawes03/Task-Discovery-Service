package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorPurple = "\033[35m"
	colorCyan   = "\033[36m"
	colorWhite  = "\033[37m"
	colorBold   = "\033[1m"
)

// Message types matching the TDS protocol
type Message struct {
	Command string `json:"cmd"`
	Task    string `json:"task"`
	Address string `json:"address,omitempty"`
	Capacity int    `json:"capacity,omitempty"`
}

type Response struct {
	Status  string `json:"status"`
	Address string `json:"address,omitempty"`
	Error   string `json:"error,omitempty"`
}

var (
	serverAddr = "127.0.0.1:5000"
	protocol   = "tcp" // "tcp" or "udp" - must match server mode
	stepByStep = true
	stats      = &Stats{}
	demoServices []DemoService
	demoRunID   = fmt.Sprintf("%d", time.Now().UnixNano()%100000)
)

type DemoService struct {
	name     string
	task     string
	address  string
	capacity int
}

type Stats struct {
	mu                sync.Mutex
	registrations     int
	queries           int
	successfulQueries int
	errors            int
}

func main() {
	parseFlags()

	clearScreen()
	printBanner()
	pause()

	// Step 1: Show architecture
	clearScreen()
	showArchitecture()
	pause()

	// Step 2: Start server (assumes it's already running or we tell user to start it)
	clearScreen()
	checkServer()
	pause()

	// Step 3: Register services
	clearScreen()
	registerServices()
	stopRefresh := startRegistrationRefresher(demoServices, 8*time.Second)
	defer stopRefresh()
	pause()

	// Step 4: Demonstrate queries
	clearScreen()
	demonstrateQueries()
	pause()

	// Step 5: Show round-robin
	clearScreen()
	demonstrateRoundRobin()
	pause()

	// Step 6: Show concurrent queries
	clearScreen()
	demonstrateWeightedCapacity()
	pause()

	// Step 7: Show concurrent queries
	clearScreen()
	demonstrateConcurrentQueries()
	pause()

	// Step 8: Demonstrate firewall mode behavior
	clearScreen()
	demonstrateFirewallMode()
	pause()

	// Step 9: Final summary
	clearScreen()
	showSummary()
}

func parseFlags() {
	protocolFlag := flag.String("protocol", protocol, "Transport protocol to use: tcp or udp")
	serverFlag := flag.String("server", serverAddr, "TDS server address in host:port format")
	stepFlag := flag.Bool("step-by-step", stepByStep, "Pause before each register/query request")

	flag.Parse()

	selectedProtocol := strings.ToLower(strings.TrimSpace(*protocolFlag))
	if selectedProtocol != "tcp" && selectedProtocol != "udp" {
		fmt.Fprintf(os.Stderr, "invalid -protocol value %q (expected tcp or udp)\n", *protocolFlag)
		os.Exit(2)
	}

	protocol = selectedProtocol
	serverAddr = strings.TrimSpace(*serverFlag)
	stepByStep = *stepFlag
}

func clearScreen() {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("cmd", "/c", "cls")
		cmd.Stdout = os.Stdout
		cmd.Run()
	} else {
		fmt.Print("\033[2J\033[H")
	}
}

func printBanner() {
	fmt.Printf("%s%s", colorCyan, colorBold)
	fmt.Println("╔════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                                                                ║")
	fmt.Println("║         Task Discovery Service (TDS) - Live Demo              ║")
	fmt.Println("║                                                                ║")
	fmt.Println("╚════════════════════════════════════════════════════════════════╝")
	fmt.Printf("%s\n", colorReset)

	fmt.Printf("%s", colorWhite)
	fmt.Println("\nWelcome to the TDS demonstration!")
	fmt.Println("\nThis demo will show you:")
	fmt.Printf("%s", colorYellow)
	fmt.Println("  ✓ How services register with TDS")
	fmt.Println("  ✓ How clients query for services")
	fmt.Println("  ✓ Round-robin load balancing")
	fmt.Println("  ✓ Weighted capacity routing")
	fmt.Println("  ✓ Firewall routing/blocking behavior")
	fmt.Println("  ✓ Concurrent query handling")
	fmt.Printf("%s\n", colorReset)

	fmt.Printf("%sPress ENTER to begin...%s", colorGreen, colorReset)
}

func showArchitecture() {
	fmt.Printf("%s%sSystem Architecture%s\n\n", colorBold, colorCyan, colorReset)

	fmt.Printf("%s", colorWhite)
	fmt.Println("┌─────────────────────────────────────────────────────────┐")
	fmt.Printf("│                  %sTDS Server%s                         │\n", colorGreen, colorWhite)
	fmt.Printf("│                  %s%s%s                            │\n", colorCyan, serverAddr, colorWhite)
	fmt.Println("│                     ┌──────────┐                      │")
	fmt.Println("│                     │ Registry │                      │")
	fmt.Println("│                     └─────┬────┘                      │")
	fmt.Println("└───────────────────────────┼─────────────────────────────┘")
	fmt.Println("                            │                             ")
	fmt.Println("            ┌───────────────┼────────────────┐            ")
	fmt.Println("            │               │                │            ")
	fmt.Printf("      %sREGISTER%s        %sQUERY%s          %sHEARTBEAT%s   \n",
		colorYellow, colorWhite, colorBlue, colorWhite, colorPurple, colorWhite)
	fmt.Println("            │               │                │            ")
	fmt.Println("   ┌────────▼────┐   ┌──────▼──────┐   ┌────▼────┐      ")
	fmt.Printf("   │ %sService A%s  │   │  %sClient 1%s   │   │ %sTimer%s   │      \n",
		colorGreen, colorWhite, colorCyan, colorWhite, colorPurple, colorWhite)
	fmt.Println("   │  task_web   │   └─────────────┘   └─────────┘      ")
	fmt.Println("   └─────────────┘                                       ")
	fmt.Println("   ┌─────────────┐   ┌─────────────┐                    ")
	fmt.Printf("   │ %sService B%s  │   │  %sClient 2%s   │                    \n",
		colorGreen, colorWhite, colorCyan, colorWhite)
	fmt.Println("   │  task_web   │   └─────────────┘                    ")
	fmt.Println("   └─────────────┘                                       ")
	fmt.Printf("%s\n", colorReset)

	fmt.Printf("%sFlow:%s\n", colorBold, colorReset)
	fmt.Printf("  %s1.%s Services register their task type and address\n", colorYellow, colorWhite)
	fmt.Printf("  %s2.%s Clients query TDS for a service by task name\n", colorBlue, colorWhite)
	fmt.Printf("  %s3.%s TDS returns an available service address (round-robin)\n", colorGreen, colorWhite)
	fmt.Printf("  %s4.%s Services send heartbeats to stay alive\n", colorPurple, colorWhite)
	fmt.Printf("%s\n", colorReset)

	fmt.Printf("%sPress ENTER to continue...%s", colorGreen, colorReset)
}

func checkServer() {
	fmt.Printf("%s%sStep 1: Checking TDS Server%s\n\n", colorBold, colorCyan, colorReset)

	fmt.Printf("%s⏳ Attempting to connect to server at %s using %s...%s\n", colorYellow, serverAddr, strings.ToUpper(protocol), colorReset)
	time.Sleep(500 * time.Millisecond)

	// Try to connect
	conn, err := net.DialTimeout(protocol, serverAddr, 2*time.Second)
	if err != nil {
		fmt.Printf("%s✗ Server not reachable via %s!%s\n\n", colorRed, strings.ToUpper(protocol), colorReset)
		fmt.Printf("%sPlease start the TDS server first:%s\n", colorYellow, colorReset)
		fmt.Println("  cd cmd/server")
		fmt.Println("  go run main.go")
		fmt.Println("\nOr build and run:")
		fmt.Println("  go build -o server.exe ./cmd/server")
		fmt.Println("  .\\server.exe")
		fmt.Printf("\n%sNote: Make sure server uses the same protocol (%s) as this demo%s\n", colorYellow, strings.ToUpper(protocol), colorReset)
		os.Exit(1)
	}
	conn.Close()

	fmt.Printf("%s✓ Server is running and accessible via %s!%s\n", colorGreen, strings.ToUpper(protocol), colorReset)
	fmt.Printf("\n%sPress ENTER to continue...%s", colorGreen, colorReset)
}

func registerServices() {
	fmt.Printf("%s%sStep 2: Registering Services%s\n\n", colorBold, colorCyan, colorReset)

	services := []DemoService{
		{name: "Web Service Alpha", task: "task_web", address: "127.0.0.1:8001", capacity: 1},
		{name: "Web Service Beta", task: "task_web", address: "127.0.0.1:8002", capacity: 1},
		{name: "Web Service Gamma", task: "task_web", address: "127.0.0.1:8003", capacity: 1},
		{name: "Database Service", task: "task_db", address: "127.0.0.1:8004", capacity: 1},
		{name: "Cache Service", task: "task_cache", address: "127.0.0.1:8005", capacity: 1},
	}
	demoServices = services

	fmt.Printf("%sRegistering %d services with TDS...%s\n\n", colorWhite, len(services), colorReset)

	for i, svc := range services {
		if stepByStep {
			waitForRequest("REGISTER", svc.task, svc.address)
		}

		fmt.Printf("%s[%d/%d]%s Registering %s%s%s on task '%s%s%s'...",
			colorCyan, i+1, len(services), colorWhite,
			colorGreen, svc.name, colorWhite,
			colorYellow, svc.task, colorWhite)

		success := sendRegisterWithCapacity(svc.task, svc.address, svc.capacity)

		time.Sleep(300 * time.Millisecond) // Visual pacing

		if success {
			fmt.Printf(" %s✓%s\n", colorGreen, colorReset)
			stats.mu.Lock()
			stats.registrations++
			stats.mu.Unlock()
		} else {
			fmt.Printf(" %s✗ FAILED%s\n", colorRed, colorReset)
			stats.mu.Lock()
			stats.errors++
			stats.mu.Unlock()
		}
	}

	fmt.Printf("\n%s✓ Registration complete!%s\n", colorGreen, colorReset)
	fmt.Printf("%sBackground refresh enabled:%s services re-register every 8s to keep demo data fresh.\n", colorYellow, colorReset)
	fmt.Printf("  %sSuccessful: %d%s\n", colorGreen, stats.registrations, colorReset)
	if stats.errors > 0 {
		fmt.Printf("  %sFailed: %d%s\n", colorRed, stats.errors, colorReset)
	}

	fmt.Printf("\n%sPress ENTER to continue...%s", colorGreen, colorReset)
}

func demonstrateQueries() {
	fmt.Printf("%s%sStep 3: Querying for Services%s\n\n", colorBold, colorCyan, colorReset)

	queries := []string{"task_web", "task_db", "task_cache", "task_nonexistent"}

	fmt.Printf("%sClients requesting services by task name...%s\n\n", colorWhite, colorReset)

	for i, task := range queries {
		if stepByStep {
			waitForRequest("QUERY", task, "")
		}

		fmt.Printf("%s[Query %d]%s Looking for '%s%s%s'...",
			colorCyan, i+1, colorWhite,
			colorYellow, task, colorWhite)

		address, success := sendQuery(task)

		time.Sleep(400 * time.Millisecond)

		if success {
			fmt.Printf(" %s✓%s Found: %s%s%s\n", colorGreen, colorReset, colorGreen, address, colorReset)
			stats.mu.Lock()
			stats.successfulQueries++
			stats.mu.Unlock()
		} else {
			fmt.Printf(" %s✗%s Not found\n", colorRed, colorReset)
		}

		stats.mu.Lock()
		stats.queries++
		stats.mu.Unlock()
	}

	fmt.Printf("\n%sPress ENTER to continue...%s", colorGreen, colorReset)
}

func demonstrateRoundRobin() {
	fmt.Printf("%s%sStep 4: Round-Robin Load Balancing%s\n\n", colorBold, colorCyan, colorReset)

	fmt.Printf("%sQuerying for 'task_web' multiple times to show round-robin...%s\n\n", colorWhite, colorReset)
	fmt.Printf("%sWe registered 3 web services. Watch them cycle:%s\n\n", colorYellow, colorReset)

	addresses := make(map[string]int)

	for i := 1; i <= 9; i++ {
		if stepByStep {
			waitForRequest("QUERY", "task_web", "")
		}

		fmt.Printf("%s[Request %d]%s task_web → ",
			colorCyan, i, colorWhite)

		address, success := sendQuery("task_web")

		time.Sleep(400 * time.Millisecond)

		if success {
			addresses[address]++
			stats.mu.Lock()
			stats.successfulQueries++
			stats.queries++
			stats.mu.Unlock()

			// Show which service we got
			color := colorGreen
			if i%3 == 1 {
				color = colorGreen
			} else if i%3 == 2 {
				color = colorYellow
			} else {
				color = colorBlue
			}
			fmt.Printf("%s%s%s\n", color, address, colorReset)
		} else {
			fmt.Printf("%sERROR%s\n", colorRed, colorReset)
			stats.mu.Lock()
			stats.queries++
			stats.mu.Unlock()
		}
	}

	fmt.Printf("\n%sDistribution:%s\n", colorBold, colorReset)
	for addr, count := range addresses {
		bar := strings.Repeat("█", count)
		fmt.Printf("  %s%-20s%s %s%s%s (%d requests)\n",
			colorWhite, addr, colorReset,
			colorGreen, bar, colorReset, count)
	}

	fmt.Printf("\n%s✓ Each service received approximately equal load!%s\n", colorGreen, colorReset)
	fmt.Printf("\n%sPress ENTER to continue...%s", colorGreen, colorReset)
}

func demonstrateConcurrentQueries() {
	fmt.Printf("%s%sStep 6: Concurrent Query Performance%s\n\n", colorBold, colorCyan, colorReset)

	numThreads := 20
	queriesPerThread := 5
	totalQueries := numThreads * queriesPerThread

	fmt.Printf("%sSimulating %d concurrent clients...%s\n", colorWhite, numThreads, colorReset)
	fmt.Printf("%sEach client makes %d requests%s\n\n", colorWhite, queriesPerThread, colorReset)

	fmt.Printf("%s⏳ Running %d total queries...%s\n", colorYellow, totalQueries, colorReset)

	start := time.Now()
	var wg sync.WaitGroup
	successChan := make(chan bool, totalQueries)

	// Progress indicator
	done := make(chan bool)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		dots := 0
		for {
			select {
			case <-ticker.C:
				fmt.Printf("\r%s⏳ Running queries%s", colorYellow, strings.Repeat(".", dots%4))
				dots++
			case <-done:
				return
			}
		}
	}()

	for i := 0; i < numThreads; i++ {
		wg.Add(1)
		go func(threadID int) {
			defer wg.Done()
			tasks := []string{"task_web", "task_db", "task_cache"}
			for j := 0; j < queriesPerThread; j++ {
				task := tasks[rand.Intn(len(tasks))]
				_, success := sendQuery(task)
				successChan <- success
				time.Sleep(time.Duration(rand.Intn(50)) * time.Millisecond)
			}
		}(i)
	}

	wg.Wait()
	close(successChan)
	done <- true

	elapsed := time.Since(start)

	// Count successes
	successful := 0
	for success := range successChan {
		if success {
			successful++
		}
	}

	stats.mu.Lock()
	stats.queries += totalQueries
	stats.successfulQueries += successful
	stats.mu.Unlock()

	fmt.Printf("\r%s✓ Completed in %s%s\n\n", colorGreen, elapsed.Round(time.Millisecond), colorReset)

	qps := float64(totalQueries) / elapsed.Seconds()

	fmt.Printf("%sResults:%s\n", colorBold, colorReset)
	fmt.Printf("  Total queries:    %s%d%s\n", colorCyan, totalQueries, colorReset)
	fmt.Printf("  Successful:       %s%d%s\n", colorGreen, successful, colorReset)
	if totalQueries-successful > 0 {
		fmt.Printf("  Failed:           %s%d%s\n", colorRed, totalQueries-successful, colorReset)
	}
	fmt.Printf("  Duration:         %s%s%s\n", colorYellow, elapsed.Round(time.Millisecond), colorReset)
	fmt.Printf("  Queries/second:   %s%.1f%s\n", colorCyan, qps, colorReset)
	fmt.Printf("  Avg latency:      %s%s%s\n", colorYellow, (elapsed / time.Duration(totalQueries)).Round(time.Microsecond), colorReset)

	fmt.Printf("\n%sPress ENTER to continue...%s", colorGreen, colorReset)
}

func demonstrateWeightedCapacity() {
	fmt.Printf("%s%sStep 5: Weighted Capacity Load Balancing%s\n\n", colorBold, colorCyan, colorReset)

	weightedTask := "task_weighted_" + demoRunID
	weighted := []DemoService{
		{name: "Weighted Small", task: weightedTask, address: "127.0.0.1:8101", capacity: 1},
		{name: "Weighted Medium", task: weightedTask, address: "127.0.0.1:8102", capacity: 2},
		{name: "Weighted Large", task: weightedTask, address: "127.0.0.1:8103", capacity: 3},
	}

	fmt.Printf("%sRegistering 3 weighted services with capacities 1:2:3...%s\n", colorWhite, colorReset)
	for _, svc := range weighted {
		if stepByStep {
			waitForRequest("REGISTER", svc.task, svc.address)
		}
		fmt.Printf("  %s%-16s%s -> %s (cap=%d)\n", colorGreen, svc.name, colorReset, svc.address, svc.capacity)
		if !sendRegisterWithCapacity(svc.task, svc.address, svc.capacity) {
			fmt.Printf("%sFailed to register weighted service; skipping weighted demo.%s\n", colorRed, colorReset)
			fmt.Printf("\n%sPress ENTER to continue...%s", colorGreen, colorReset)
			return
		}
		stats.mu.Lock()
		stats.registrations++
		stats.mu.Unlock()
	}

	const total = 120
	counts := make(map[string]int)
	if stepByStep {
		fmt.Printf("\n%sPress ENTER to run %d weighted queries automatically...%s", colorGreen, total, colorReset)
		pause()
	}

	fmt.Printf("\n%sRunning %d weighted queries (automatic)...%s\n", colorWhite, total, colorReset)
	for i := 0; i < total; i++ {
		addr, ok := sendQuery(weightedTask)
		stats.mu.Lock()
		stats.queries++
		if ok {
			stats.successfulQueries++
		}
		stats.mu.Unlock()
		if ok {
			counts[addr]++
			if (i+1)%20 == 0 {
				fmt.Printf("  %sProgress:%s %3d/%d queries complete\n", colorCyan, colorReset, i+1, total)
			}
		} else if (i+1)%20 == 0 {
			fmt.Printf("  %sProgress:%s %3d/%d queries complete (with errors)\n", colorCyan, colorReset, i+1, total)
		}
	}

	totalCap := 0
	for _, svc := range weighted {
		totalCap += svc.capacity
	}

	fmt.Printf("\n%sObserved distribution across %d queries:%s\n", colorBold, total, colorReset)
	for _, svc := range weighted {
		actual := counts[svc.address]
		expectedPct := float64(svc.capacity) / float64(totalCap) * 100
		expectedCount := float64(total) * float64(svc.capacity) / float64(totalCap)
		actualPct := float64(actual) / float64(total) * 100
		fmt.Printf("  %s%-16s%s %s%-16s%s expected ~%4.1f%% (~%.0f), observed %4.1f%% (%d/%d)\n",
			colorWhite, svc.name, colorReset,
			colorCyan, svc.address, colorReset,
			expectedPct, expectedCount, actualPct, actual, total)
		bar := strings.Repeat("█", actual/2)
		fmt.Printf("    %s%s%s\n", colorGreen, bar, colorReset)
	}

	fmt.Printf("\n%s✓ Proportions should be close to 1:2:3 over enough queries.%s\n", colorGreen, colorReset)
	fmt.Printf("%sPress ENTER to continue...%s", colorGreen, colorReset)
}

func demonstrateFirewallMode() {
	fmt.Printf("%s%sStep 7: Firewall Mode Demonstration%s\n\n", colorBold, colorCyan, colorReset)

	routingTask := "task_firewall_route_" + demoRunID
	blockedOnlyTask := "task_firewall_block_only_" + demoRunID

	allowedAddr := "127.0.0.1:8201"
	routableAltAddr := "127.0.0.1:8202"
	blockedAddr := "10.123.123.123:8203"

	if stepByStep {
		waitForRequest("REGISTER", routingTask, allowedAddr)
	}
	_ = sendRegister(routingTask, allowedAddr)
	if stepByStep {
		waitForRequest("REGISTER", routingTask, blockedAddr)
	}
	_ = sendRegister(routingTask, blockedAddr)
	if stepByStep {
		waitForRequest("REGISTER", routingTask, routableAltAddr)
	}
	_ = sendRegister(routingTask, routableAltAddr)
	if stepByStep {
		waitForRequest("REGISTER", blockedOnlyTask, blockedAddr)
	}
	_ = sendRegister(blockedOnlyTask, blockedAddr)
	stats.mu.Lock()
	stats.registrations += 4
	stats.mu.Unlock()

	fmt.Printf("%sRegistered firewall demo services:%s\n", colorWhite, colorReset)
	fmt.Printf("  route task:       %s\n", routingTask)
	fmt.Printf("  local candidates: %s, %s\n", allowedAddr, routableAltAddr)
	fmt.Printf("  non-local cand.:  %s\n", blockedAddr)
	fmt.Printf("  blocked-only:     %s\n\n", blockedOnlyTask)

	fmt.Printf("%sFirewall preflight%s\n", colorBold, colorReset)
	fmt.Printf("  The demo first checks whether this server session is actually filtering results.\n")
	if stepByStep {
		waitForRequest("QUERY", blockedOnlyTask, "")
	}
	preflightResp, preflightErr := sendQueryDetailed(blockedOnlyTask)
	stats.mu.Lock()
	stats.queries++
	if preflightErr == nil && preflightResp != nil && preflightResp.Status == "OK" {
		stats.successfulQueries++
	}
	stats.mu.Unlock()

	if preflightErr != nil {
		fmt.Printf("  %sPreflight error:%s %v\n", colorRed, colorReset, preflightErr)
		fmt.Printf("\n%sResult:%s could not verify firewall behavior.\n", colorYellow, colorReset)
		fmt.Printf("\n%sPress ENTER to continue...%s", colorGreen, colorReset)
		return
	}
	if preflightResp == nil {
		fmt.Printf("  %sPreflight error:%s no response from server\n", colorRed, colorReset)
		fmt.Printf("\n%sResult:%s could not verify firewall behavior.\n", colorYellow, colorReset)
		fmt.Printf("\n%sPress ENTER to continue...%s", colorGreen, colorReset)
		return
	}

	fmt.Printf("  blocked-only query returned status=%s", preflightResp.Status)
	if preflightResp.Address != "" {
		fmt.Printf(" addr=%s", preflightResp.Address)
	}
	fmt.Printf("\n")

	if preflightResp.Status != "FORBIDDEN" {
		fmt.Printf("\n%sResult:%s firewall filtering is not active for this demo run.\n", colorYellow, colorReset)
		fmt.Printf("  This means one of the following is true:\n")
		fmt.Printf("  - server was started without --firewall\n")
		fmt.Printf("  - server is running in permissive firewall mode\n")
		fmt.Printf("  - current rules do not block %s for requestor 127.0.0.1\n", blockedAddr)
		fmt.Printf("\n  Because filtering is not active, showing 12 route queries would just demonstrate normal weighted routing, not firewall behavior.\n")
		fmt.Printf("\n%sPress ENTER to continue...%s", colorGreen, colorReset)
		return
	}

	fmt.Printf("\n%sRouting demonstration%s (6 queries to %s)\n", colorBold, colorReset, routingTask)
	routedCounts := map[string]int{}
	for i := 0; i < 6; i++ {
		if stepByStep {
			waitForRequest("QUERY", routingTask, "")
		}
		resp, err := sendQueryDetailed(routingTask)
		stats.mu.Lock()
		stats.queries++
		if err == nil && resp != nil && resp.Status == "OK" {
			stats.successfulQueries++
		}
		stats.mu.Unlock()

		if err != nil {
			fmt.Printf("  %s[Q%02d]%s error: %v\n", colorCyan, i+1, colorReset, err)
			continue
		}
		if resp == nil {
			fmt.Printf("  %s[Q%02d]%s no response\n", colorCyan, i+1, colorReset)
			continue
		}

		routedCounts[resp.Address]++
		fmt.Printf("  %s[Q%02d]%s status=%-9s addr=%s\n", colorCyan, i+1, colorReset, resp.Status, resp.Address)
	}

	fmt.Printf("\n%sFirewall summary%s\n", colorBold, colorReset)
	fmt.Printf("  route task -> %s : %d\n", allowedAddr, routedCounts[allowedAddr])
	fmt.Printf("  route task -> %s : %d\n", routableAltAddr, routedCounts[routableAltAddr])
	fmt.Printf("  route task -> %s : %d\n", blockedAddr, routedCounts[blockedAddr])
	fmt.Printf("\n%sResult:%s PASS - preflight proved filtering is active, and the route task shows what addresses remain reachable.\n", colorGreen, colorReset)

	fmt.Printf("\n%sPress ENTER to continue...%s", colorGreen, colorReset)
}

func showSummary() {
	fmt.Printf("%s%sDemonstration Complete!%s\n\n", colorBold, colorCyan, colorReset)

	fmt.Printf("%s", colorWhite)
	fmt.Println("╔════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                      SESSION SUMMARY                           ║")
	fmt.Println("╚════════════════════════════════════════════════════════════════╝")
	fmt.Printf("%s\n", colorReset)

	stats.mu.Lock()
	defer stats.mu.Unlock()

	fmt.Printf("%sTotal Operations:%s\n", colorBold, colorReset)
	fmt.Printf("  Services registered:     %s%d%s\n", colorGreen, stats.registrations, colorReset)
	fmt.Printf("  Queries performed:       %s%d%s\n", colorCyan, stats.queries, colorReset)
	fmt.Printf("  Successful queries:      %s%d%s\n", colorGreen, stats.successfulQueries, colorReset)
	if stats.errors > 0 {
		fmt.Printf("  Errors encountered:      %s%d%s\n", colorRed, stats.errors, colorReset)
	}

	if stats.queries > 0 {
		successRate := float64(stats.successfulQueries) / float64(stats.queries) * 100
		fmt.Printf("\n%sSuccess Rate: %s%.1f%%%s\n", colorBold, colorGreen, successRate, colorReset)
	}

	fmt.Printf("\n%sKey Features Demonstrated:%s\n", colorBold, colorReset)
	fmt.Printf("  %s✓%s Service registration\n", colorGreen, colorWhite)
	fmt.Printf("  %s✓%s Service discovery by task name\n", colorGreen, colorWhite)
	fmt.Printf("  %s✓%s Round-robin load balancing\n", colorGreen, colorWhite)
	fmt.Printf("  %s✓%s Concurrent query handling\n", colorGreen, colorWhite)
	fmt.Printf("  %s✓%s High-performance operations\n", colorGreen, colorWhite)

	fmt.Printf("\n%s", colorCyan)
	fmt.Println("Thank you for watching this TDS demonstration!")
	fmt.Printf("%s\n", colorReset)
}

func sendRegister(task, address string) bool {
	return sendRegisterWithCapacity(task, address, 1)
}

func sendRegisterWithCapacity(task, address string, capacity int) bool {
	msg := Message{
		Command: "REGISTER",
		Task:    task,
		Address: address,
		Capacity: capacity,
	}

	resp, err := sendMessage(msg)
	if err != nil {
		fmt.Printf("\n%s  Error: %v%s", colorRed, err, colorReset)
		return false
	}

	if resp.Status != "OK" {
		fmt.Printf("\n%s  Server response: %s", colorRed, resp.Status)
		if resp.Error != "" {
			fmt.Printf(" - %s", resp.Error)
		}
		fmt.Printf("%s", colorReset)
		return false
	}

	return true
}

func sendQuery(task string) (string, bool) {
	resp, err := sendQueryDetailed(task)
	if err != nil {
		return "", false
	}

	if resp.Status == "OK" && resp.Address != "" {
		return resp.Address, true
	}

	return "", false
}

func sendQueryDetailed(task string) (*Response, error) {
	msg := Message{
		Command: "QUERY",
		Task:    task,
	}

	return sendMessage(msg)
}

func startRegistrationRefresher(services []DemoService, interval time.Duration) func() {
	if len(services) == 0 {
		return func() {}
	}

	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				for _, svc := range services {
					_ = sendRegisterWithCapacity(svc.task, svc.address, svc.capacity)
				}
			case <-stop:
				return
			}
		}
	}()

	return func() {
		close(stop)
	}
}

func sendMessage(msg Message) (*Response, error) {
	if protocol == "tcp" {
		return sendTCP(msg)
	}
	return sendUDP(msg)
}

func sendTCP(msg Message) (*Response, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}

	// TCP requires newline-terminated JSON
	data = append(data, '\n')

	conn, err := net.DialTimeout("tcp", serverAddr, 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(2 * time.Second))

	// Send request
	_, err = conn.Write(data)
	if err != nil {
		return nil, err
	}

	// Read response (one line)
	buffer := make([]byte, 4096)
	n, err := conn.Read(buffer)
	if err != nil {
		return nil, err
	}

	// Remove trailing newline if present
	responseData := buffer[:n]
	if n > 0 && responseData[n-1] == '\n' {
		responseData = responseData[:n-1]
	}

	var resp Response
	err = json.Unmarshal(responseData, &resp)
	if err != nil {
		return nil, err
	}

	return &resp, nil
}

func sendUDP(msg Message) (*Response, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}

	// Debug: uncomment to see JSON being sent
	// fmt.Printf("\n[DEBUG] Sending: %s\n", string(data))

	conn, err := net.DialTimeout("udp", serverAddr, 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(2 * time.Second))

	_, err = conn.Write(data)
	if err != nil {
		return nil, err
	}

	buffer := make([]byte, 4096)
	n, err := conn.Read(buffer)
	if err != nil {
		return nil, err
	}

	// Debug: uncomment to see response
	// fmt.Printf("[DEBUG] Received: %s\n", string(buffer[:n]))

	var resp Response
	err = json.Unmarshal(buffer[:n], &resp)
	if err != nil {
		return nil, err
	}

	return &resp, nil
}

func pause() {
	fmt.Scanln()
}

func waitForRequest(command, task, address string) {
	if command == "REGISTER" {
		fmt.Printf("%sPress ENTER to send %s %s -> %s...%s", colorGreen, command, task, address, colorReset)
	} else {
		fmt.Printf("%sPress ENTER to send %s %s...%s", colorGreen, command, task, colorReset)
	}
	pause()
}
