package dht_test

import (
	"testing"
	"time"

	"tds/pkg/dht"
)

// TestDHTQueryForwarding verifies that queries are forwarded to k-nearest nodes
// when the querying node is not responsible for the key
func TestDHTQueryForwarding(t *testing.T) {
	// Create bootstrap node
	bootstrap, err := dht.NewDHT("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create bootstrap DHT: %v", err)
	}

	bootstrapNetwork := dht.NewDHTNetwork(bootstrap, nil)
	if err := bootstrapNetwork.Start(); err != nil {
		t.Fatalf("Failed to start bootstrap network: %v", err)
	}
	t.Cleanup(func() {
		_ = bootstrapNetwork.Stop()
	})

	// Wait for bootstrap to fully start
	time.Sleep(100 * time.Millisecond)
	bootstrapAddr := bootstrap.GetSelf().Address

	// Create node A (will store the value)
	nodeA, err := dht.NewDHT("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create node A: %v", err)
	}

	networkA := dht.NewDHTNetwork(nodeA, []string{bootstrapAddr})
	if err := networkA.Start(); err != nil {
		t.Fatalf("Failed to start network A: %v", err)
	}
	t.Cleanup(func() {
		_ = networkA.Stop()
	})

	// Create node B (will query for the value)
	nodeB, err := dht.NewDHT("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create node B: %v", err)
	}

	networkB := dht.NewDHTNetwork(nodeB, []string{bootstrapAddr})
	if err := networkB.Start(); err != nil {
		t.Fatalf("Failed to start network B: %v", err)
	}
	t.Cleanup(func() {
		_ = networkB.Stop()
	})

	// Wait for peer discovery to complete (peerDiscoveryLoop runs every 1s, need time for full convergence)
	time.Sleep(2 * time.Second)

	// Store a value via network layer (with replication)
	testTask := "forwarding-test-task"
	testAddr := "10.0.0.42:9000"

	if err := networkA.Store(testTask, testAddr); err != nil {
		t.Fatalf("Failed to store: %v", err)
	}

	// Wait for replication
	time.Sleep(300 * time.Millisecond)

	// Query from node B (which may not be responsible)
	// The query should either:
	// 1. Find it locally (if B is in k-nearest), or
	// 2. Forward to k-nearest and find it there
	addrs := nodeB.LookupTask(testTask)
	if len(addrs) == 0 {
		t.Errorf("Query forwarding failed: task not found")
	}

	found := false
	for _, addr := range addrs {
		if addr == testAddr {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Query forwarding returned wrong addresses: got %v, want %s", addrs, testAddr)
	}

	t.Logf("Query forwarding successful: found %s via DHT lookup", testTask)
}

// TestDHTQueryForwardingNotFound verifies that forwarded queries
// properly return not-found when the key doesn't exist anywhere
func TestDHTQueryForwardingNotFound(t *testing.T) {
	bootstrap, err := dht.NewDHT("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create bootstrap DHT: %v", err)
	}

	bootstrapNetwork := dht.NewDHTNetwork(bootstrap, nil)
	if err := bootstrapNetwork.Start(); err != nil {
		t.Fatalf("Failed to start bootstrap network: %v", err)
	}
	t.Cleanup(func() {
		_ = bootstrapNetwork.Stop()
	})

	time.Sleep(100 * time.Millisecond)
	bootstrapAddr := bootstrap.GetSelf().Address

	nodeA, err := dht.NewDHT("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create node A: %v", err)
	}

	networkA := dht.NewDHTNetwork(nodeA, []string{bootstrapAddr})
	if err := networkA.Start(); err != nil {
		t.Fatalf("Failed to start network A: %v", err)
	}
	t.Cleanup(func() {
		_ = networkA.Stop()
	})

	time.Sleep(300 * time.Millisecond)

	// Query for non-existent task
	addrs := nodeA.LookupTask("does-not-exist")
	if len(addrs) > 0 {
		t.Errorf("Expected empty result for non-existent task, got %v", addrs)
	}

	t.Logf("Not-found case handled correctly")
}
