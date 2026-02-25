package main

import (
	"fmt"
	"math/big"
	"net"
	"os"
	"sort"
	"strings"
	"time"

	"tds/pkg/dht"
)

type nodeInfo struct {
	name string
	addr string
	reg  *dht.DHTRegistry
}

type viewNode struct {
	addr string
	id   dht.NodeID
}

type distNode struct {
	addr string
	dist *big.Int
}

func main() {
	fmt.Println("P2P k-nearest neighbors demo")
	fmt.Println(strings.Repeat("-", 60))

	nodes := make([]nodeInfo, 0, 6)
	addNode := func(node nodeInfo) nodeInfo {
		nodes = append(nodes, node)
		return node
	}
	defer func() {
		stopAll(nodes)
	}()

	// Step 1: Start bootstrap A1.
	a1 := addNode(startRegistry("A1", nil))

	// Step 2: Start A2/A3 joining A1.
	a2 := addNode(startRegistry("A2", []string{a1.addr}))
	_ = addNode(startRegistry("A3", []string{a1.addr}))

	// Step 3: Start bootstrap B1, which joins A1.
	b1 := addNode(startRegistry("B1", []string{a1.addr}))

	// Step 4: Start B2/B3 joining B1.
	b2 := addNode(startRegistry("B2", []string{b1.addr}))
	_ = addNode(startRegistry("B3", []string{b1.addr}))

	// Wait for initial peer discovery
	time.Sleep(500 * time.Millisecond)
	
	// Wait for nodes to learn about their neighbors
	waitRingSizeAtLeast("A1", a1, 3)
	waitRingSizeAtLeast("A2", a2, 2)
	waitRingSizeAtLeast("B1", b1, 3)
	waitRingSizeAtLeast("B2", b2, 2)

	fmt.Println("\nCurrent peer views:")
	printInfo(a1)
	printInfo(a2)
	printInfo(b1)
	printInfo(b2)

	// Pick a task so that A1 is in B2's k-closest set.
	task, kclosest := pickTaskIncludingTarget("svc", b2, a1.addr, dht.ReplicationFactor)
	fmt.Println("\nTask selection:")
	fmt.Printf("- Selected task: %s\n", task)
	fmt.Printf("- B2 k-closest: %s\n", strings.Join(kclosest, ", "))
	fmt.Printf("- A2 peers: %s\n", strings.Join(getPeerList(a2), ", "))
	fmt.Printf("- A2 does not know B2: %v\n", !contains(getPeerList(a2), b2.addr))

	serviceAddr := "10.0.0.42:9000"
	fmt.Println("\nRegister on B2 (replicated to k-closest nodes):")
	if err := b2.reg.Register(task, serviceAddr); err != nil {
		exitf("register failed: %v", err)
	}

	// Query from A2, which does not know B2. Should succeed via replica on A1.
	fmt.Println("\nQuery from A2 (does not know B2):")
	addr, err := a2.reg.Query(task)
	if err != nil {
		exitf("query failed: %v", err)
	}
	if addr == "" {
		exitf("query returned empty address")
	}
	fmt.Printf("- A2 query result: %s\n", addr)
	fmt.Printf("- Expected: %s\n", serviceAddr)

	if addr == serviceAddr {
		fmt.Println("\nResult: SUCCESS - k-nearest replication provided the service via known peers.")
	} else {
		fmt.Println("\nResult: UNEXPECTED - response does not match registered service.")
	}
}

func startRegistry(name string, bootstraps []string) nodeInfo {
	addr := getFreeAddr()
	reg, err := dht.NewDHTRegistry(addr, bootstraps)
	if err != nil {
		exitf("%s new registry: %v", name, err)
	}
	if err := reg.Start(); err != nil {
		exitf("%s start: %v", name, err)
	}
	return nodeInfo{name: name, addr: addr, reg: reg}
}

func getFreeAddr() string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		exitf("listen for free port: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	_ = ln.Close()
	return fmt.Sprintf("127.0.0.1:%d", addr.Port)
}

func waitRingSizeAtLeast(name string, node nodeInfo, expected int) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		info := node.reg.GetDHTInfo()
		size := getRingSize(info)
		if size >= expected {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	info := node.reg.GetDHTInfo()
	exitf("%s ring size timeout: got %d", name, getRingSize(info))
}

func getRingSize(info map[string]interface{}) int {
	v, ok := info["ring_size"]
	if !ok {
		return 0
	}
	switch size := v.(type) {
	case int:
		return size
	case int64:
		return int(size)
	case float64:
		return int(size)
	default:
		return 0
	}
}

func getPeerList(node nodeInfo) []string {
	info := node.reg.GetDHTInfo()
	peerRaw, ok := info["peers"]
	if !ok {
		return nil
	}
	peers, ok := peerRaw.([]string)
	if ok {
		return peers
	}
	// Handle []interface{} from map serialization
	ifaceList, ok := peerRaw.([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(ifaceList))
	for _, item := range ifaceList {
		if addr, ok := item.(string); ok {
			result = append(result, addr)
		}
	}
	return result
}

func getView(node nodeInfo) []viewNode {
	info := node.reg.GetDHTInfo()
	selfAddr, _ := info["address"].(string)
	peers := getPeerList(node)
	addrs := append([]string{selfAddr}, peers...)
	view := make([]viewNode, 0, len(addrs))
	for _, addr := range addrs {
		view = append(view, viewNode{addr: addr, id: dht.HashAddress(addr)})
	}
	return view
}

func pickTaskIncludingTarget(prefix string, node nodeInfo, targetAddr string, k int) (string, []string) {
	view := getView(node)
	for i := 0; i < 5000; i++ {
		task := fmt.Sprintf("%s-%d", prefix, i)
		kclosest := kClosest(task, view, k)
		if contains(kclosest, targetAddr) {
			return task, kclosest
		}
	}
	return fmt.Sprintf("%s-fallback", prefix), kClosest(fmt.Sprintf("%s-fallback", prefix), view, k)
}

func kClosest(task string, view []viewNode, k int) []string {
	taskID := dht.HashTask(task)
	distances := make([]distNode, 0, len(view))
	for _, node := range view {
		distances = append(distances, distNode{
			addr: node.addr,
			dist: dht.Distance(taskID, node.id),
		})
	}
	sort.Slice(distances, func(i, j int) bool {
		return distances[i].dist.Cmp(distances[j].dist) < 0
	})
	result := make([]string, 0, k)
	for i := 0; i < k && i < len(distances); i++ {
		result = append(result, distances[i].addr)
	}
	return result
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func printInfo(node nodeInfo) {
	info := node.reg.GetDHTInfo()
	addr, _ := info["address"].(string)
	ringSize := getRingSize(info)
	peers := getPeerList(node)
	fmt.Printf("- %s addr=%s ring=%d peers=%v\n", node.name, addr, ringSize, peers)
}

func stopAll(nodes []nodeInfo) {
	for _, node := range nodes {
		_ = node.reg.Stop()
	}
}

func exitf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
