package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"sync"
	"time"
)

// Message/Response mirror the TDS protocol used by demos
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
	serverAddr = "127.0.0.1:5000"
	protocol   = "tcp"
)

func main() {
	var (
		numTasks         = flag.Int("tasks", 5, "number of distinct tasks to create")
		servicesPerTask  = flag.Int("services", 3, "services to register per task")
		clients          = flag.Int("clients", 10, "concurrent client goroutines")
		queriesPerClient = flag.Int("queries", 50, "queries per client")
		protoFlag        = flag.String("protocol", "tcp", "tcp or udp")
		addrFlag         = flag.String("server", "127.0.0.1:5000", "server address host:port")
	)
	flag.Parse()
	protocol = *protoFlag
	serverAddr = *addrFlag

	rand.Seed(time.Now().UnixNano())

	// create tasks and services
	tasks := make([]string, *numTasks)
	for i := 0; i < *numTasks; i++ {
		tasks[i] = fmt.Sprintf("task_auto_%d", i)
	}

	fmt.Printf("Registering %d tasks with %d services each against %s (%s)\n", *numTasks, *servicesPerTask, serverAddr, protocol)

	serviceCount := 0
	for ti, task := range tasks {
		for si := 0; si < *servicesPerTask; si++ {
			addr := fmt.Sprintf("127.0.0.1:%d", 8000+serviceCount)
			ok := sendRegisterWithCapacity(task, addr, 1)
			if !ok {
				fmt.Fprintf(os.Stderr, "failed to register %s@%s\n", task, addr)
			}
			serviceCount++
			// small pause to avoid overwhelming server
			time.Sleep(50 * time.Millisecond)
		}
		if ti%5 == 0 {
			// spacing
			time.Sleep(150 * time.Millisecond)
		}
	}

	// start clients
	fmt.Printf("Starting %d clients, each making %d random queries\n", *clients, *queriesPerClient)

	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	success := 0
	start := time.Now()

	for c := 0; c < *clients; c++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for q := 0; q < *queriesPerClient; q++ {
				task := tasks[rand.Intn(len(tasks))]
				_, ok := sendQuery(task)
				mu.Lock()
				total++
				if ok {
					success++
				}
				mu.Unlock()
				// random small backoff
				time.Sleep(time.Duration(rand.Intn(80)) * time.Millisecond)
			}
		}(c)
	}

	wg.Wait()
	elapsed := time.Since(start)

	fmt.Printf("Done: %d queries, %d successes, elapsed=%s, qps=%.1f\n", total, success, elapsed.Round(time.Millisecond), float64(total)/elapsed.Seconds())
}

func sendRegisterWithCapacity(task, address string, capacity int) bool {
	msg := Message{Command: "REGISTER", Task: task, Address: address, Capacity: capacity}
	resp, err := sendMessage(msg)
	if err != nil {
		return false
	}
	if resp.Status != "OK" {
		return false
	}
	return true
}

func sendQuery(task string) (string, bool) {
	msg := Message{Command: "QUERY", Task: task}
	resp, err := sendMessage(msg)
	if err != nil {
		return "", false
	}
	if resp.Status != "OK" {
		return "", false
	}
	return resp.Address, true
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
	data = append(data, '\n')

	conn, err := net.DialTimeout("tcp", serverAddr, 2*time.Second)
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
