package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
)

type Message struct {
	Command  string `json:"cmd"`
	Task     string `json:"task"`
	Address  string `json:"address,omitempty"`
	Capacity int    `json:"capacity,omitempty"`
}

type Response struct {
	Status  string `json:"status"`
	Address string `json:"address,omitempty"`
	Error   string `json:"error,omitempty"`
}

var (
	serverAddr = "localhost:5000"
	protocol   = "tcp"
)

func main() {
	clearScreen()
	printBanner()
	pause()

	// Check server
	clearScreen()
	checkServer()
	pause()

	// Register tasks
	clearScreen()
	registerTasks()
	pause()

	// Phase 1: Prime cache with task_002 (3 queries to establish it in cache)
	clearScreen()
	fmt.Printf("%s%sPhase 1: Prime the Cache%s\n\n", colorBold, colorCyan, colorReset)
	fmt.Printf("%sNow querying task_002 three times.%s\n", colorYellow, colorReset)
	fmt.Printf("%sWatch the server UI - task_002 should appear and stay visible.%s\n\n", colorYellow, colorReset)

	primeLatencies := performConsistentQueries("task_002", 3)
	showLatencies("Priming cache with task_002", primeLatencies)
	pause()

	// Phase 2: Now query task_001 - watch it evict task_002 and get cached
	clearScreen()
	fmt.Printf("%s%sPhase 2: Watch Cache Eviction and Population%s\n\n", colorBold, colorBlue, colorReset)
	fmt.Printf("%sNow querying task_001 ten times.%s\n", colorYellow, colorReset)
	fmt.Printf("%s  First query: SLOW (task_002 evicted, task_001 loaded from DB)%s\n", colorRed, colorReset)
	fmt.Printf("%s  Queries 2-10: FAST (task_001 stays in cache)%s\n\n", colorGreen, colorReset)

	task1Latencies := performConsistentQueries("task_001", 10)
	showLatencies("Cache Behavior: task_001 queries", task1Latencies)
	pause()

	// Comparison
	clearScreen()
	fmt.Printf("%s%s=== ANALYSIS ===%s\n\n", colorBold, colorCyan, colorReset)
	showCacheTransitionAnalysis(primeLatencies, task1Latencies)
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
	fmt.Println("║         TDS Cache Miss Demonstration (Cache Size = 1)          ║")
	fmt.Println("║                                                                ║")
	fmt.Println("╚════════════════════════════════════════════════════════════════╝")
	fmt.Printf("%s\n", colorReset)

	fmt.Printf("%s", colorYellow)
	fmt.Println("\nThis demo shows how caching affects query performance when")
	fmt.Println("the server is configured with cache-max-size=1")
	fmt.Println("\nYou should see:")
	fmt.Printf("%s", colorGreen)
	fmt.Println("  ✓ Fast queries for the cached task (sub-millisecond)")
	fmt.Printf("%s", colorRed)
	fmt.Println("  ✗ Slower queries for non-cached tasks (requires DB lookup)")
	fmt.Printf("%s\n", colorReset)

	fmt.Printf("%sStart the server with:%s\n", colorYellow, colorReset)
	fmt.Println("  ./server.exe --store-url=\"postgresql://user:pass@localhost/tds\" --cache-max-size=1")

	fmt.Printf("%sPress ENTER to begin...%s", colorGreen, colorReset)
}

func checkServer() {
	fmt.Printf("%s%sChecking if TDS Server is running...%s\n\n", colorBold, colorCyan, colorReset)

	fmt.Printf("%s⏳ Connecting to %s using %s...%s\n", colorYellow, serverAddr, strings.ToUpper(protocol), colorReset)
	time.Sleep(300 * time.Millisecond)

	conn, err := net.DialTimeout(protocol, serverAddr, 2*time.Second)
	if err != nil {
		fmt.Printf("%s✗ Server not reachable!%s\n\n", colorRed, colorReset)
		fmt.Printf("%sStart the server first:%s\n", colorYellow, colorReset)
		fmt.Println("  ./server.exe --store-url=\"postgresql://...\" --cache-max-size=1 --tcp")
		os.Exit(1)
	}
	conn.Close()

	fmt.Printf("%s✓ Server is running!%s\n", colorGreen, colorReset)
	fmt.Printf("\n%sPress ENTER to continue...%s", colorGreen, colorReset)
}

func registerTasks() []string {
	fmt.Printf("%s%sRegistering Test Tasks%s\n\n", colorBold, colorCyan, colorReset)

	taskNames := []string{"task_001", "task_002", "task_003", "task_004", "task_005"}

	for i, task := range taskNames {
		address := fmt.Sprintf("192.168.1.%d:8000", 10+i)
		if sendRegister(task, address) {
			fmt.Printf("%s✓%s Registered: %s -> %s\n", colorGreen, colorReset, task, address)
		} else {
			fmt.Printf("%s✗%s Failed to register %s\n", colorRed, colorReset, task)
		}
	}

	fmt.Printf("\n%sPress ENTER to continue...%s", colorGreen, colorReset)
	return taskNames
}

func performConsistentQueries(task string, count int) []time.Duration {
	fmt.Printf("%s%sQuerying '%s' %d times...%s\n", colorBold, colorGreen, task, count, colorReset)
	fmt.Printf("%s(Expect fast responses - sub-millisecond after first query)%s\n\n", colorYellow, colorReset)

	latencies := make([]time.Duration, count)
	successCount := 0

	for i := 0; i < count; i++ {
		start := time.Now()
		success := sendQuery(task)
		latency := time.Since(start)
		latencyMs := float64(latency) / float64(time.Millisecond)
		latencies[i] = latency

		if success {
			successCount++
		}

		resultIndicator := "✓"
		if !success {
			resultIndicator = "✗"
		}

		fmt.Printf("  Query %2d: %s %8.3fms   %s\n", i+1, resultIndicator, latencyMs, visualizeLatency(latency))

		time.Sleep(50 * time.Millisecond)
	}

	if successCount < count {
		fmt.Printf("\n%sWarning: %d/%d queries failed%s\n", colorRed, count-successCount, count, colorReset)
	}

	return latencies
}

func performRotatingQueries(tasks []string, queriesPerTask int) []time.Duration {
	fmt.Printf("%s%sQuerying %d different tasks, %d times each...%s\n",
		colorBold, colorRed, len(tasks), queriesPerTask, colorReset)
	fmt.Printf("%s(Expect slower responses - each requires database lookup)%s\n\n", colorYellow, colorReset)

	latencies := make([]time.Duration, 0)

	for round := 0; round < queriesPerTask; round++ {
		for i, task := range tasks {
			start := time.Now()
			sendQuery(task)
			latency := time.Since(start)
			latencyMs := float64(latency) / float64(time.Millisecond)
			latencies = append(latencies, latency)

			queryNum := round*len(tasks) + i + 1
			fmt.Printf("  Query %2d: 🗄️  CACHE MISS   %8.3fms   %s\n", queryNum, latencyMs, visualizeLatency(latency))

			time.Sleep(50 * time.Millisecond)
		}
	}

	return latencies
}

func sendRegister(task, address string) bool {
	msg := Message{
		Command:  "REGISTER",
		Task:     task,
		Address:  address,
		Capacity: 1,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		fmt.Printf("%s[Register Error: JSON marshal failed]%s\n", colorRed, colorReset)
		return false
	}

	conn, err := net.DialTimeout(protocol, serverAddr, 2*time.Second)
	if err != nil {
		fmt.Printf("%s[Register Error: Cannot connect to %s - %v]%s\n", colorRed, serverAddr, err, colorReset)
		return false
	}
	defer conn.Close()

	// Send request with newline terminator for TCP
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	_, err = conn.Write(append(data, '\n'))
	if err != nil {
		fmt.Printf("%s[Register Error: Write failed - %v]%s\n", colorRed, err, colorReset)
		return false
	}

	// Read response line by line
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		fmt.Printf("%s[Register Error: Read failed - %v]%s\n", colorRed, err, colorReset)
		return false
	}

	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		fmt.Printf("%s[Register Error: Response parse failed - %v]%s\n", colorRed, err, colorReset)
		return false
	}

	return resp.Status == "OK"
}

func sendQuery(task string) bool {
	msg := Message{
		Command: "QUERY",
		Task:    task,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return false
	}

	conn, err := net.DialTimeout(protocol, serverAddr, 2*time.Second)
	if err != nil {
		return false
	}
	defer conn.Close()

	// Send request with newline terminator for TCP
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	_, err = conn.Write(append(data, '\n'))
	if err != nil {
		return false
	}

	// Read response line by line
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return false
	}

	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return false
	}

	return resp.Status == "OK"
}

func visualizeLatency(latency time.Duration) string {
	millis := float64(latency.Microseconds()) / 1000.0

	// Scale: each 0.5ms = one block
	blocks := int(millis / 0.5)
	if blocks > 40 {
		blocks = 40
	}
	if blocks == 0 && latency > 0 {
		blocks = 1 // Show at least one block for very small latencies
	}

	bar := strings.Repeat("█", blocks) + strings.Repeat("░", 40-blocks)
	return fmt.Sprintf("[%s]", bar)
}

func showLatencies(title string, latencies []time.Duration) {
	fmt.Printf("%s%s%s%s\n\n", colorBold, colorCyan, title, colorReset)

	if len(latencies) == 0 {
		fmt.Printf("%sNo data%s\n", colorRed, colorReset)
		return
	}

	var min, max, total time.Duration
	min = latencies[0]
	max = latencies[0]

	for _, lat := range latencies {
		if lat < min {
			min = lat
		}
		if lat > max {
			max = lat
		}
		total += lat
	}

	avg := total / time.Duration(len(latencies))

	fmt.Printf("%sStatistics:%s\n", colorBold, colorReset)
	fmt.Printf("  Min:     %s%v%s\n", colorGreen, min, colorReset)
	fmt.Printf("  Max:     %s%v%s\n", colorRed, max, colorReset)
	fmt.Printf("  Avg:     %s%v%s\n", colorYellow, avg, colorReset)
	fmt.Printf("  Total:   %s%v%s\n", colorCyan, total, colorReset)

	fmt.Printf("\n%sPress ENTER to continue...%s", colorGreen, colorReset)
}

func showCacheTransitionAnalysis(primeLatencies, task1Latencies []time.Duration) {
	if len(task1Latencies) == 0 {
		fmt.Printf("%sInsufficient data%s\n", colorRed, colorReset)
		return
	}

	fmt.Printf("%sCache Transition Analysis:%s\n\n", colorBold, colorReset)

	// Show the first query (eviction + load)
	if len(task1Latencies) > 0 {
		fmt.Printf("%sQuery 1 (task_001) - EVICTION + LOAD:%s\n", colorRed, colorReset)
		fmt.Printf("  Latency: %v%s\n", task1Latencies[0], colorReset)
		fmt.Printf("  Action: task_002 evicted from cache, task_001 loaded from DB\n\n")
	}

	// Show the cached queries (2-10)
	if len(task1Latencies) > 1 {
		fmt.Printf("%sQueries 2-10 (task_001) - CACHE HITS:%s\n", colorGreen, colorReset)

		var cachedTotal time.Duration
		for i := 1; i < len(task1Latencies); i++ {
			cachedTotal += task1Latencies[i]
		}
		cachedAvg := cachedTotal / time.Duration(len(task1Latencies)-1)

		minCached := task1Latencies[1]
		maxCached := task1Latencies[1]
		for i := 2; i < len(task1Latencies); i++ {
			if task1Latencies[i] < minCached {
				minCached = task1Latencies[i]
			}
			if task1Latencies[i] > maxCached {
				maxCached = task1Latencies[i]
			}
		}

		fmt.Printf("  Min: %v  |  Avg: %v  |  Max: %v\n\n", minCached, cachedAvg, maxCached)

		// Calculate speedup
		speedup := float64(task1Latencies[0]) / float64(cachedAvg)
		fmt.Printf("%sFirst query is %s%.1fx SLOWER%s than cached queries\n", colorBold, colorRed, speedup, colorReset)
	}

	fmt.Printf("\n%sKey Observations:%s\n", colorBold, colorReset)
	fmt.Printf("  • Query 1 requires:\n")
	fmt.Printf("    - Evicting task_002 from cache\n")
	fmt.Printf("    - Loading task_001 from database\n")
	fmt.Printf("  • Queries 2-10 all serve from cache memory (very fast)\n")
	fmt.Printf("  • With cache-max-size=3, we could hold more tasks\n")
}

func pause() {
	fmt.Scanln()
}
