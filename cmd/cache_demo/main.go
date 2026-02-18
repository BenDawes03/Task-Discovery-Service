package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"runtime"
	"sort"
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
}

type Response struct {
	Status  string `json:"status"`
	Address string `json:"address,omitempty"`
	Error   string `json:"error,omitempty"`
}

var (
	serverAddr = "localhost:5000"
	protocol   = "tcp" // "tcp" or "udp" - must match server mode
)

type QueryResult struct {
	task     string
	duration time.Duration
	success  bool
}

func main() {
	clearScreen()
	printBanner()
	pause()

	// Check server
	clearScreen()
	checkServer()
	pause()

	// Setup: Register many services
	clearScreen()
	numTasks := 30
	servicesPerTask := 3
	taskNames := registerManyServices(numTasks, servicesPerTask)
	pause()

	// Warm-up: Prime the cache with popular tasks
	clearScreen()
	popularTasks := taskNames[:10] // First 10 tasks
	warmupCache(popularTasks)
	pause()

	// Phase 1: Query popular tasks (cache hits)
	clearScreen()
	cacheHitResults := performQueries("Cache Hit Test (Popular Tasks)", popularTasks, 50)
	pause()

	// Phase 2: Query unpopular tasks (cache misses)
	clearScreen()
	unpopularTasks := taskNames[20:] // Last 10 tasks
	cacheMissResults := performQueries("Cache Miss Test (Unpopular Tasks)", unpopularTasks, 50)
	pause()

	// Phase 3: Mixed workload
	clearScreen()
	mixedResults := performMixedWorkload(popularTasks, unpopularTasks, 100)
	pause()

	// Show comparison
	clearScreen()
	showComparison(cacheHitResults, cacheMissResults, mixedResults)
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
	fmt.Println("║           TDS Cache Performance Demonstration                  ║")
	fmt.Println("║                                                                ║")
	fmt.Println("╚════════════════════════════════════════════════════════════════╝")
	fmt.Printf("%s\n", colorReset)

	fmt.Printf("%s", colorWhite)
	fmt.Println("\nThis demo compares query performance for:")
	fmt.Printf("%s", colorYellow)
	fmt.Println("  ✓ Cache hits (tasks in memory cache)")
	fmt.Println("  ✓ Cache misses (tasks requiring database lookup)")
	fmt.Println("  ✓ Mixed workload scenarios")
	fmt.Printf("%s\n", colorReset)

	fmt.Printf("%sNote:%s This demo requires the server to be running with:\n", colorYellow, colorReset)
	fmt.Println("  --store-url <db_url> --cache-max-size 10")
	fmt.Println("\nExample:")
	fmt.Println("  ./server.exe --store-url=\"postgresql://user:pass@localhost:5432/tds\" --cache-max-size=10")

	fmt.Printf("\n%sPress ENTER to begin...%s", colorGreen, colorReset)
}

func checkServer() {
	fmt.Printf("%s%sStep 1: Checking TDS Server%s\n\n", colorBold, colorCyan, colorReset)

	fmt.Printf("%s⏳ Connecting to server at %s using %s...%s\n", colorYellow, serverAddr, strings.ToUpper(protocol), colorReset)
	time.Sleep(500 * time.Millisecond)

	conn, err := net.DialTimeout(protocol, serverAddr, 2*time.Second)
	if err != nil {
		fmt.Printf("%s✗ Server not reachable!%s\n\n", colorRed, colorReset)
		fmt.Printf("%sPlease start the TDS server with database persistence:%s\n", colorYellow, colorReset)
		fmt.Println("  ./server.exe --store-url=\"postgresql://...\" --cache-max-size=10")
		os.Exit(1)
	}
	conn.Close()

	fmt.Printf("%s✓ Server is running!%s\n", colorGreen, colorReset)
	fmt.Printf("\n%sPress ENTER to continue...%s", colorGreen, colorReset)
}

func registerManyServices(numTasks, servicesPerTask int) []string {
	fmt.Printf("%s%sStep 2: Registering %d Tasks with %d Services Each%s\n\n",
		colorBold, colorCyan, numTasks, servicesPerTask, colorReset)

	taskNames := make([]string, numTasks)
	totalServices := numTasks * servicesPerTask

	fmt.Printf("%sRegistering %d total services...%s\n\n", colorWhite, totalServices, colorReset)

	registered := 0
	failed := 0

	// Progress bar
	progressWidth := 50

	for i := 0; i < numTasks; i++ {
		taskNames[i] = fmt.Sprintf("task_%03d", i)

		for j := 0; j < servicesPerTask; j++ {
			address := fmt.Sprintf("192.168.1.%d:%d", 10+i, 8000+j)

			if sendRegister(taskNames[i], address) {
				registered++
			} else {
				failed++
			}

			// Update progress bar
			current := i*servicesPerTask + j + 1
			progress := float64(current) / float64(totalServices)
			filled := int(progress * float64(progressWidth))

			bar := strings.Repeat("█", filled) + strings.Repeat("░", progressWidth-filled)
			fmt.Printf("\r%s[%s]%s %d/%d services (%.1f%%) ",
				colorGreen, bar, colorReset, current, totalServices, progress*100)
		}
	}

	fmt.Printf("\n\n%s✓ Registration complete!%s\n", colorGreen, colorReset)
	fmt.Printf("  %sSuccessful: %d%s\n", colorGreen, registered, colorReset)
	if failed > 0 {
		fmt.Printf("  %sFailed: %d%s\n", colorRed, failed, colorReset)
	}

	fmt.Printf("\n%sPress ENTER to continue...%s", colorGreen, colorReset)
	return taskNames
}

func warmupCache(tasks []string) {
	fmt.Printf("%s%sStep 3: Warming Cache with Popular Tasks%s\n\n", colorBold, colorCyan, colorReset)

	fmt.Printf("%sQuerying each task once to populate cache...%s\n\n", colorWhite, colorReset)

	successCount := 0

	progressWidth := 50

	for i, task := range tasks {
		if _, success := sendQuery(task); success {
			successCount++
		}

		// Update progress bar
		progress := float64(i+1) / float64(len(tasks))
		filled := int(progress * float64(progressWidth))

		bar := strings.Repeat("█", filled) + strings.Repeat("░", progressWidth-filled)
		fmt.Printf("\r%s[%s]%s %d/%d tasks (%.1f%%) ",
			colorGreen, bar, colorReset, i+1, len(tasks), progress*100)

		// Small delay to ensure query completes
		time.Sleep(10 * time.Millisecond)
	}

	fmt.Printf("\n\n%s✓ Cache warmed!%s\n", colorGreen, colorReset)
	fmt.Printf("  %sSuccessful: %d/%d%s\n", colorGreen, successCount, len(tasks), colorReset)
	fmt.Printf("\n%sNote:%s Popular tasks are now in the server's in-memory cache.\n", colorYellow, colorReset)
	fmt.Printf("Subsequent queries should be served directly from memory.\n")

	fmt.Printf("\n%sPress ENTER to continue...%s", colorGreen, colorReset)
}

func performQueries(title string, tasks []string, queriesPerTask int) []QueryResult {
	fmt.Printf("%s%s%s%s\n\n", colorBold, colorCyan, title, colorReset)

	totalQueries := len(tasks) * queriesPerTask
	fmt.Printf("%sExecuting %d queries across %d tasks...%s\n\n",
		colorWhite, totalQueries, len(tasks), colorReset)

	results := make([]QueryResult, 0, totalQueries)
	var mu sync.Mutex
	var wg sync.WaitGroup

	start := time.Now()

	// Progress tracking
	completed := 0
	ticker := time.NewTicker(50 * time.Millisecond)
	done := make(chan bool)

	go func() {
		for {
			select {
			case <-ticker.C:
				mu.Lock()
				c := completed
				mu.Unlock()
				progress := float64(c) / float64(totalQueries)
				filled := int(progress * 50)
				bar := strings.Repeat("█", filled) + strings.Repeat("░", 50-filled)
				fmt.Printf("\r%s[%s]%s %d/%d queries (%.1f%%) ",
					colorYellow, bar, colorReset, c, totalQueries, progress*100)
			case <-done:
				return
			}
		}
	}()

	// Execute queries concurrently
	for _, task := range tasks {
		for i := 0; i < queriesPerTask; i++ {
			wg.Add(1)
			go func(t string) {
				defer wg.Done()
				queryStart := time.Now()
				_, success := sendQuery(t)
				duration := time.Since(queryStart)

				mu.Lock()
				results = append(results, QueryResult{
					task:     t,
					duration: duration,
					success:  success,
				})
				completed++
				mu.Unlock()
			}(task)

			// Slight delay to avoid overwhelming the server
			time.Sleep(2 * time.Millisecond)
		}
	}

	wg.Wait()
	ticker.Stop()
	done <- true

	elapsed := time.Since(start)

	fmt.Printf("\r%s[%s]%s %d/%d queries (100.0%%) \n\n",
		colorGreen, strings.Repeat("█", 50), colorReset, totalQueries, totalQueries)

	// Calculate statistics
	var totalDuration time.Duration
	successCount := 0
	for _, r := range results {
		totalDuration += r.duration
		if r.success {
			successCount++
		}
	}
	avgDuration := totalDuration / time.Duration(len(results))

	// Calculate percentiles
	sort.Slice(results, func(i, j int) bool {
		return results[i].duration < results[j].duration
	})

	p50 := results[len(results)/2].duration
	p95 := results[int(float64(len(results))*0.95)].duration
	p99 := results[int(float64(len(results))*0.99)].duration

	fmt.Printf("%sResults:%s\n", colorBold, colorReset)
	fmt.Printf("  Total time:        %s%s%s\n", colorCyan, elapsed, colorReset)
	fmt.Printf("  Queries/sec:       %s%.2f%s\n", colorYellow, float64(totalQueries)/elapsed.Seconds(), colorReset)
	fmt.Printf("  Success rate:      %s%d/%d (%.1f%%)%s\n",
		colorGreen, successCount, totalQueries, float64(successCount)/float64(totalQueries)*100, colorReset)
	fmt.Printf("  Avg latency:       %s%v%s\n", colorCyan, avgDuration, colorReset)
	fmt.Printf("  Median (p50):      %s%v%s\n", colorCyan, p50, colorReset)
	fmt.Printf("  95th percentile:   %s%v%s\n", colorYellow, p95, colorReset)
	fmt.Printf("  99th percentile:   %s%v%s\n", colorYellow, p99, colorReset)

	fmt.Printf("\n%sPress ENTER to continue...%s", colorGreen, colorReset)
	return results
}

func performMixedWorkload(popularTasks, unpopularTasks []string, totalQueries int) []QueryResult {
	fmt.Printf("%s%sMixed Workload Test (80%% cache hits, 20%% misses)%s\n\n",
		colorBold, colorCyan, colorReset)

	fmt.Printf("%sExecuting %d queries with realistic distribution...%s\n\n",
		colorWhite, totalQueries, colorReset)

	results := make([]QueryResult, 0, totalQueries)
	var mu sync.Mutex
	var wg sync.WaitGroup

	start := time.Now()

	// Progress tracking
	completed := 0
	ticker := time.NewTicker(50 * time.Millisecond)
	done := make(chan bool)

	go func() {
		for {
			select {
			case <-ticker.C:
				mu.Lock()
				c := completed
				mu.Unlock()
				progress := float64(c) / float64(totalQueries)
				filled := int(progress * 50)
				bar := strings.Repeat("█", filled) + strings.Repeat("░", 50-filled)
				fmt.Printf("\r%s[%s]%s %d/%d queries (%.1f%%) ",
					colorPurple, bar, colorReset, c, totalQueries, progress*100)
			case <-done:
				return
			}
		}
	}()

	// Execute mixed queries
	for i := 0; i < totalQueries; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// 80% chance of querying popular task (cache hit)
			var task string
			if rand.Float64() < 0.8 {
				task = popularTasks[rand.Intn(len(popularTasks))]
			} else {
				task = unpopularTasks[rand.Intn(len(unpopularTasks))]
			}

			queryStart := time.Now()
			_, success := sendQuery(task)
			duration := time.Since(queryStart)

			mu.Lock()
			results = append(results, QueryResult{
				task:     task,
				duration: duration,
				success:  success,
			})
			completed++
			mu.Unlock()
		}()

		time.Sleep(2 * time.Millisecond)
	}

	wg.Wait()
	ticker.Stop()
	done <- true

	elapsed := time.Since(start)

	fmt.Printf("\r%s[%s]%s %d/%d queries (100.0%%) \n\n",
		colorGreen, strings.Repeat("█", 50), colorReset, totalQueries, totalQueries)

	// Calculate statistics
	var totalDuration time.Duration
	successCount := 0
	for _, r := range results {
		totalDuration += r.duration
		if r.success {
			successCount++
		}
	}
	avgDuration := totalDuration / time.Duration(len(results))

	// Calculate percentiles
	sort.Slice(results, func(i, j int) bool {
		return results[i].duration < results[j].duration
	})

	p50 := results[len(results)/2].duration
	p95 := results[int(float64(len(results))*0.95)].duration
	p99 := results[int(float64(len(results))*0.99)].duration

	fmt.Printf("%sResults:%s\n", colorBold, colorReset)
	fmt.Printf("  Total time:        %s%s%s\n", colorCyan, elapsed, colorReset)
	fmt.Printf("  Queries/sec:       %s%.2f%s\n", colorYellow, float64(totalQueries)/elapsed.Seconds(), colorReset)
	fmt.Printf("  Success rate:      %s%d/%d (%.1f%%)%s\n",
		colorGreen, successCount, totalQueries, float64(successCount)/float64(totalQueries)*100, colorReset)
	fmt.Printf("  Avg latency:       %s%v%s\n", colorCyan, avgDuration, colorReset)
	fmt.Printf("  Median (p50):      %s%v%s\n", colorCyan, p50, colorReset)
	fmt.Printf("  95th percentile:   %s%v%s\n", colorYellow, p95, colorReset)
	fmt.Printf("  99th percentile:   %s%v%s\n", colorYellow, p99, colorReset)

	fmt.Printf("\n%sPress ENTER to continue...%s", colorGreen, colorReset)
	return results
}

func showComparison(cacheHits, cacheMisses, mixed []QueryResult) {
	fmt.Printf("%s%sPerformance Comparison Summary%s\n\n", colorBold, colorCyan, colorReset)

	// Calculate averages
	avgHit := averageDuration(cacheHits)
	avgMiss := averageDuration(cacheMisses)
	avgMixed := averageDuration(mixed)

	// Calculate percentiles
	p95Hit := percentile(cacheHits, 0.95)
	p95Miss := percentile(cacheMisses, 0.95)
	p95Mixed := percentile(mixed, 0.95)

	// Speedup calculation
	speedup := float64(avgMiss) / float64(avgHit)

	fmt.Printf("%s┌─────────────────────────────────────────────────────────────┐%s\n", colorWhite, colorReset)
	fmt.Printf("%s│                    Average Latency                          │%s\n", colorWhite, colorReset)
	fmt.Printf("%s├─────────────────────────────────────────────────────────────┤%s\n", colorWhite, colorReset)
	fmt.Printf("%s│  Cache Hits (popular):   %-30v│%s\n", colorWhite, formatDuration(avgHit), colorReset)
	fmt.Printf("%s│  Cache Misses (rare):    %-30v│%s\n", colorWhite, formatDuration(avgMiss), colorReset)
	fmt.Printf("%s│  Mixed Workload:         %-30v│%s\n", colorWhite, formatDuration(avgMixed), colorReset)
	fmt.Printf("%s└─────────────────────────────────────────────────────────────┘%s\n\n", colorWhite, colorReset)

	fmt.Printf("%s┌─────────────────────────────────────────────────────────────┐%s\n", colorWhite, colorReset)
	fmt.Printf("%s│                  95th Percentile Latency                    │%s\n", colorWhite, colorReset)
	fmt.Printf("%s├─────────────────────────────────────────────────────────────┤%s\n", colorWhite, colorReset)
	fmt.Printf("%s│  Cache Hits (popular):   %-30v│%s\n", colorWhite, formatDuration(p95Hit), colorReset)
	fmt.Printf("%s│  Cache Misses (rare):    %-30v│%s\n", colorWhite, formatDuration(p95Miss), colorReset)
	fmt.Printf("%s│  Mixed Workload:         %-30v│%s\n", colorWhite, formatDuration(p95Mixed), colorReset)
	fmt.Printf("%s└─────────────────────────────────────────────────────────────┘%s\n\n", colorWhite, colorReset)

	// Visual comparison
	fmt.Printf("%sVisual Comparison (Average Latency):%s\n\n", colorBold, colorReset)

	maxBar := 60
	hitBar := int(float64(avgHit) / float64(avgMiss) * float64(maxBar))
	if hitBar < 1 {
		hitBar = 1
	}

	fmt.Printf("  Cache Hits:   %s%s%s %v\n", colorGreen, strings.Repeat("█", hitBar), colorReset, avgHit)
	fmt.Printf("  Cache Misses: %s%s%s %v\n", colorRed, strings.Repeat("█", maxBar), colorReset, avgMiss)

	mixedBar := int(float64(avgMixed) / float64(avgMiss) * float64(maxBar))
	if mixedBar < 1 {
		mixedBar = 1
	}
	fmt.Printf("  Mixed:        %s%s%s %v\n\n", colorYellow, strings.Repeat("█", mixedBar), colorReset, avgMixed)

	// Speedup
	fmt.Printf("%s╔═══════════════════════════════════════════════════════════╗%s\n", colorGreen, colorReset)
	fmt.Printf("%s║                                                           ║%s\n", colorGreen, colorReset)
	fmt.Printf("%s║  Cache Speedup: %.2fx faster                             ║%s\n", colorGreen, speedup, colorReset)
	fmt.Printf("%s║                                                           ║%s\n", colorGreen, colorReset)
	fmt.Printf("%s╚═══════════════════════════════════════════════════════════╝%s\n", colorGreen, colorReset)

	fmt.Println("\n" + colorYellow + "Key Takeaways:" + colorReset)
	fmt.Println("  • In-memory cache provides significantly faster response times")
	fmt.Println("  • Cache misses require database lookups, adding latency")
	fmt.Println("  • Even with 20% cache misses, mixed workload performs well")
	fmt.Println("  • Proper cache sizing is critical for performance")
}

func averageDuration(results []QueryResult) time.Duration {
	if len(results) == 0 {
		return 0
	}
	var total time.Duration
	for _, r := range results {
		total += r.duration
	}
	return total / time.Duration(len(results))
}

func percentile(results []QueryResult, p float64) time.Duration {
	if len(results) == 0 {
		return 0
	}
	sorted := make([]QueryResult, len(results))
	copy(sorted, results)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].duration < sorted[j].duration
	})
	idx := int(float64(len(sorted)) * p)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx].duration
}

func formatDuration(d time.Duration) string {
	if d < time.Microsecond {
		return fmt.Sprintf("%dns", d.Nanoseconds())
	} else if d < time.Millisecond {
		return fmt.Sprintf("%.2fµs", float64(d.Nanoseconds())/1000.0)
	} else if d < time.Second {
		return fmt.Sprintf("%.2fms", float64(d.Microseconds())/1000.0)
	}
	return d.String()
}

func sendRegister(task, address string) bool {
	msg := Message{
		Command: "REGISTER",
		Task:    task,
		Address: address,
	}

	resp, err := sendMessage(msg)
	if err != nil {
		return false
	}

	return resp.Status == "OK"
}

func sendQuery(task string) (string, bool) {
	msg := Message{
		Command: "QUERY",
		Task:    task,
	}

	resp, err := sendMessage(msg)
	if err != nil {
		return "", false
	}

	if resp.Status == "OK" && resp.Address != "" {
		return resp.Address, true
	}

	return "", false
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
