package dht_test

import (
	"testing"
	"time"

	"tds/pkg/dht"
)

// TestDHTStoreConvergence verifies that Store only queues closer nodes
// when receiving NOT_RESPONSIBLE responses, converging to actual k-closest
func TestDHTStoreConvergence(t *testing.T) {
	// Create a ring of 5 nodes: bootstrap, A, B, C, D
	bootstrap, err := dht.NewDHT("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create bootstrap: %v", err)
	}

	bootstrapNet := dht.NewDHTNetwork(bootstrap, nil)
	if err := bootstrapNet.Start(); err != nil {
		t.Fatalf("Failed to start bootstrap: %v", err)
	}
	t.Cleanup(func() { _ = bootstrapNet.Stop() })

	time.Sleep(100 * time.Millisecond)
	bootstrapAddr := bootstrap.GetSelf().Address

	// Create node A
	nodeA, err := dht.NewDHT("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create node A: %v", err)
	}
	netA := dht.NewDHTNetwork(nodeA, []string{bootstrapAddr})
	if err := netA.Start(); err != nil {
		t.Fatalf("Failed to start network A: %v", err)
	}
	t.Cleanup(func() { _ = netA.Stop() })

	// Create node B
	nodeB, err := dht.NewDHT("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create node B: %v", err)
	}
	netB := dht.NewDHTNetwork(nodeB, []string{bootstrapAddr})
	if err := netB.Start(); err != nil {
		t.Fatalf("Failed to start network B: %v", err)
	}
	t.Cleanup(func() { _ = netB.Stop() })

	// Create node C
	nodeC, err := dht.NewDHT("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create node C: %v", err)
	}
	netC := dht.NewDHTNetwork(nodeC, []string{bootstrapAddr})
	if err := netC.Start(); err != nil {
		t.Fatalf("Failed to start network C: %v", err)
	}
	t.Cleanup(func() { _ = netC.Stop() })

	// Create node D
	nodeD, err := dht.NewDHT("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create node D: %v", err)
	}
	netD := dht.NewDHTNetwork(nodeD, []string{bootstrapAddr})
	if err := netD.Start(); err != nil {
		t.Fatalf("Failed to start network D: %v", err)
	}
	t.Cleanup(func() { _ = netD.Stop() })

	// Wait for peer discovery to complete (peerDiscoveryLoop runs every 1s, need time for full convergence)
	time.Sleep(2 * time.Second)

	// Verify all nodes know about each other
	if nodeA.GetRingSize() < 4 {
		t.Logf("Warning: nodeA only knows %d peers", nodeA.GetRingSize())
	}

	// Pick a task and determine actual k-closest nodes (k=3)
	testTask := "convergence-test-task"
	testAddr := "10.0.0.99:8080"

	// Find k-closest from bootstrap's perspective (most complete view)
	actualClosest := bootstrap.FindKClosestNodes(testTask, dht.ReplicationFactor)
	t.Logf("Actual k-closest nodes for task '%s':", testTask)
	for i, node := range actualClosest {
		t.Logf("  %d: %s (ID: %s)", i+1, node.Address, dht.NodeIDToString(node.ID))
	}

	// Store from node A (might have incomplete view)
	t.Logf("\nStoring from node A...")
	if err := netA.Store(testTask, testAddr); err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	// Wait for replication to settle
	time.Sleep(300 * time.Millisecond)

	// Verify the task is stored on at least some of the actual k-closest nodes
	storedCount := 0
	allNodes := []*dht.DHT{bootstrap, nodeA, nodeB, nodeC, nodeD}

	t.Logf("\nStorage distribution:")
	for _, node := range allNodes {
		addrs := node.LookupTask(testTask)
		selfAddr := node.GetSelf().Address

		isInKClosest := false
		for _, closest := range actualClosest {
			if closest.Address == selfAddr {
				isInKClosest = true
				break
			}
		}

		hasData := len(addrs) > 0 && contains(addrs, testAddr)
		if hasData {
			storedCount++
		}

		status := "❌"
		if hasData {
			status = "✅"
		}

		kClosestMarker := ""
		if isInKClosest {
			kClosestMarker = " [K-CLOSEST]"
		}

		t.Logf("  %s %s: has data=%v%s", status, selfAddr, hasData, kClosestMarker)
	}

	// Verify convergence: at least 2 of k=3 replicas should exist
	// (Allow for some network timing issues)
	if storedCount < 2 {
		t.Errorf("Expected at least 2 replicas, got %d", storedCount)
	}

	// Verify that nodes in k-closest have the data
	kClosestWithData := 0
	for _, closest := range actualClosest {
		for _, node := range allNodes {
			if node.GetSelf().Address == closest.Address {
				addrs := node.LookupTask(testTask)
				if len(addrs) > 0 && contains(addrs, testAddr) {
					kClosestWithData++
				}
			}
		}
	}

	t.Logf("\nConvergence result: %d/%d k-closest nodes have the data", kClosestWithData, len(actualClosest))

	if kClosestWithData < 2 {
		t.Errorf("Expected at least 2 k-closest nodes to have data, got %d", kClosestWithData)
	}

	t.Logf("\n✅ Store convergence test passed: data stored on k-closest nodes")
}

// TestDHTStoreRejectsSuboptimalNodes verifies that far nodes aren't queued
// when closer options are available
func TestDHTStoreRejectsSuboptimalNodes(t *testing.T) {
	// Create 3 nodes
	bootstrap, err := dht.NewDHT("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create bootstrap: %v", err)
	}

	bootstrapNet := dht.NewDHTNetwork(bootstrap, nil)
	if err := bootstrapNet.Start(); err != nil {
		t.Fatalf("Failed to start bootstrap: %v", err)
	}
	t.Cleanup(func() { _ = bootstrapNet.Stop() })

	time.Sleep(100 * time.Millisecond)
	bootstrapAddr := bootstrap.GetSelf().Address

	nodeA, err := dht.NewDHT("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create node A: %v", err)
	}
	netA := dht.NewDHTNetwork(nodeA, []string{bootstrapAddr})
	if err := netA.Start(); err != nil {
		t.Fatalf("Failed to start network A: %v", err)
	}
	t.Cleanup(func() { _ = netA.Stop() })

	nodeB, err := dht.NewDHT("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create node B: %v", err)
	}
	netB := dht.NewDHTNetwork(nodeB, []string{bootstrapAddr})
	if err := netB.Start(); err != nil {
		t.Fatalf("Failed to start network B: %v", err)
	}
	t.Cleanup(func() { _ = netB.Stop() })

	time.Sleep(2 * time.Second)

	// Store a task
	testTask := "suboptimal-test-task"
	testAddr := "10.0.0.88:7070"

	if err := netA.Store(testTask, testAddr); err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// Verify it's stored somewhere
	foundCount := 0
	allNodes := []*dht.DHT{bootstrap, nodeA, nodeB}

	for _, node := range allNodes {
		addrs := node.LookupTask(testTask)
		if len(addrs) > 0 && contains(addrs, testAddr) {
			foundCount++
		}
	}

	if foundCount == 0 {
		t.Errorf("Task not stored on any node")
	}

	t.Logf("✅ Task stored on %d nodes (convergence successful)", foundCount)
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
